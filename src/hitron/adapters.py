"""Capability-gated endpoint adapters for the CODA6X REST surface.

One registry, four consumers:

* ``Coda60Client.get_all()`` uses ``plan()`` to poll only the endpoints a given
  model can actually answer (a CODA60 has no WiFi/voice/MoCA SKU, so it is not
  asked for them, and endpoints known to 500 on 8.7.1.0.1b4 are skipped).
* ``hitron coda60-verify`` uses the same plan, with ``include_unverified=True``,
  to re-measure every endpoint and record its current state.
* ``tools/gen_spec.py`` turns the registry into ``specs/openapi-coda60.yaml``, so
  the spec, the clients and the verifier cannot drift.
* ``specs/models.json`` decides which capability tags a SKU has.

Every entry is GET/read-only unless ``read=False``. ``plan()`` excludes
non-read entries by default, which is what keeps the verifier and the exporter
from ever touching reboot/restore/upload/manage.

Path correctness: every path below is taken verbatim from the firmware's own
``app/routes.ini`` plus the ``$args['info']`` / ``$params['ifname']`` branches
in the controller it maps to. Where a controller reads a route parameter that
the URL does not supply (e.g. ``SsidInfo::get()`` reads ``$params['ifname']``
even for ``/WiFi/SSIDs``), that shape is recorded as broken on this build
rather than guessed around.

``state`` values, measured against firmware 8.7.1.0.1b4 on 2026-11-26:

``ok``        HTTP 200 with a real payload
``php8``      HTTP 500 from a PHP 8 fatal in the controller itself (not a
              request-shaping problem - the same route fails for every caller)
``session``   HTTP 400 / errCode 001: needs the web UI's ``SESSION.uid``
``verb``      HTTP 400 / errCode 005: "GET request is not supported for this model"
``untested``  not exercised on that build
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Iterable

from .models import ModelProfile

__all__ = [
    "Endpoint",
    "ENDPOINTS",
    "DESTRUCTIVE_IDS",
    "get_endpoint",
    "plan",
    "plan_paths",
    "capability_map",
    "summary",
    "state_counts",
]

FIRMWARE_VERIFIED = "8.7.1.0.1b4"

_OK = "ok"
_PHP8 = "php8"
_SESSION = "session"
_VERB = "verb"
_UNTESTED = "untested"


@dataclass(frozen=True)
class Endpoint:
    id: str
    path: str
    summary: str = ""
    requires: tuple[str, ...] = ("always",)
    methods: tuple[str, ...] = ("GET",)
    read: bool = True
    # Measured: this write answered 200 without a session cookie on the
    # firmware in FIRMWARE_VERIFIED. Everything else that is not a read
    # presents credentials.
    anonymous: bool = False
    models: tuple[str, ...] = ()  # empty = every profile that satisfies `requires`
    state: str = _UNTESTED
    http_status: int | None = None
    note: str = ""
    group: str = "misc"
    probe_path: str | None = None
    query: dict[str, Any] = field(default_factory=dict)

    def applies_to(self, profile: ModelProfile) -> bool:
        if not profile.supports(*self.requires):
            return False
        return not self.models or profile.id in self.models

    def concrete_path(self, **substitutions: Any) -> str:
        path = self.probe_path or self.path
        try:
            return path.format(**substitutions)
        except KeyError:
            return path

    @property
    def working(self) -> bool:
        return self.state in (_OK, _UNTESTED)


def E(*args: Any, **kwargs: Any) -> Endpoint:
    return Endpoint(*args, **kwargs)


# --------------------------------------------------------------------------
# Registry. Ordered by UI area so reports read top-to-bottom like the app.
# --------------------------------------------------------------------------

ENDPOINTS: tuple[Endpoint, ...] = (
    # --- shell ------------------------------------------------------------
    E("menu.main", "/1/Device/Menu/Main", summary="Main menu tree (session-shaped)", group="shell",
      state=_OK, http_status=200),
    E("menu.sub", "/1/Device/Menu/Sub", summary="Submenu for the selected main menu", group="shell",
      state=_OK, http_status=200),
    E("users.list", "/1/Device/Users", summary="Local account list", group="shell",
      state=_PHP8, http_status=500, note="Undefined array key 'action'"),

    # --- identity ---------------------------------------------------------
    E("deviceinfo", "/deviceinfo", summary="Telemetry identity: hw_model, hardware_version, software_version, serial",
      group="identity", state=_OK, http_status=200,
      note="Root route (/@info=TelemetryAssist), not under /1/Device. The only place hw_model is populated "
           "on this build; TR-181 DeviceInfo.ModelName is null."),
    E("cm.version", "/1/Device/CM/Version", summary="CM version screen", group="identity",
      state=_OK, http_status=200, note="Unauthenticated."),
    E("cm.sysinfo", "/1/Device/CM/SysInfo", summary="CM IP/subnet/gateway, lease and provisioned config name",
      group="identity", state=_OK, http_status=200),
    E("router.sysinfo", "/1/Device/Router/SysInfo", summary="hw/sw version, serial, rfMac, uptime",
      group="identity", state=_OK, http_status=200,
      note="modelName/vendorName/deploymentName are null on 8.7.1.0.1b4."),

    # --- DOCSIS -----------------------------------------------------------
    E("cm.provision", "/1/Device/CM/DocsisProvision", summary="DOCSIS provisioning state machine",
      group="docsis", state=_OK, http_status=200),
    E("cm.ds", "/1/Device/CM/DsInfo", summary="Downstream SC-QAM channels", group="docsis",
      state=_OK, http_status=200),
    E("cm.us", "/1/Device/CM/UsInfo", summary="Upstream SC-QAM channels", group="docsis",
      state=_OK, http_status=200),
    E("cm.ds.ofdm", "/1/Device/CM/DsOfdm", summary="Downstream OFDM channels", requires=("docsis31",),
      group="docsis", state=_OK, http_status=200),
    E("cm.us.ofdm", "/1/Device/CM/UsOfdm", summary="Upstream OFDMA channels", requires=("docsis31",),
      group="docsis", state=_OK, http_status=200),
    E("cm.log", "/1/Device/CM/Log", summary="DOCSIS event log", group="docsis",
      state=_OK, http_status=200),
    E("cm.lte", "/1/Device/CM/LTEBackup", summary="LTE backup-WAN status", requires=("lte",),
      group="docsis", state=_PHP8, http_status=500, note="Creation of dynamic property LTEBackup::$LANClient"),
    E("cm.ready", "/1/Device/CM/Ready", summary="App-only CM readiness", group="docsis",
      state=_SESSION, http_status=400),
    E("cm.ds.clear", "/1/Device/CM/DsInfo", summary="Clear DOCSIS 3.0 downstream FEC counters",
      methods=("PUT",), read=False, anonymous=True, group="docsis", state=_OK, http_status=200,
      note="ModelInfo::doClearDsFec sets Docsis.ClearAllDownstreamFECCounter. Bare PUT, no "
           "body and no session; the handler ignores both. Measured on 8.7.1.0.1b4: all eight "
           "channels read 0/0 immediately after one call. This is the only counter stop the "
           "vendor's Reset FEC Counters button calls."),
    E("cm.ds.ofdm.clear", "/1/Device/CM/DsOfdm", summary="Clear OFDM downstream FEC counters",
      requires=("docsis31",), methods=("PUT",), read=False, anonymous=True, group="docsis",
      state=_OK, http_status=200,
      note="Bare PUT, no body or session. Measured on 8.7.1.0.1b4: unreliable - one call "
           "zeroes zero to two of the four live channels, which ones vary, and the body/query "
           "are ignored so there is no per-channel targeting. Repeat until the counters stop "
           "improving (Client.ResetFEC / CODA60Client.reset_fec do eight attempts)."),

    # --- WiFi (gated; entirely dead on 8.7.1.0.1b4) ------------------------
    E("wifi.root", "/1/Device/WiFi", summary="WiFi overview", requires=("wifi",), group="wifi",
      state=_PHP8, http_status=500, note="Undefined array key 'info'"),
    E("wifi.bandsteering", "/1/Device/WiFi/BandSteering", summary="Band-steering state",
      requires=("wifi",), group="wifi", state=_OK, http_status=200),
    E("wifi.guestssid", "/1/Device/WiFi/GuestSSID", summary="Guest + primary SSID summary",
      requires=("wifi",), group="wifi", state=_OK, http_status=200,
      note="Answers 200 even on a unit with no radios; all fields are empty strings."),
    E("wifi.wps", "/1/Device/WiFi/WPS", summary="WPS state per band", requires=("wifi",),
      group="wifi", state=_OK, http_status=200,
      note="Same stub caveat: 200 does not prove a radio exists."),
    E("wifi.radios", "/1/Device/WiFi/Radios", summary="All radios", requires=("wifi",),
      group="wifi", state=_PHP8, http_status=500,
      note="Fatal at class load: optional parameter $radio declared before required $f3."),
    E("wifi.radios.survey", "/1/Device/WiFi/Radios/Survey", summary="Neighbour scan / ACS input",
      requires=("wifi",), group="wifi", state=_PHP8, http_status=500),
    E("wifi.radios.atf", "/1/Device/WiFi/Radios/ATF", summary="Airtime fairness config",
      requires=("wifi",), group="wifi", state=_PHP8, http_status=500),
    E("wifi.radios.atfssid", "/1/Device/WiFi/Radios/ATFSSID", summary="Per-SSID ATF policy",
      requires=("wifi",), group="wifi", state=_PHP8, http_status=500),
    E("wifi.radios.atfstation", "/1/Device/WiFi/Radios/ATFSTATION", summary="Per-station ATF state",
      requires=("wifi",), group="wifi", state=_PHP8, http_status=500),
    E("wifi.radios.advanced", "/1/Device/WiFi/Radios/Advanced", summary="Per-radio advanced settings",
      requires=("wifi",), group="wifi", state=_PHP8, http_status=500),
    E("wifi.ssids", "/1/Device/WiFi/SSIDs", summary="All SSIDs", requires=("wifi",),
      group="wifi", state=_PHP8, http_status=500, note="Undefined array key 'info'"),
    E("wifi.ssids.one", "/1/Device/WiFi/SSIDs/{ifname}/version", summary="One SSID by interface name",
      requires=("wifi",), group="wifi", state=_PHP8, http_status=500,
      probe_path="/1/Device/WiFi/SSIDs/ath0/version",
      note="Needs both @ifname and @info; checkValidIfName() itself is broken on PHP 8."),
    E("wifi.client", "/1/Device/WiFi/Client", summary="Associated WiFi clients", requires=("wifi",),
      group="wifi", state=_PHP8, http_status=500),
    E("wifi.client.ssids", "/1/Device/WiFi/Stations/{ifname}", summary="Clients per radio",
      requires=("wifi",), group="wifi", state=_PHP8, http_status=500,
      probe_path="/1/Device/WiFi/Stations/ath0"),
    E("wifi.client.ssids.alias", "/1/Device/WiFi/Client/SSIDs", summary="Clients per radio (alias route)",
      requires=("wifi",), group="wifi", state=_PHP8, http_status=500,
      note="Same data as /WiFi/Stations/{ifname}; this shape omits the @ifname the handler reads."),
    E("wifi.accesscontrol", "/1/Device/WiFi/AccessControl", summary="MAC allow/deny list",
      requires=("wifi",), group="wifi", state=_PHP8, http_status=500),
    E("wifi.accesscontrol.macs", "/1/Device/WiFi/AccessControl/Macs", summary="Access-control entries",
      requires=("wifi",), group="wifi", state=_PHP8, http_status=500,
      note="Undefined function getInstanceIDs() (platform defines getInstanceIDs)."),
    E("wifi.accesscontrol.mode", "/1/Device/WiFi/AccessControl/Mode", summary="Allow/deny mode",
      requires=("wifi",), group="wifi", state=_OK, http_status=200),
    E("wifi.accesscontrol.status", "/1/Device/WiFi/AccessControl/Status", summary="Access-control enabled",
      requires=("wifi",), group="wifi", state=_OK, http_status=200),
    E("wifi.hotspot", "/1/Device/WiFi/Hotspot", summary="Hotspot / community WiFi", requires=("wifi",),
      group="wifi", state=_PHP8, http_status=500, note="Undefined variable $rootObjName"),
    E("wifi.rssis", "/1/Device/WiFi/RSSIs", summary="Per-client RSSI", requires=("wifi",),
      group="wifi", state=_SESSION, http_status=400),

    # --- router / LAN (router-mode SKUs only) ------------------------------
    E("router.portforward", "/1/Device/Router/PortForward", summary="Port-forward rules",
      requires=("router",), group="lan", state=_PHP8, http_status=500),
    E("router.portforward.status", "/1/Device/Router/PortForward/Status", summary="Port-forward enabled",
      requires=("router",), group="lan", state=_OK, http_status=200),
    E("router.porttrigger", "/1/Device/Router/PortTrigger", summary="Port-trigger rules",
      requires=("router",), group="lan", state=_PHP8, http_status=500),
    E("router.porttrigger.status", "/1/Device/Router/PortTrigger/Status", summary="Port-trigger enabled",
      requires=("router",), group="lan", state=_OK, http_status=200),
    E("hosts", "/1/Device/Hosts", summary="Connected LAN hosts", requires=("router",),
      group="lan", state=_PHP8, http_status=500),
    E("hosts.deffw", "/1/Device/Hosts/DefFwLevel", summary="Default firmware level per host",
      requires=("router",), group="lan", state=_SESSION, http_status=400),
    E("dhcp", "/1/Device/DHCP", summary="DHCP server config/leases", requires=("router",),
      group="lan", state=_PHP8, http_status=500),
    E("dhcp.lan", "/1/Device/DHCP/Lan", summary="LAN DHCP settings", requires=("router",),
      group="lan", state=_PHP8, http_status=500,
      note="Creation of dynamic property DHCP::$bSomeItemsReadOnly"),
    E("dhcp.reservation", "/1/Device/DHCP/Reservation", summary="Static reservations",
      requires=("router",), group="lan", state=_PHP8, http_status=500),
    E("wan", "/1/Device/WAN", summary="WAN config/status", requires=("router",),
      group="lan", state=_PHP8, http_status=500),
    E("dns", "/1/Device/DNS", summary="DNS config", requires=("router",),
      group="lan", state=_PHP8, http_status=500),
    E("ddns", "/1/Device/DDNS", summary="Dynamic DNS config", requires=("router",),
      group="lan", state=_PHP8, http_status=500),
    E("ddns.status", "/1/Device/DDNS/Status", summary="Dynamic DNS state", requires=("router",),
      group="lan", state=_OK, http_status=200),
    E("ipmapping", "/1/Device/IpMappingInfo", summary="1:1 NAT / IP mapping", requires=("router",),
      group="lan", state=_PHP8, http_status=500),
    E("ipmapping.status", "/1/Device/IpMappingInfo/Status", summary="IP mapping enabled",
      requires=("router",), group="lan", state=_OK, http_status=200),
    E("ippass", "/1/Device/IpPass", summary="IP passthrough", requires=("router",),
      group="lan", state=_PHP8, http_status=500),
    E("gre", "/1/Device/GRE", summary="GRE tunnels", requires=("router",),
      group="lan", state=_PHP8, http_status=500),
    E("ripinfo", "/1/Device/RipInfo", summary="RIP routing", requires=("router",),
      group="lan", state=_PHP8, http_status=500),
    E("staticrouteinfo", "/1/Device/StaticRouteInfo", summary="Static routes", requires=("router",),
      group="lan", state=_PHP8, http_status=500),

    # --- hardware, present regardless of router mode ----------------------
    E("ether.vlan", "/1/Device/Ether/VLAN", summary="Ethernet port VLAN state", group="lan",
      state=_OK, http_status=200,
      note="ethPort1..4 are fixed schema fields, not a port count; on the live CODA60 all four are 0 "
           "while AdvancedSwitch reports one port."),
    E("advanced.switch", "/1/Device/Advanced/AdvancedSwitch", summary="On-board Ethernet switch ports",
      group="lan", state=_OK, http_status=200),

    # --- MoCA -------------------------------------------------------------
    E("moca", "/1/Device/MoCA", summary="MoCA interface status/config", requires=("moca",),
      group="moca", state=_PHP8, http_status=500, note="Undefined array key 'info'"),

    # --- firewall (router mode) -------------------------------------------
    E("firewall.level", "/1/Device/Firewall/Level", summary="Firewall level", requires=("router",),
      group="firewall", state=_OK, http_status=200),
    E("firewall.levels", "/1/Device/Firewall/Levels", summary="Firewall level list", requires=("router",),
      group="firewall", state=_SESSION, http_status=400),
    E("firewall.logs", "/1/Device/Firewall/Logs", summary="Firewall log", requires=("router",),
      group="firewall", state=_OK, http_status=200),
    E("firewall.vpn", "/1/Device/Firewall/VPN", summary="VPN passthrough state", requires=("router",),
      group="firewall", state=_OK, http_status=200),
    E("firewall.inbound", "/1/Device/Firewall/Inbound", summary="Inbound rule set", requires=("router",),
      group="firewall", state=_PHP8, http_status=500),
    E("firewall.inbound.rules", "/1/Device/Firewall/Inbound/Rules", summary="Inbound rules",
      requires=("router",), group="firewall", state=_OK, http_status=200),
    E("firewall.inbound.status", "/1/Device/Firewall/Inbound/Status", summary="Inbound filter enabled",
      requires=("router",), group="firewall", state=_OK, http_status=200),
    E("firewall.servicefilter", "/1/Device/Firewall/ServiceFilter", summary="Service filter set",
      requires=("router",), group="firewall", state=_PHP8, http_status=500),
    E("firewall.servicefilter.rules", "/1/Device/Firewall/ServiceFilter/Rules",
      summary="Service filter rules", requires=("router",), group="firewall", state=_PHP8,
      http_status=500, note="Undefined function getInstanceIDs()."),
    E("firewall.servicefilter.status", "/1/Device/Firewall/ServiceFilter/Status",
      summary="Service filter enabled", requires=("router",), group="firewall",
      state=_OK, http_status=200),
    E("firewall.servicefilter.trust", "/1/Device/Firewall/ServiceFilter/TrustRules",
      summary="Service filter trust rules", requires=("router",), group="firewall",
      state=_PHP8, http_status=500),
    E("firewall.devicefilter", "/1/Device/Firewall/DeviceFilter", summary="Device filter set",
      requires=("router",), group="firewall", state=_PHP8, http_status=500),
    E("firewall.devicefilter.rules", "/1/Device/Firewall/DeviceFilter/Rules",
      summary="Device filter rules", requires=("router",), group="firewall",
      state=_PHP8, http_status=500),
    E("firewall.devicefilter.type", "/1/Device/Firewall/DeviceFilter/Type",
      summary="Device filter allow/deny type", requires=("router",), group="firewall",
      state=_OK, http_status=200),
    E("firewall.keywordfilter", "/1/Device/Firewall/KeywordFilter", summary="Keyword filter set",
      requires=("router",), group="firewall", state=_PHP8, http_status=500),
    E("firewall.keywordfilter.rules", "/1/Device/Firewall/KeywordFilter/Rules",
      summary="Keyword filter rules", requires=("router",), group="firewall",
      state=_PHP8, http_status=500),
    E("firewall.keywordfilter.status", "/1/Device/Firewall/KeywordFilter/Status",
      summary="Keyword filter enabled", requires=("router",), group="firewall",
      state=_OK, http_status=200),
    E("firewall.keywordfilter.trust", "/1/Device/Firewall/KeywordFilter/TrustRules",
      summary="Keyword filter trust rules", requires=("router",), group="firewall",
      state=_PHP8, http_status=500),
    E("firewall.timefilter", "/1/Device/Firewall/TimeFilter", summary="Time-based access filter",
      requires=("router",), group="firewall", state=_PHP8, http_status=500),
    E("firewall.kwdfilter.app", "/1/Device/Firewall/KwdFilter/{info}",
      summary="App-only keyword filter", requires=("router",), group="firewall", state=_UNTESTED),
    E("firewall.srvfilter.app", "/1/Device/Firewall/SrvFilter/{info}",
      summary="App-only service filter", requires=("router",), group="firewall", state=_UNTESTED),

    # --- voice ------------------------------------------------------------
    E("mta.status", "/1/Device/MTA/StatusInfo", summary="EMTA line registration + call state",
      requires=("voice",), group="voice", state=_OK, http_status=200,
      note="Answers 200 even with no SLIC; NumberOfSLIC is null and all LineNRegState are null."),
    E("mta.dhcp", "/1/Device/MTA/StatusInfo/DhcpInfo", summary="EMTA DHCP state",
      requires=("voice",), group="voice", state=_OK, http_status=200),
    E("mta.log", "/1/Device/MTA/StatusInfo/Log", summary="EMTA event log", requires=("voice",),
      group="voice", state=_OK, http_status=200),
    E("mta.quality", "/1/Device/MTA/StatusInfo/QualityMetric", summary="Voice quality metrics",
      requires=("voice",), group="voice", state=_OK, http_status=200),
    E("bbu", "/1/Device/BBU", summary="Battery backup unit", requires=("battery",),
      group="voice", state=_PHP8, http_status=500),

    # --- diagnostics ------------------------------------------------------
    E("diagnostics.root", "/1/Device/Diagnostics", summary="Diagnostics controller root",
      group="diagnostics", state=_PHP8, http_status=500,
      note="Requires an @info branch; the bare controller root reads $args['info'] and fatals."),
    E("diag.input", "/1/Device/Diagnostics/Input", summary="Stored diagnostic target", group="diagnostics",
      state=_OK, http_status=200),
    E("diag.ping", "/1/Device/Diagnostics/Ping", summary="Last ping result", group="diagnostics",
      state=_OK, http_status=200),
    E("diag.traceroute", "/1/Device/Diagnostics/TraceRoute", summary="Last traceroute result",
      group="diagnostics", state=_OK, http_status=200),

    # --- spectrum ---------------------------------------------------------
    E("spectrum", "/1/Device/Spectrum", summary="Spectrum analyzer source address",
      requires=("spectrum",), group="spectrum", state=_OK, http_status=200),
    E("spectrum.freqlist", "/1/Device/Spectrum/FreqList", summary="Analyzer frequency list",
      requires=("spectrum",), group="spectrum", state=_OK, http_status=200),
    E("spectrum.rtdata", "/1/Device/Spectrum/RTData", summary="Real-time spectrum trace",
      requires=("spectrum",), group="spectrum", state=_OK, http_status=200),
    E("spectrum.constellation", "/1/Device/Spectrum/Constellation",
      summary="Constellation points for one downstream port", requires=("spectrum",),
      group="spectrum", state=_OK, http_status=200,
      query={"portId": 24},
      note="portId is the analyzer's own id, not the DOCSIS channel id: a live CODA-60 "
           "reports points for 0 and 25..31 only. Inactive ports answer 200 with an empty "
           "array. The handler parses the query with Utils::getUrlParams(), whose unquoted "
           "$_SERVER[HTTP_HOST] is a PHP 8 undefined-constant fatal; this build answers "
           "200, so it is not running PHP 8."),
    E("spectrum.freqpower", "/1/Device/Spectrum/FreqPower", summary="Power sweep over a frequency range",
      requires=("spectrum",), group="spectrum", state=_OK, http_status=200,
      query={"centFreq": 645, "fromFreq": 637.5, "toFreq": 643.5},
      note="One 6 MHz window per call from the shared tuner, 300 points at 20 kHz spacing. "
           "The handler accepts at most one non-zero fractional digit (637.5, never "
           "637.500); a value that misses its regex silently falls back to the "
           "645/637.5/643.5 defaults. The returned window can still start up to 1.5 MHz "
           "away from fromFreq, so plot the frequencies in the payload, not the request."),

    # --- system -----------------------------------------------------------
    E("time", "/1/Device/Time", summary="NTP/time configuration", group="system",
      state=_PHP8, http_status=500),
    E("advanced.root", "/1/Device/Advanced", summary="Advanced controller root", group="system",
      state=_PHP8, http_status=500, note="Requires an @info branch."),
    E("usb.root", "/1/Device/USB", summary="USB controller root", group="system",
      state=_PHP8, http_status=500, note="Requires an @info branch."),
    E("adminremote.root", "/1/Device/AdminRemoteManagement", summary="Remote-management controller root",
      group="system", state=_PHP8, http_status=500, note="Requires an @info branch."),
    E("usb.devices", "/1/Device/USB/DeviceList", summary="Attached USB devices", requires=("usb",),
      group="system", state=_OK, http_status=200,
      note="0 entries proves an empty port, not an absent one; only the catalog can say a SKU has no USB."),
    E("adminremote.status", "/1/Device/AdminRemoteManagement/Status",
      summary="WAN-side admin access state", group="system", state=_OK, http_status=200),
    E("adminremote.aclist", "/1/Device/AdminRemoteManagement/AcList",
      summary="Remote-management allow list", group="system", state=_PHP8, http_status=500,
      note="count() on a RemoteAccess object"),
    E("adminremote.rlist", "/1/Device/AdminRemoteManagement/RList",
      summary="Remote-management rule list", group="system", state=_PHP8, http_status=500,
      note="Undefined function getInstanceIDs()."),
    E("adminremote.backup", "/1/Device/AdminRemoteManagement/Backup",
      summary="Download the running config", group="system", read=False, state=_PHP8,
      http_status=500,
      note="Saves to /tmp/basic.cfg then readfile()s it; confPhp never produces the file on this build."),
    E("health.root", "/1/Device/Health", summary="App health summary", group="system",
      state=_PHP8, http_status=500),
    E("health.settings", "/1/Device/Health/Settings", summary="App health thresholds",
      group="system", state=_SESSION, http_status=400),
    E("tempaccess.status", "/1/Device/TempAccess/Status", summary="Temporary access window",
      group="system", state=_VERB, http_status=400),
    E("addons.assia", "/1/Device/Addons/AssiaAgent", summary="Assia agent state", group="system",
      state=_OK, http_status=200),
    E("manage.root", "/1/Device/Manage", summary="Admin account screen", group="system",
      state=_OK, http_status=200,
      note="Returns the literal text 'cusadmin,false' (current admin username + a flag), not JSON, "
           "to an unauthenticated GET on 8.7.1.0.1b4."),
    E("upload.root", "/1/Device/Upload", summary="Config upload handler", group="system",
      methods=("POST",), read=False, state=_OK, http_status=200,
      note="Unauthenticated .cfg upload into /var/tmp on this build; GET renders the form, POST accepts it."),

    # --- telemetry (root routes) -----------------------------------------
    E("telemetry.rfmetrics", "/rfmetrics", summary="RF metrics bundle", group="telemetry",
      state=_OK, http_status=200),
    E("telemetry.pnm", "/pnm", summary="PNM bundle (rxmer etc.)", group="telemetry",
      state=_OK, http_status=200),
    E("telemetry.pnm.rxmer", "/pnm/rxmer", summary="Downstream RxMER per subcarrier", group="telemetry",
      state=_OK, http_status=200),
    E("telemetry.pnm.ch_est_coef", "/pnm/ch_est_coef", summary="Channel estimate coefficients",
      group="telemetry", state=_UNTESTED),
    E("telemetry.pnm.us_pre_eq", "/pnm/us_pre_eq", summary="Upstream pre-equaliser coefficients",
      group="telemetry", state=_OK, http_status=200),
    E("telemetry.pnm.mod_prof", "/pnm/mod_prof", summary="Modulation profiles", group="telemetry",
      state=_OK, http_status=200),

    # --- writes / actions (never in plan() unless include_writes=True) ----
    E("cm.reboot", "/1/Device/CM/Reboot", summary="Reboot / factory-reset flag", methods=("POST",),
      read=False, state=_UNTESTED, group="write",
      note="Body must carry all three flags: ModelInfo::doRebootAction reads reboot, "
           "factoryReset and restoreReset before choosing a branch, so a partial body is a "
           "PHP 8 fatal (HTTP 500). Measured on 8.7.1.0.1b4: {reboot:1,...} answers 200 "
           "{errCode:000} but the unit does not reboot (ICMP stays up, WAN lease and DOCSIS "
           "counters unchanged, no CM Reboot Reason log entry). {reboot:0,factoryReset:0,"
           "restoreReset:1} works: the connection drops with no reply, the unit is down ~5 s "
           "later and back ~122 s after that, with CM Reboot Reason : POWER_ON. factoryReset "
           "was not run."),
    E("adminremote.manage", "/1/Device/AdminRemoteManagement/Manage", summary="Enable/disable WAN admin",
      methods=("POST",), read=False, state=_UNTESTED, group="write"),
    E("adminremote.rlist.write", "/1/Device/AdminRemoteManagement/RList",
      summary="Add/remove remote-management rules", methods=("POST", "PUT"), read=False,
      state=_UNTESTED, group="write"),
    E("adminremote.restore", "/1/Device/AdminRemoteManagement/Restore",
      summary="Restore config from an uploaded file", methods=("POST",), read=False,
      state=_UNTESTED, group="write",
      note="Builds exec('confPhp restore '.$target) from an unvalidated filename."),
    E("manage.password", "/1/Device/Manage/Password", summary="Change admin password",
      methods=("POST",), read=False, state=_UNTESTED, group="write"),
    E("manage.username", "/1/Device/Manage/Username", summary="Change admin username",
      methods=("POST",), read=False, state=_UNTESTED, group="write"),
    E("users.login", "/1/Device/Users/Login", summary="Browser session login", methods=("POST",),
      read=False, state=_UNTESTED, group="write"),
    E("users.logout", "/1/Device/Users/Logout", summary="Browser session logout", methods=("POST",),
      read=False, state=_UNTESTED, group="write"),
    E("api.register", "/1/Device/API/Register", summary="Vendor-app JWT registration",
      methods=("POST",), read=False, state=_VERB, http_status=400, group="write",
      note="GET returns errCode 005; POST expects {appid, seckey}."),
    E("wifi.radio.write", "/1/Device/WiFi/Radios/{index}", summary="Update one radio",
      requires=("wifi",), methods=("PUT", "PATCH"), read=False, state=_UNTESTED, group="write"),
)

DESTRUCTIVE_IDS: frozenset[str] = frozenset(
    {
        "cm.reboot",
        "adminremote.manage",
        "adminremote.rlist.write",
        "adminremote.restore",
        "adminremote.backup",
        "upload.root",
        "manage.password",
        "manage.username",
        "users.login",
        "users.logout",
        "api.register",
        "wifi.radio.write",
    }
)

_BY_ID = {e.id: e for e in ENDPOINTS}


def get_endpoint(identifier: str) -> Endpoint | None:
    if identifier in _BY_ID:
        return _BY_ID[identifier]
    for endpoint in ENDPOINTS:
        if endpoint.path == identifier:
            return endpoint
    return None


def plan(
    profile: ModelProfile,
    include_writes: bool = False,
    only_groups: Iterable[str] | None = None,
    include_unverified: bool = False,
) -> list[Endpoint]:
    """Endpoints that apply to ``profile``, in registry order.

    Reads only by default, and only endpoints that are observed working (or
    not yet tested). ``include_unverified`` re-includes the ones this firmware
    is known to 500 or gate, which is what the verifier wants; the poller does
    not, because retrying a PHP fatal eight times a minute helps nobody.

    ``include_writes`` exists for documentation/codegen, not for the poller or
    verifier.
    """
    groups = set(only_groups) if only_groups else None
    return [
        e
        for e in ENDPOINTS
        if (include_writes or e.read)
        and (include_unverified or e.working)
        and (groups is None or e.group in groups)
        and e.applies_to(profile)
    ]


def plan_paths(profile: ModelProfile, **kwargs: Any) -> list[str]:
    return [e.path for e in plan(profile, **kwargs)]


def capability_map(include_writes: bool = True) -> dict[str, list[str]]:
    """``{path: [capability, ...]}`` for the OpenAPI extension block."""
    return {
        e.path: [c for c in e.requires if c != "always"]
        for e in ENDPOINTS
        if include_writes or e.read
    }


def state_counts() -> dict[str, int]:
    counts: dict[str, int] = {}
    for e in ENDPOINTS:
        if e.read:
            counts[e.state] = counts.get(e.state, 0) + 1
    return dict(sorted(counts.items()))


def summary(profile: ModelProfile) -> dict[str, Any]:
    selected = plan(profile)
    by_group: dict[str, int] = {}
    for e in selected:
        by_group[e.group] = by_group.get(e.group, 0) + 1
    return {
        "profile": profile.id,
        "capabilities": sorted(profile.capabilities),
        "endpoints": len(selected),
        "by_group": dict(sorted(by_group.items())),
        "skipped_groups": sorted({e.group for e in ENDPOINTS if e.read and not e.applies_to(profile)}),
        "skipped_known_broken": sorted(e.id for e in ENDPOINTS if e.read and e.applies_to(profile) and not e.working),
    }
