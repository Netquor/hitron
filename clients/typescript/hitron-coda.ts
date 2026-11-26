/**
 * hitron-coda.ts
 * ---------------------------------------------------------------------------
 * Zero-dependency TypeScript client for the Hitron CODA 4x8x DOCSIS 3.1
 * cable modem/router web API (CODA-4582, CODA-4680, CODA56, ...).
 *
 * API shape reverse-engineered from the modem web UI and cross-checked against
 * hairyhenderson/hitron_coda (Go). Tested surface: CODA-4680 fw 7.1.1.2.2b9.
 *
 * Auth flow:
 *   POST /Users/Login   body: model=<json {"username","password"}>  (form-enc)
 *                       -> sets a session cookie, stored in the jar
 *   GET  /Users/CSRF    -> CSRF token, required for state-changing POSTs
 *   POST /Users/Logout
 *
 * Requires Node 18+ (global fetch / undici). No npm deps.
 * ---------------------------------------------------------------------------
 */

// ---------------------------------------------------------------------------
// Minimal cookie jar (the modem hands back one session cookie; native fetch
// does not persist cookies, so we track Set-Cookie ourselves).
// ---------------------------------------------------------------------------

class CookieJar {
  private cookies = new Map<string, string>();

  /** Parse a response's Set-Cookie header(s) and store name=value pairs. */
  store(headers: Headers): void {
    // undici exposes getSetCookie(); fall back to the single-header form.
    const raw: string[] =
      typeof (headers as any).getSetCookie === "function"
        ? (headers as any).getSetCookie()
        : headers.get("set-cookie")
          ? [headers.get("set-cookie") as string]
          : [];
    for (const line of raw) {
      const pair = line.split(";", 1)[0];
      const eq = pair.indexOf("=");
      if (eq > 0) {
        this.cookies.set(pair.slice(0, eq).trim(), pair.slice(eq + 1).trim());
      }
    }
  }

  header(): string | undefined {
    if (this.cookies.size === 0) return undefined;
    return [...this.cookies].map(([k, v]) => `${k}=${v}`).join("; ");
  }

  clear(): void {
    this.cookies.clear();
  }
}

// ---------------------------------------------------------------------------
// Options & errors
// ---------------------------------------------------------------------------

export interface HitronClientOptions {
  /** Modem IP or host. Default 192.168.0.1 */
  host?: string;
  /** "http" (default) or "https". Some units redirect to https. */
  scheme?: "http" | "https";
  /** Default admin username is "cusadmin" on most ISP builds. */
  username: string;
  password: string;
  /** Per-request timeout in ms. Default 10000. */
  timeoutMs?: number;
  /**
   * Accept the modem's self-signed TLS cert (https only). Lazily wires an
   * undici Agent with rejectUnauthorized:false. Never use against anything
   * but a device on your own LAN.
   */
  insecureTLS?: boolean;
  /** Inject a fetch impl (tests). Defaults to global fetch. */
  fetchImpl?: typeof fetch;
}

export class HitronError extends Error {
  constructor(
    message: string,
    readonly status?: number,
    readonly body?: string,
  ) {
    super(message);
    this.name = "HitronError";
  }
}

export class NotLoggedInError extends HitronError {
  constructor() {
    super("Not logged in (session expired or never established)");
    this.name = "NotLoggedInError";
  }
}

// ---------------------------------------------------------------------------
// Typed responses for the well-known endpoints. Fields the firmware emits are
// strings even when numeric; parse at the call site if you need numbers.
// Endpoints without a dedicated interface fall through to Record<string,...>.
// ---------------------------------------------------------------------------

export interface CmSysInfo {
  hwVersion?: string;
  swVersion?: string;
  serialNumber?: string;
  rfMac?: string;
  systemUptime?: string;
  [k: string]: unknown;
}

export interface DsChannel {
  portId?: string;
  frequency?: string;
  modulation?: string;
  signalStrength?: string; // dBmV
  snr?: string; // dB
  [k: string]: unknown;
}

export interface UsChannel {
  portId?: string;
  frequency?: string;
  bandwidth?: string;
  scdmaMode?: string;
  signalStrength?: string; // dBmV
  [k: string]: unknown;
}

export interface Host {
  hostName?: string;
  ipAddress?: string;
  macAddress?: string;
  interface?: string;
  online?: string;
  [k: string]: unknown;
}

// ---------------------------------------------------------------------------
// The known GET endpoints, grouped. Source: apilist.yaml (31 endpoints).
// ---------------------------------------------------------------------------

export const ENDPOINTS = {
  cm: {
    docsisProvision: "/CM/DocsisProvision",
    dsInfo: "/CM/DsInfo",
    dsOfdm: "/CM/DsOfdm",
    log: "/CM/Log",
    sysInfo: "/CM/SysInfo",
    usInfo: "/CM/UsInfo",
    usOfdm: "/CM/UsOfdm",
    version: "/CM/Version",
  },
  router: {
    capability: "/Router/Capability",
    dmz: "/Router/DMZ",
    location: "/Router/Location",
    portForwardStatus: "/Router/PortForward/Status",
    portForwardAll: "/Router/PortForward/all",
    portTriggerStatus: "/Router/PortTrigger/Status",
    portTriggerAll: "/Router/PortTrigger/all",
    sysInfo: "/Router/SysInfo",
    tr069: "/Router/TR069",
  },
  net: {
    ddns: "/DDNS",
    dns: "/DNS",
    hosts: "/Hosts",
    time: "/Time",
  },
  wifi: {
    accessControl: "/WiFi/AccessControl",
    accessControlStatus: "/WiFi/AccessControl/Status",
    client: "/WiFi/Client",
    guestSsid: "/WiFi/GuestSSID",
    radios: "/WiFi/Radios",
    radiosAdvanced: "/WiFi/Radios/Advanced",
    radiosSurvey: "/WiFi/Radios/Survey",
    ssids: "/WiFi/SSIDs",
    wps: "/WiFi/WPS",
  },
  users: {
    csrf: "/Users/CSRF",
  },
} as const;

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

export class HitronCodaClient {
  private readonly base: string;
  private readonly username: string;
  private readonly password: string;
  private readonly timeoutMs: number;
  private readonly jar = new CookieJar();
  private readonly fetchImpl: typeof fetch;
  private dispatcher?: unknown; // undici Agent for insecure TLS
  private loggedIn = false;

  constructor(opts: HitronClientOptions) {
    const host = opts.host ?? "192.168.0.1";
    const scheme = opts.scheme ?? "http";
    this.base = `${scheme}://${host}`;
    this.username = opts.username;
    this.password = opts.password;
    this.timeoutMs = opts.timeoutMs ?? 10_000;
    this.fetchImpl = opts.fetchImpl ?? fetch;

    if (opts.insecureTLS && scheme === "https") {
      // Lazy require so the module still loads where undici isn't resolvable.
      try {
        // eslint-disable-next-line @typescript-eslint/no-var-requires
        const { Agent } = require("undici");
        this.dispatcher = new Agent({ connect: { rejectUnauthorized: false } });
      } catch {
        throw new HitronError(
          "insecureTLS requires the 'undici' Agent (Node 18+ bundles it; ensure it is resolvable)",
        );
      }
    }
  }

  // --- low-level request ---------------------------------------------------

  private async request(
    method: "GET" | "POST",
    path: string,
    opts: { body?: string; contentType?: string; csrf?: string } = {},
  ): Promise<Response> {
    const headers: Record<string, string> = {};
    const cookie = this.jar.header();
    if (cookie) headers["Cookie"] = cookie;
    if (opts.contentType) headers["Content-Type"] = opts.contentType;
    if (opts.csrf) headers["X-Csrf-Token"] = opts.csrf;

    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), this.timeoutMs);
    try {
      const res = await this.fetchImpl(this.base + path, {
        method,
        headers,
        body: opts.body,
        redirect: "manual", // a 302 here means "not logged in"
        signal: ctrl.signal,
        ...(this.dispatcher ? { dispatcher: this.dispatcher } : {}),
      } as RequestInit);
      this.jar.store(res.headers);
      return res;
    } finally {
      clearTimeout(timer);
    }
  }

  // --- auth ----------------------------------------------------------------

  /** Establish a session. Throws HitronError on bad credentials. */
  async login(): Promise<void> {
    const model = JSON.stringify({
      username: this.username,
      password: this.password,
    });
    const res = await this.request("POST", "/Users/Login", {
      body: "model=" + encodeURIComponent(model),
      contentType: "application/x-www-form-urlencoded",
    });
    if (res.status !== 200) {
      const body = await safeText(res);
      throw new HitronError(`Login failed (HTTP ${res.status})`, res.status, body);
    }
    this.loggedIn = true;
  }

  /** Fetch a fresh CSRF token for a state-changing request. */
  async csrf(): Promise<string> {
    const data = await this.getJSON<{ Csrf_token?: string; token?: string }>(
      ENDPOINTS.users.csrf,
    );
    const token = data.Csrf_token ?? data.token;
    if (!token) throw new HitronError("CSRF token not present in /Users/CSRF response");
    return token;
  }

  async logout(): Promise<void> {
    try {
      await this.request("POST", "/Users/Logout");
    } finally {
      this.jar.clear();
      this.loggedIn = false;
    }
  }

  // --- generic typed GET ---------------------------------------------------

  /**
   * GET an endpoint and parse JSON. On a 302/redirect (session gone), will
   * transparently re-login once and retry.
   */
  async getJSON<T = unknown>(path: string): Promise<T> {
    let res = await this.request("GET", path);
    if (isNotLoggedIn(res)) {
      if (!this.loggedIn) throw new NotLoggedInError();
      await this.login();
      res = await this.request("GET", path);
      if (isNotLoggedIn(res)) throw new NotLoggedInError();
    }
    if (res.status !== 200) {
      throw new HitronError(`GET ${path} -> HTTP ${res.status}`, res.status, await safeText(res));
    }
    return parseJSON<T>(await res.text(), path);
  }

  // --- convenience typed accessors ----------------------------------------

  cmSysInfo() {
    return this.getJSON<CmSysInfo>(ENDPOINTS.cm.sysInfo);
  }
  cmVersion() {
    return this.getJSON<Record<string, string>>(ENDPOINTS.cm.version);
  }
  downstream() {
    return this.getJSON<DsChannel[]>(ENDPOINTS.cm.dsInfo);
  }
  downstreamOfdm() {
    return this.getJSON<DsChannel[]>(ENDPOINTS.cm.dsOfdm);
  }
  upstream() {
    return this.getJSON<UsChannel[]>(ENDPOINTS.cm.usInfo);
  }
  upstreamOfdm() {
    return this.getJSON<UsChannel[]>(ENDPOINTS.cm.usOfdm);
  }
  docsisProvision() {
    return this.getJSON<Record<string, unknown>>(ENDPOINTS.cm.docsisProvision);
  }
  cmLog() {
    return this.getJSON<Array<Record<string, string>>>(ENDPOINTS.cm.log);
  }
  routerSysInfo() {
    return this.getJSON<Record<string, unknown>>(ENDPOINTS.router.sysInfo);
  }
  hosts() {
    return this.getJSON<Host[]>(ENDPOINTS.net.hosts);
  }
  wifiSsids() {
    return this.getJSON<Array<Record<string, unknown>>>(ENDPOINTS.wifi.ssids);
  }
  wifiClients() {
    return this.getJSON<Array<Record<string, unknown>>>(ENDPOINTS.wifi.client);
  }

  /** Escape hatch: GET any endpoint path (see ENDPOINTS) not wrapped above. */
  raw<T = unknown>(path: string) {
    return this.getJSON<T>(path);
  }

  /** Run `fn` inside a login/logout bracket. */
  static async withSession<T>(
    opts: HitronClientOptions,
    fn: (c: HitronCodaClient) => Promise<T>,
  ): Promise<T> {
    const c = new HitronCodaClient(opts);
    await c.login();
    try {
      return await fn(c);
    } finally {
      await c.logout().catch(() => void 0);
    }
  }
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

function isNotLoggedIn(res: Response): boolean {
  // The CODA firmware answers an unauthenticated data request with a redirect
  // to the login page rather than a 401.
  return res.status === 302 || res.status === 301 || res.status === 307;
}

async function safeText(res: Response): Promise<string> {
  try {
    return (await res.text()).slice(0, 500);
  } catch {
    return "";
  }
}

function parseJSON<T>(text: string, path: string): T {
  try {
    return JSON.parse(text) as T;
  } catch {
    throw new HitronError(`GET ${path}: response was not valid JSON`, 200, text.slice(0, 500));
  }
}

// ---------------------------------------------------------------------------
// Example (run: node --loader ts-node/esm hitron-coda.ts, or compile first)
// ---------------------------------------------------------------------------

if (require.main === module) {
  (async () => {
    const result = await HitronCodaClient.withSession(
      {
        host: process.env.HITRON_HOST ?? "192.168.0.1",
        username: process.env.HITRON_USER ?? "cusadmin",
        password: process.env.HITRON_PASS ?? "",
      },
      async (c) => {
        const [sys, ds, us] = await Promise.all([
          c.cmSysInfo(),
          c.downstream(),
          c.upstream(),
        ]);
        return { sys, dsChannels: ds?.length, usChannels: us?.length };
      },
    );
    console.log(JSON.stringify(result, null, 2));
  })().catch((e) => {
    console.error(e);
    process.exit(1);
  });
}
