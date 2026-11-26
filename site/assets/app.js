/* hitron docs — plain ES2020, no bundler. */
'use strict';

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

const state = {
  spec: null,
  ops: [],
  selected: null,
  charts: {},
  history: {},
  requestLog: [],
  pollTimer: null,
  specVersion: '',
};

const METHODS = ['get', 'post', 'put', 'delete', 'patch'];

/* ------------------------------------------------------------------ */
/* small helpers                                                       */
/* ------------------------------------------------------------------ */

function escapeHtml(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

function toast(message) {
  const el = $('#toast');
  if (!el) return;
  el.textContent = message;
  el.classList.add('show');
  clearTimeout(toast._timer);
  toast._timer = setTimeout(() => el.classList.remove('show'), 2200);
}

function resolveRef(ref) {
  if (!ref || !ref.startsWith('#/')) return null;
  return ref.slice(2).split('/').reduce((node, part) => {
    if (!node) return null;
    const key = decodeURIComponent(part.replace(/~1/g, '/').replace(/~0/g, '~'));
    return node[key];
  }, state.spec);
}

function deref(node) {
  let current = node;
  let guard = 0;
  while (current && current.$ref && guard < 20) {
    current = resolveRef(current.$ref);
    guard += 1;
  }
  return current;
}

function paramsOf(op, pathItem) {
  const merged = [...(pathItem.parameters || []), ...(op.parameters || [])];
  const seen = new Map();
  for (const raw of merged) {
    const param = deref(raw);
    if (!param || !param.name) continue;
    seen.set(`${param.in}:${param.name}`, param);
  }
  return Array.from(seen.values());
}

function schemaType(schema) {
  const s = deref(schema);
  if (!s) return 'any';
  if (s.type) return s.type;
  if (s.oneOf) return 'oneOf';
  if (s.allOf) return 'allOf';
  if (s.properties) return 'object';
  if (s.items) return `array<${schemaType(s.items)}>`;
  return 'any';
}

function titleCase(text) {
  return String(text || '').replace(/[-_]/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
}

/* ------------------------------------------------------------------ */
/* markdown-lite for operation descriptions                            */
/* ------------------------------------------------------------------ */

function renderMarkdown(src) {
  if (!src) return '';
  const lines = String(src).replace(/\r\n/g, '\n').split('\n');
  const out = [];
  let paragraph = [];
  let list = null;
  let code = null;
  let table = null;

  const inline = (text) => escapeHtml(text)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/(^|[\s(])\*([^*\n]+)\*/g, '$1<em>$2</em>')
    .replace(/\[([^\]]+)\]\((https?:[^)]+)\)/g, '<a href="$2" rel="noopener">$1</a>');

  const flushParagraph = () => {
    if (paragraph.length) {
      out.push(`<p>${inline(paragraph.join(' '))}</p>`);
      paragraph = [];
    }
  };
  const flushList = () => {
    if (list) {
      out.push(`<${list.tag}>${list.items.map((i) => `<li>${inline(i)}</li>`).join('')}</${list.tag}>`);
      list = null;
    }
  };
  const flushTable = () => {
    if (!table) return;
    const head = table.shift();
    const body = table.filter((row) => !row.every((cell) => /^-{2,}$/.test(cell.trim())));
    out.push(
      '<div class="table-wrap"><table><thead><tr>' +
      head.map((c) => `<th>${inline(c.trim())}</th>`).join('') +
      '</tr></thead><tbody>' +
      body.map((row) => '<tr>' + row.map((c) => `<td>${inline(c.trim())}</td>`).join('') + '</tr>').join('') +
      '</tbody></table></div>'
    );
    table = null;
  };

  for (const line of lines) {
    if (code !== null) {
      if (/^\s*```/.test(line)) {
        out.push(`<pre><code>${escapeHtml(code.join('\n'))}</code></pre>`);
        code = null;
      } else {
        code.push(line);
      }
      continue;
    }
    if (/^\s*```/.test(line)) {
      flushParagraph(); flushList(); flushTable();
      code = [];
      continue;
    }
    if (/^\s*\|.*\|\s*$/.test(line)) {
      flushParagraph(); flushList();
      table = table || [];
      table.push(line.trim().replace(/^\||\|$/g, '').split('|'));
      continue;
    }
    flushTable();

    if (!line.trim()) { flushParagraph(); flushList(); continue; }

    const bullet = line.match(/^\s*[-*]\s+(.*)$/);
    if (bullet) {
      flushParagraph();
      if (!list || list.tag !== 'ul') { flushList(); list = { tag: 'ul', items: [] }; }
      list.items.push(bullet[1]);
      continue;
    }
    const ordered = line.match(/^\s*\d+\.\s+(.*)$/);
    if (ordered) {
      flushParagraph();
      if (!list || list.tag !== 'ol') { flushList(); list = { tag: 'ol', items: [] }; }
      list.items.push(ordered[1]);
      continue;
    }
    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      flushParagraph(); flushList();
      const level = Math.min(6, heading[1].length + 3);
      out.push(`<h${level}>${inline(heading[2])}</h${level}>`);
      continue;
    }
    flushList();
    paragraph.push(line.trim());
  }
  if (code !== null) out.push(`<pre><code>${escapeHtml(code.join('\n'))}</code></pre>`);
  flushParagraph(); flushList(); flushTable();
  return out.join('\n');
}

/* ------------------------------------------------------------------ */
/* spec loading and operation index                                    */
/* ------------------------------------------------------------------ */

async function loadSpec() {
  const response = await fetch('openapi.json', { cache: 'no-cache' });
  if (!response.ok) throw new Error(`openapi.json: HTTP ${response.status}`);
  state.spec = await response.json();
  state.specVersion = state.spec.info && state.spec.info.version ? state.spec.info.version : '';

  const ops = [];
  for (const [path, item] of Object.entries(state.spec.paths || {})) {
    for (const method of METHODS) {
      const op = item[method];
      if (!op || !op.operationId) continue;
      ops.push({
        method: method.toUpperCase(),
        path,
        op,
        item,
        tag: (op.tags && op.tags[0]) || 'Other',
        summary: op.summary || '',
        search: [path, method, op.operationId, op.summary, (op.tags || []).join(' '), op.description || '']
          .join(' ').toLowerCase(),
      });
    }
  }
  ops.sort((a, b) => a.tag.localeCompare(b.tag) || a.path.localeCompare(b.path) || a.method.localeCompare(b.method));
  state.ops = ops;

  const tags = Array.from(new Set(ops.map((o) => o.tag))).sort();
  const select = $('#tag-filter');
  for (const tag of tags) {
    const option = document.createElement('option');
    option.value = tag;
    option.textContent = tag;
    select.appendChild(option);
  }

  const paths = Object.keys(state.spec.paths || {}).length;
  $('#stat-paths').textContent = String(paths);
  $('#stat-ops').textContent = String(ops.length);
  const status = $('#spec-status');
  status.textContent = `OpenAPI ${state.specVersion || ''} · ${ops.length} ops`.trim();
  status.classList.remove('bad');
  status.classList.add('ok');

  renderOpList();
}

function renderOpList() {
  const query = $('#op-search').value.trim().toLowerCase();
  const tag = $('#tag-filter').value;
  const list = $('#op-list');
  list.innerHTML = '';

  const visible = state.ops.filter((o) =>
    (!tag || o.tag === tag) && (!query || o.search.includes(query))
  );

  if (!visible.length) {
    list.innerHTML = '<p class="muted small" style="padding:1rem">No operations match.</p>';
    return;
  }

  let currentTag = null;
  for (const op of visible) {
    if (op.tag !== currentTag) {
      currentTag = op.tag;
      const label = document.createElement('div');
      label.className = 'op-group-label';
      label.textContent = currentTag;
      list.appendChild(label);
    }
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'op-item';
    button.dataset.key = `${op.method} ${op.path}`;
    button.innerHTML = `<span class="method ${op.method.toLowerCase()}">${op.method}</span>` +
      `<span class="path">${escapeHtml(op.path.replace(/^\/1\/Device/, ''))}</span>`;
    button.title = op.summary;
    button.addEventListener('click', () => selectOp(op));
    list.appendChild(button);
  }
}

/* ------------------------------------------------------------------ */
/* operation detail                                                    */
/* ------------------------------------------------------------------ */

function exampleFromSchema(schema, depth = 0) {
  const s = deref(schema);
  if (!s || depth > 6) return null;
  if (s.example !== undefined) return s.example;
  if (s.default !== undefined) return s.default;
  if (s.enum && s.enum.length) return s.enum[0];
  if (s.oneOf && s.oneOf.length) return exampleFromSchema(s.oneOf[0], depth + 1);
  if (s.allOf) {
    const merged = {};
    let any = false;
    for (const part of s.allOf) {
      const value = exampleFromSchema(part, depth + 1);
      if (value && typeof value === 'object' && !Array.isArray(value)) {
        Object.assign(merged, value);
        any = true;
      }
    }
    return any ? merged : null;
  }
  if (s.type === 'object' || s.properties) {
    const obj = {};
    for (const [key, value] of Object.entries(s.properties || {})) {
      const sample = exampleFromSchema(value, depth + 1);
      if (sample !== null && sample !== undefined) obj[key] = sample;
    }
    return obj;
  }
  if (s.type === 'array') return [];
  if (s.type === 'integer' || s.type === 'number') return 0;
  if (s.type === 'boolean') return false;
  return null;
}

function bodyExample(op) {
  const body = deref(op.requestBody);
  if (!body) return null;
  const content = body.content || {};
  const preferred = content['application/json'] || content['application/x-www-form-urlencoded'];
  if (!preferred) return null;
  const schema = deref(preferred.schema);
  if (schema && schema.properties && schema.properties.model && schema.properties.model.example) {
    return schema.properties.model.example;
  }
  const example = exampleFromSchema(preferred.schema || {});
  if (example === null || example === undefined) return '';
  return JSON.stringify(example, null, 2);
}

function schemaTree(schema, depth = 0) {
  const s = deref(schema);
  if (!s || depth > 5) return '';
  if (s.allOf) {
    let merged = { type: 'object', properties: {} };
    for (const part of s.allOf) {
      const p = deref(part);
      if (p && p.properties) Object.assign(merged.properties, p.properties);
    }
    return schemaTree(merged, depth);
  }
  if (s.oneOf) {
    return '<div class="schema-tree">' + s.oneOf.map((part, i) =>
      `<div><span class="t">variant ${i + 1}</span>${schemaTree(part, depth + 1)}</div>`
    ).join('') + '</div>';
  }
  if (s.properties) {
    return '<ul>' + Object.entries(s.properties).map(([key, value]) => {
      const v = deref(value) || {};
      const type = schemaType(v);
      const note = v.description ? ` — ${escapeHtml(v.description)}` : '';
      const nested = (type === 'object' && !v.properties?.length) ? '' : '';
      return `<li><span class="k">${escapeHtml(key)}</span><span class="t">${escapeHtml(type)}${note}</span>` +
        (v.properties ? schemaTree(v, depth + 1) : '') + nested + '</li>';
    }).join('') + '</ul>';
  }
  return '';
}

function responseSchema(op) {
  const responses = op.responses || {};
  const ok = responses['200'] || responses['201'] || responses[Object.keys(responses)[0]];
  if (!ok) return null;
  const content = ok.content || {};
  const entry = content['application/json'] || Object.values(content)[0];
  if (!entry) return null;
  return entry.schema || null;
}

function selectOp(op) {
  state.selected = op;
  $$('.op-item').forEach((el) => {
    el.classList.toggle('active', el.dataset.key === `${op.method} ${op.path}`);
  });

  const params = paramsOf(op.op, op.item);
  const body = bodyExample(op.op);
  const base = $('#base-url').value.trim() || '/proxy';

  const detail = $('#op-detail');
  detail.innerHTML = `
    <div class="op-head">
      <span class="method ${op.method.toLowerCase()}">${op.method}</span>
      <h3>${escapeHtml(op.summary || op.op.operationId)}</h3>
    </div>
    <p class="op-summary">${escapeHtml(op.op.description ? String(op.op.description).split('\n')[0] : '')}</p>
    <div class="op-meta">
      <span class="pill">${escapeHtml(op.tag)}</span>
      <span class="pill muted">${escapeHtml(op.op.operationId)}</span>
      ${op.op['x-hitron-destructive'] ? '<span class="pill bad">destructive</span>' : ''}
      ${op.op['x-hitron-implemented'] === false ? '<span class="pill bad">not implemented</span>' : ''}
      ${op.op['x-hitron-shell-sink'] ? '<span class="pill bad">shell sink</span>' : ''}
    </div>
    <div class="op-route"><span class="method ${op.method.toLowerCase()}">${op.method}</span><span>${escapeHtml(base)}${escapeHtml(op.path)}</span></div>
    ${op.op.description ? `<details open><summary class="muted small">Description</summary><div class="op-desc">${renderMarkdown(op.op.description)}</div></details>` : ''}
    ${params.length ? `<h4 class="small">Parameters</h4>
      <table class="field-table"><thead><tr><th>Name</th><th>In</th><th>Value</th></tr></thead><tbody>
      ${params.map((p) => `
        <tr>
          <td>${escapeHtml(p.name)}${p.required ? ' *' : ''}</td>
          <td class="muted">${escapeHtml(p.in)}</td>
          <td><input class="field-input" data-param="${escapeHtml(p.name)}" data-in="${escapeHtml(p.in)}"
            placeholder="${escapeHtml(schemaType(p.schema))}" value="${escapeHtml(p.schema && p.schema.example !== undefined ? p.schema.example : '')}"></td>
        </tr>`).join('')}
      </tbody></table>` : ''}
    ${body !== null ? `
      <div class="body-editor">
        <label for="request-body">Request body (JSON${op.op.requestBody && deref(op.op.requestBody) ? '' : ''})</label>
        <textarea id="request-body" spellcheck="false">${escapeHtml(body)}</textarea>
        <p class="hint">Sent as the firmware's <code>model</code> form field by default. Toggle below for a raw JSON body.</p>
        <label class="inline small"><input type="checkbox" id="raw-json"> Send as raw <code>application/json</code></label>
      </div>` : ''}
    <div class="form-actions">
      <button class="btn primary" id="send-op" type="button">Send request</button>
      <button class="btn ghost" id="copy-curl" type="button">Copy as cURL</button>
      <button class="btn ghost" id="try-rf" type="button">Send and chart</button>
      <span class="muted small">Read routes answer without login on the analyzed firmware.</span>
    </div>
    <details style="margin-top:1.2rem">
      <summary class="muted small">Response schema</summary>
      <div class="schema-tree">${schemaTree(responseSchema(op.op)) || '<p class="muted small">No JSON schema.</p>'}</div>
    </details>
  `;

  $('#send-op').addEventListener('click', () => sendSelected(false));
  $('#try-rf').addEventListener('click', () => sendSelected(true));
  $('#copy-curl').addEventListener('click', copyAsCurl);
}

function buildRequest() {
  const op = state.selected;
  if (!op) return null;
  const base = $('#base-url').value.trim().replace(/\/+$/, '');
  let path = op.path;

  const pathParams = [];
  $$('#op-detail [data-param]').forEach((input) => {
    if (input.dataset.in === 'path') {
      pathParams.push([input.dataset.param, input.value]);
    }
  });
  for (const [name, value] of pathParams) {
    if (!value) { toast(`Path parameter ${name} is required`); return null; }
    path = path.replace(`{${name}}`, encodeURIComponent(value));
  }

  const query = new URLSearchParams();
  $$('#op-detail [data-param]').forEach((input) => {
    if (input.dataset.in === 'query' && input.value.trim()) {
      query.set(input.dataset.param, input.value.trim());
    }
  });
  const url = `${base}${path}${query.toString() ? `?${query}` : ''}`;

  const textarea = $('#request-body');
  let bodyInit = null;
  if (textarea && textarea.value.trim()) {
    const rawJson = $('#raw-json') && $('#raw-json').checked;
    if (rawJson) {
      bodyInit = { body: textarea.value, headers: { 'Content-Type': 'application/json' } };
    } else {
      bodyInit = {
        body: new URLSearchParams({ model: textarea.value }).toString(),
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      };
    }
  }

  return { op, url, method: op.method, ...(bodyInit || {}) };
}

async function sendSelected(alsoChart) {
  const request = buildRequest();
  if (!request) return;
  const started = performance.now();
  const statusEl = $('#resp-status');
  statusEl.textContent = 'sending…';
  statusEl.className = 'pill muted';

  try {
    const response = await fetch(request.url, {
      method: request.method,
      headers: request.headers || {},
      body: request.body,
      credentials: 'include',
    });
    const text = await response.text();
    const elapsed = performance.now() - started;

    let pretty = text;
    let parsed = null;
    try {
      parsed = JSON.parse(text);
      pretty = JSON.stringify(parsed, null, 2);
    } catch (_) { /* JSONP or HTML */ }

    $('#resp-body').textContent = pretty;
    const ok = response.ok && (!parsed || parsed.errCode === undefined || parsed.errCode === '000');
    statusEl.textContent = `${response.status} ${response.statusText || ''}`.trim();
    statusEl.className = `pill ${ok ? 'ok' : 'bad'}`;
    $('#resp-time').textContent = `${elapsed.toFixed(0)} ms`;
    $('#resp-size').textContent = `${text.length} B`;

    logRequest(request.method, request.url, response.status, elapsed);

    if (parsed) {
      ingestResponse(request.op.path, parsed);
      if (alsoChart) await fetchRfPoints();
    }
  } catch (error) {
    const elapsed = performance.now() - started;
    statusEl.textContent = 'network error';
    statusEl.className = 'pill bad';
    $('#resp-body').textContent =
      `Request failed: ${error.message}\n\n` +
      `If you opened this page from GitHub Pages, the browser blocks direct calls to the gateway ` +
      `(mixed content / CORS). Run "hitron site --device <gateway-ip>" locally and use the /proxy base URL.`;
    logRequest(request.method, request.url, 0, elapsed, error.message);
  }
}

function requestForCurl() {
  const request = buildRequest();
  if (!request) return null;
  const parts = [`curl -sS -X ${request.method} '${request.url}'`];
  for (const [key, value] of Object.entries(request.headers || {})) {
    parts.push(`  -H '${key}: ${value}'`);
  }
  if (request.body) {
    parts.push(`  --data '${String(request.body).replace(/'/g, "'\\''")}'`);
  }
  return parts.join(' \\\n');
}

function copyAsCurl() {
  const text = requestForCurl();
  if (!text) return;
  navigator.clipboard.writeText(text).then(() => toast('cURL copied'), () => toast('Clipboard blocked'));
}

/* ------------------------------------------------------------------ */
/* auth                                                                */
/* ------------------------------------------------------------------ */

async function login() {
  const base = $('#base-url').value.trim().replace(/\/+$/, '');
  const username = $('#username').value.trim();
  const password = $('#password').value;
  const pill = $('#auth-state');
  pill.textContent = 'logging in…';
  pill.className = 'pill muted';
  try {
    const body = new URLSearchParams({ model: JSON.stringify({ username, password }) }).toString();
    const response = await fetch(`${base}/1/Device/Users/Login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body,
      credentials: 'include',
    });
    const text = await response.text();
    let parsed = null;
    try { parsed = JSON.parse(text); } catch (_) { /* ignore */ }
    const ok = response.ok && (!parsed || parsed.errCode === undefined || parsed.errCode === '000');
    pill.textContent = ok ? 'session cookie set' : `login failed (${response.status})`;
    pill.className = `pill ${ok ? 'ok' : 'bad'}`;
    logRequest('POST', `${base}/1/Device/Users/Login`, response.status, 0);
  } catch (error) {
    pill.textContent = 'login blocked';
    pill.className = 'pill bad';
    toast('Login needs the local proxy (hitron site --device …)');
  }
}

/* ------------------------------------------------------------------ */
/* charts                                                              */
/* ------------------------------------------------------------------ */

const CHART_IDS = ['rf-ds-power', 'rf-ds-snr', 'rf-us-power', 'latency'];

function chartColors(count) {
  const palette = ['#2f9e8f', '#5b7fd1', '#c98a2e', '#b25b8c', '#6f9a3d', '#a8614a', '#7a6bc4', '#3d8fa8'];
  return Array.from({ length: count }, (_, i) => palette[i % palette.length]);
}

function ensureChart(id) {
  if (state.charts[id] || typeof Chart === 'undefined') return state.charts[id];
  const canvas = document.getElementById(id);
  if (!canvas) return null;
  const isLatency = id === 'latency';
  state.charts[id] = new Chart(canvas.getContext('2d'), {
    type: 'line',
    data: { labels: [], datasets: [] },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      interaction: { mode: 'index', intersect: false },
      plugins: {
        legend: { display: !isLatency, labels: { boxWidth: 10, boxHeight: 10, usePointStyle: true, font: { size: 11 } } },
        tooltip: { enabled: true },
      },
      scales: {
        x: { ticks: { maxTicksLimit: 6, font: { size: 10 } }, grid: { display: false } },
        y: { ticks: { font: { size: 10 } }, grid: { color: 'rgba(128,128,128,.15)' } },
      },
      elements: { point: { radius: 0, hitRadius: 8 }, line: { borderWidth: 2, tension: 0.25 } },
    },
  });
  return state.charts[id];
}

function pushHistory(id, timestamp, values) {
  const series = state.history[id] || (state.history[id] = { labels: [], series: {} });
  series.labels.push(timestamp);
  for (const [key, value] of Object.entries(values)) {
    if (!series.series[key]) series.series[key] = [];
    series.series[key].push(value);
  }
  const limit = 400;
  if (series.labels.length > limit) {
    series.labels.splice(0, series.labels.length - limit);
    for (const key of Object.keys(series.series)) {
      series.series[key].splice(0, Math.max(0, series.series[key].length - limit));
    }
  }
  localStorage.setItem('hitron.charts', JSON.stringify(state.history));
}

function redrawChart(id) {
  const chart = ensureChart(id);
  const series = state.history[id];
  if (!chart || !series) return;
  const keys = Object.keys(series.series);
  const colors = chartColors(keys.length);
  chart.data.labels = series.labels.map((t) => new Date(t).toLocaleTimeString());
  chart.data.datasets = keys.map((key, i) => ({
    label: key,
    data: series.series[key],
    borderColor: colors[i],
    backgroundColor: colors[i] + '22',
    fill: false,
    spanGaps: true,
  }));
  chart.update('none');
}

function loadHistory() {
  try {
    state.history = JSON.parse(localStorage.getItem('hitron.charts') || '{}') || {};
  } catch (_) {
    state.history = {};
  }
  for (const id of CHART_IDS) {
    if (state.history[id]) redrawChart(id);
  }
}

function numbersToValues(list, keyFn, field) {
  const values = {};
  for (const item of list || []) {
    const key = keyFn(item);
    const value = parseFloat(item[field]);
    if (key && Number.isFinite(value)) values[key] = value;
  }
  return values;
}

function ingestResponse(path, payload) {
  const now = Date.now();
  if (/\/CM\/DsInfo$/.test(path) && Array.isArray(payload.Freq_List)) {
    pushHistory('rf-ds-power', now, numbersToValues(payload.Freq_List, (c) => `ch ${c.channelId}`, 'signalStrength'));
    pushHistory('rf-ds-snr', now, numbersToValues(payload.Freq_List, (c) => `ch ${c.channelId}`, 'snr'));
    redrawChart('rf-ds-power');
    redrawChart('rf-ds-snr');
  }
  if (/\/CM\/UsInfo$/.test(path) && Array.isArray(payload.Freq_List)) {
    pushHistory('rf-us-power', now, numbersToValues(payload.Freq_List, (c) => `ch ${c.channelId}`, 'signalStrength'));
    redrawChart('rf-us-power');
  }
}

/* ------------------------------------------------------------------ */
/* RF polling                                                          */
/* ------------------------------------------------------------------ */

function logRequest(method, url, status, elapsed, note) {
  state.requestLog.unshift({
    time: new Date().toISOString(),
    method,
    url,
    status,
    ms: Math.round(elapsed || 0),
    note: note || '',
  });
  state.requestLog = state.requestLog.slice(0, 200);
  renderRequestLog();
}

function renderRequestLog() {
  const filter = ($('#log-filter').value || '').toLowerCase();
  const rows = state.requestLog.filter((row) =>
    !filter || `${row.method} ${row.url} ${row.status}`.toLowerCase().includes(filter)
  );
  $('#log-count').textContent = `${state.requestLog.length} entr${state.requestLog.length === 1 ? 'y' : 'ies'}`;
  $('#request-log tbody').innerHTML = rows.map((row) => `
    <tr class="${row.status >= 400 || row.status === 0 ? 'status-bad' : ''}">
      <td>${new Date(row.time).toLocaleTimeString()}</td>
      <td>${escapeHtml(row.method)}</td>
      <td title="${escapeHtml(row.url)}">${escapeHtml(row.url.replace(/^[a-z]+:\/\/[^/]+/i, ''))}</td>
      <td>${row.status || 'ERR'}</td>
      <td>${row.ms}</td>
    </tr>`).join('') || '<tr><td colspan="5" class="muted">No requests yet.</td></tr>';
}

async function fetchJson(path) {
  const base = $('#base-url').value.trim().replace(/\/+$/, '');
  const started = performance.now();
  const response = await fetch(`${base}${path}`, { credentials: 'include', cache: 'no-store' });
  const text = await response.text();
  logRequest('GET', `${base}${path}`, response.status, performance.now() - started);
  return JSON.parse(text);
}

async function fetchRfPoints() {
  const pill = $('#poll-state');
  pill.textContent = 'fetching…';
  pill.className = 'pill muted';
  const started = performance.now();
  try {
    const ds = await fetchJson('/1/Device/CM/DsInfo');
    ingestResponse('/1/Device/CM/DsInfo', ds);
    const us = await fetchJson('/1/Device/CM/UsInfo');
    ingestResponse('/1/Device/CM/UsInfo', us);
    pushHistory('latency', Date.now(), { 'DsInfo+UsInfo round trip': Math.round(performance.now() - started) });
    redrawChart('latency');
    pill.textContent = `updated ${new Date().toLocaleTimeString()}`;
    pill.className = 'pill ok';
  } catch (error) {
    pill.textContent = 'fetch failed';
    pill.className = 'pill bad';
    toast('RF fetch failed — use the local proxy');
  }
}

function togglePolling(enabled) {
  clearInterval(state.pollTimer);
  state.pollTimer = null;
  if (enabled) {
    fetchRfPoints();
    state.pollTimer = setInterval(fetchRfPoints, 10000);
  }
}

/* ------------------------------------------------------------------ */
/* device log                                                          */
/* ------------------------------------------------------------------ */

async function fetchDeviceLog() {
  const tbody = $('#device-log tbody');
  tbody.innerHTML = '<tr><td colspan="4" class="muted">Fetching…</td></tr>';
  try {
    const payload = await fetchJson('/1/Device/CM/Log');
    const entries = payload.Log_List || payload.MTA_List || payload.Logs_List || [];
    if (!entries.length) {
      tbody.innerHTML = '<tr><td colspan="4" class="muted">The device returned an empty log.</td></tr>';
      return;
    }
    tbody.innerHTML = entries.map((entry) => `
      <tr>
        <td>${escapeHtml(entry.time || '')}</td>
        <td>${escapeHtml(entry.priority || entry.type || '')}</td>
        <td>${escapeHtml(entry.type || '')}</td>
        <td>${escapeHtml(entry.event || '')}</td>
      </tr>`).join('');
  } catch (error) {
    tbody.innerHTML = `<tr><td colspan="4" class="muted">Unavailable: ${escapeHtml(error.message)}</td></tr>`;
  }
}

/* ------------------------------------------------------------------ */
/* theme, tabs, copy                                                    */
/* ------------------------------------------------------------------ */

function applyTheme(theme) {
  document.documentElement.dataset.theme = theme;
  localStorage.setItem('hitron.theme', theme);
}

function wireUi() {
  const savedTheme = localStorage.getItem('hitron.theme');
  applyTheme(savedTheme || (window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'));
  $('#theme-toggle').addEventListener('click', () => {
    applyTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark');
  });

  $('#op-search').addEventListener('input', renderOpList);
  $('#tag-filter').addEventListener('change', renderOpList);
  $('#login-btn').addEventListener('click', login);
  $('#refresh-rf').addEventListener('click', fetchRfPoints);
  $('#poll-toggle').addEventListener('change', (event) => togglePolling(event.target.checked));
  $('#fetch-device-log').addEventListener('click', fetchDeviceLog);
  $('#log-filter').addEventListener('input', renderRequestLog);
  $('#download-log').addEventListener('click', () => {
    const blob = new Blob([JSON.stringify(state.requestLog, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `hitron-request-log-${new Date().toISOString().slice(0, 10)}.json`;
    link.click();
    URL.revokeObjectURL(url);
  });
  $('#clear-log').addEventListener('click', () => {
    state.requestLog = [];
    renderRequestLog();
  });

  $$('.copy').forEach((button) => {
    button.addEventListener('click', () => {
      const target = $(button.dataset.copy);
      if (!target) return;
      navigator.clipboard.writeText(target.textContent).then(
        () => toast('Copied'),
        () => toast('Clipboard blocked')
      );
    });
  });

  $$('.tab').forEach((tab) => {
    tab.addEventListener('click', () => {
      $$('.tab').forEach((t) => t.classList.toggle('active', t === tab));
      $$('.tab-panel').forEach((panel) => {
        panel.classList.toggle('active', panel.id === `tab-${tab.dataset.tab}`);
      });
    });
  });
}

/* ------------------------------------------------------------------ */
/* boot                                                                */
/* ------------------------------------------------------------------ */

wireUi();
loadHistory();
renderRequestLog();
loadSpec().catch((error) => {
  const status = $('#spec-status');
  status.textContent = 'spec failed to load';
  status.className = 'pill bad';
  $('#op-list').innerHTML = `<p class="muted small" style="padding:1rem">${escapeHtml(error.message)}</p>`;
});

/* Keep charts in sync when Chart.js finishes loading after this script. */
window.addEventListener('load', () => {
  for (const id of CHART_IDS) if (state.history[id]) redrawChart(id);
});
