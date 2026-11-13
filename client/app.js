'use strict';
/* Pulsedeck dashboard client. Runs unchanged in a browser (served by the
   server) and inside the native shells, which expose window.PulsedeckNative
   for LAN discovery. */

const LS_KEY = 'pulsedeck.v1';
const DEFAULT_PORT = 7878;
const HISTORY = 123;
const native = window.PulsedeckNative || null;
// ?demo shows invented computers with invented readings: for trying the
// dashboard without a server, and for screenshots that reveal nothing real.
const DEMO = !native && /[?&]demo\b/.test(location.search);
const servedByServer = !native && !DEMO && location.protocol.startsWith('http');

// ---------- tiny DOM helpers ----------
const $ = (s, r = document) => r.querySelector(s);
function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'class') el.className = v;
    else if (k === 'html') el.innerHTML = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat(Infinity)) if (kid != null && kid !== false) el.append(kid);
  return el;
}
const ICONS = {
  play: '<path d="M6 4.5v15l8-7.5z"/><path d="M17 5v14M20.5 5v14"/>',
  next: '<path d="M5 5v14l10-7z"/><path d="M19 5v14"/>',
  prev: '<path d="M19 5v14L9 12z"/><path d="M5 5v14"/>',
  volup: '<path d="M4 9.5v5h3.5L12 18V6L7.5 9.5z"/><path d="M15.5 9a4.5 4.5 0 0 1 0 6M18 6.5a8 8 0 0 1 0 11"/>',
  voldown: '<path d="M4 9.5v5h3.5L12 18V6L7.5 9.5z"/><path d="M15.5 9a4.5 4.5 0 0 1 0 6"/>',
  mute: '<path d="M4 9.5v5h3.5L12 18V6L7.5 9.5z"/><path d="M16 9.5l5 5M21 9.5l-5 5"/>',
  lock: '<rect x="5" y="11" width="14" height="9" rx="2.5"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>',
  link: '<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3A4 4 0 0 0 11 18.700l1-1"/>',
  terminal: '<rect x="3" y="4.5" width="18" height="15" rx="3"/><path d="M7.500 10l3 2.500-3 2.500M13 15h3.500"/>',
  bolt: '<path d="M13 3L5 13.500h6L10.500 21 19 10.500h-6z"/>',
  power: '<path d="M12 3v8"/><path d="M6.500 6.500a8 8 0 1 0 11 0"/>',
  edit: '<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="M13.500 6.500l4 4"/>',
  settings: '<path d="M4 7h9M17 7h3M4 17h3M11 17h9"/><circle cx="15" cy="7" r="2"/><circle cx="9" cy="17" r="2"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  full: '<path d="M4 9V5h4M20 9V5h-4M4 15v4h4M20 15v4h-4"/>',
  trash: '<path d="M5 7h14M10 7V4.500h4V7M7 7l1 12.500h8L17 7"/>',
  x: '<path d="M6 6l12 12M18 6L6 18"/>',
  check: '<path d="M5 12.500l4.500 4.500L19 7.500"/>',
  copy: '<rect x="8" y="8" width="11" height="11" rx="2.500"/><path d="M5 15V6.500A1.500 1.500 0 0 1 6.500 5H15"/>',
};
const icon = (name) => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">${ICONS[name] || ICONS.bolt}</svg>`;
const uid = () => Math.random().toString(36).slice(2, 9);

// ---------- persisted state ----------
function defaults() {
  return {
    v: 1,
    theme: { mode: 'dark', accent: '#7c5cff', card: 'glass', radius: 20, gap: 12, font: 'sans', bg: 'aurora', bgImage: '', fit: true, fx: 'auto' },
    servers: servedByServer ? [{ id: 'local', url: '', token: '' }] : [],
    wake: false,
    // Every dashboard belongs to one computer; `server` is the computer on
    // screen and `sel` remembers which of its dashboards was last open.
    profiles: [],
    server: null,
    sel: {},
  };
}
function load() {
  try {
    const s = JSON.parse(localStorage.getItem(LS_KEY));
    if (s && s.v === 1) {
      const d = defaults();
      s.theme = { ...d.theme, ...s.theme };
      if (servedByServer && !s.servers.some((x) => x.id === 'local')) s.servers.unshift(d.servers[0]);
      // Older saves had one flat list of dashboards: attach each to the
      // computer its widgets point at.
      s.sel ||= {};
      for (const p of s.profiles) {
        if (p.server) continue;
        const w = (p.widgets || []).find((x) => x.server || x.sensor || x.action);
        const sid = w && (w.server || (w.sensor || w.action).split(':')[0]);
        p.server = s.servers.some((x) => x.id === sid) ? sid : s.servers[0]?.id;
      }
      if (s.active != null) {
        const ap = s.profiles[s.active];
        if (ap) { s.sel[ap.server] = ap.id; s.server = ap.server; }
        delete s.active;
      }
      return s;
    }
  } catch { /* storage unavailable or corrupt: start fresh */ }
  return defaults();
}
let state = DEMO ? { ...defaults(), servers: [{ id: 'demo1', url: '192.168.1.20:7878', token: 'demo' }, { id: 'demo2', url: '192.168.1.31:7878', token: 'demo' }] } : load();
function save() {
  if (DEMO) return; // nothing invented is ever stored
  try { localStorage.setItem(LS_KEY, JSON.stringify(state)); } catch { toast('Could not save (storage full?)'); }
}
const activeServer = () => (state.servers.some((s) => s.id === state.server) ? state.server : state.servers[0]?.id);
// A computer entry is an address; `machine` is the installation answering
// there (a dual-boot PC answers as two). Dashboards belong to the pair, so
// Linux and Windows on the same box each keep their own.
const machineOf = (sid) => conns.get(sid)?.machine || state.servers.find((s) => s.id === sid)?.machine || '';
const serverProfiles = (sid = activeServer()) => { const m = machineOf(sid); return state.profiles.filter((p) => p.server === sid && (p.machine || '') === m); };
const profile = () => { const list = serverProfiles(); return list.find((p) => p.id === state.sel[activeServer()]) || list[0]; };

// A pairing link looks like http://host:7878/#token=abc. The fragment never
// leaves the browser; store it and clean the address bar.
function takeToken() {
  const m = location.hash.match(/token=([a-z0-9]+)/i);
  if (!m) return;
  const local = state.servers.find((s) => s.id === 'local');
  if (local) { local.token = m[1]; save(); }
  history.replaceState(null, '', location.pathname + location.search);
}
takeToken();
addEventListener('hashchange', takeToken);

// ---------- connections ----------
const conns = new Map();

function wsUrl(server) {
  if (!server.url) return `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws`;
  let u = server.url.trim().replace(/^https?:\/\//, '').replace(/^wss?:\/\//, '').replace(/\/.*$/, '');
  if (!/:\d+$/.test(u)) u += ':' + DEFAULT_PORT;
  return `ws://${u}/ws`;
}

class Conn {
  constructor(server) {
    this.server = server;
    this.status = 'connecting';
    this.name = server.name || server.url || 'This computer';
    this.sensors = new Map();
    this.actions = [];
    this.values = {};
    this.hist = new Map();
    this.pending = new Map();
    this.n = 0;
    this.retry = 1000;
    this.dead = false;
    this.connect();
  }
  connect() {
    if (this.dead) return;
    clearTimeout(this.timer); // never let a pending retry open a second socket
    this.setStatus('connecting');
    let ws;
    try { ws = new WebSocket(wsUrl(this.server)); } catch { this.setStatus('closed'); return; }
    this.ws = ws;
    ws.onmessage = (e) => { let m; try { m = JSON.parse(e.data); } catch { return; } this.onMsg(m); };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.setStatus('closed');
      for (const [, cb] of this.pending) cb(false, 'Disconnected');
      this.pending.clear();
      if (!this.dead) this.timer = setTimeout(() => this.connect(), this.retry);
      this.retry = Math.min(this.retry * 1.6, 6000);
    };
    ws.onerror = () => ws.close();
  }
  setStatus(s) { this.status = s; renderDock(); scheduleUpdate(); tellNative(); refreshComputers(); if (s === 'closed' && this.server.id === activeServer()) update(); }
  onMsg(m) {
    if (m.type === 'hello') {
      this.retry = 1000;
      this.name = m.server.name;
      this.os = m.server.os;
      this.machine = m.server.id || '';
      if (this.server.machine !== this.machine) { this.server.machine = this.machine; save(); }
      this.interval = m.interval_ms || 1000;
      if (this.server.name !== this.name) { this.server.name = this.name; save(); }
      this.sensors = new Map(m.sensors.map((s) => [s.id, s]));
      this.actions = m.actions || [];
      this.hist.clear();
      for (const [id, arr] of Object.entries(m.history?.v || {})) this.hist.set(id, arr.filter((x) => x != null).slice(-HISTORY));
      this.setStatus('open');
      // Dashboards saved before installations had identities: hand them to
      // this one only if they were evidently built for it (its sensors exist).
      for (const p of state.profiles) {
        if (p.server !== this.server.id || p.machine || !this.machine) continue;
        const refs = (p.widgets || []).map((w) => splitRef(w.sensor)[1]).filter(Boolean);
        const known = refs.filter((id) => this.sensors.has(id)).length;
        if (!refs.length || known / refs.length >= 0.9) p.machine = this.machine;
      }
      if (!serverProfiles(this.server.id).length) { state.profiles.push(defaultProfile(this)); save(); }
      render();
    } else if (m.type === 'data') {
      Object.assign(this.values, m.v);
      for (const [id, val] of Object.entries(m.v)) {
        if (typeof val !== 'number') continue;
        let a = this.hist.get(id);
        if (!a) this.hist.set(id, (a = []));
        a.push(val);
        if (a.length > HISTORY) a.shift();
      }
      scheduleUpdate();
    } else if (m.type === 'action_result') {
      const cb = this.pending.get(m.n);
      this.pending.delete(m.n);
      if (cb) cb(m.ok, m.error);
    }
  }
  action(id, cb) {
    if (this.status !== 'open') return cb(false, 'Not connected');
    const n = ++this.n;
    this.pending.set(n, cb);
    this.ws.send(JSON.stringify({ type: 'action', id, n, token: this.server.token || '' }));
  }
  close() { this.dead = true; clearTimeout(this.timer); try { this.ws.close(); } catch { /* already closed */ } }
}

// Keeps the native shell informed: which computers exist (so it can watch for
// them while the screen is off) and whether one is connected right now.
let lastWake, lastConnected;
function tellNative() {
  if (!native?.setWake) return;
  const hosts = JSON.stringify(state.servers.map((s) => wsUrl(s).replace(/^ws:\/\//, '').replace(/\/ws$/, '')));
  const wakeKey = state.wake + hosts;
  if (wakeKey !== lastWake) { lastWake = wakeKey; native.setWake(!!state.wake, hosts); }
  const connected = [...conns.values()].some((c) => c.status === 'open');
  if (connected !== lastConnected) { lastConnected = connected; native.setConnected(connected); }
}

// A pretend server: same messages a real one sends, generated in the page.
class DemoConn extends Conn {
  connect() {
    const mac = this.server.id === 'demo2', cores = mac ? 10 : 16;
    const S = (id, label, group, unit, min = 0, max = 0) => ({ id, label, group, unit, kind: 'value', min, max });
    const sensors = [
      S('cpu.total', 'CPU', 'CPU', '%', 0, 100), S('cpu.freq', 'CPU clock (average)', 'CPU clock', 'MHz', 0, mac ? 4400 : 5000), S('cpu.power', 'CPU power', 'Power', 'W'),
      S('mem.used', 'Memory used', 'Memory', 'B', 0, (mac ? 24 : 32) * 2 ** 30), S('mem.used_pct', 'Memory', 'Memory', '%', 0, 100), S('mem.swap_pct', 'Swap', 'Memory', '%', 0, 100),
      S('net.rx', 'Download', 'Network', 'B/s'), S('net.tx', 'Upload', 'Network', 'B/s'),
      S('temp.cpu', 'CPU temperature', 'Temperature', '°C', 20, 110), S('temp.gpu', 'GPU temperature', 'Temperature', '°C', 20, 110), S('temp.ssd', 'Storage temperature', 'Temperature', '°C', 20, 110),
      S('fan.0', 'CPU fan', 'Fans', 'rpm', 0, 2200), S('fan.1', 'Case fan', 'Fans', 'rpm', 0, 1600),
      S('gpu.0.util', 'GPU usage', 'GPU', '%', 0, 100), S('gpu.0.mem_pct', 'GPU memory', 'GPU', '%', 0, 100), S('gpu.0.power', 'GPU power', 'GPU', 'W'),
      S('sys.uptime', 'Uptime', 'System', 's'), { id: 'proc.top', label: 'Top processes', group: 'System', unit: '', kind: 'list', min: 0, max: 0 },
    ];
    for (let i = 0; i < cores; i++) sensors.push(S('cpu.core.' + i, 'Core ' + (i + 1), 'CPU', '%', 0, 100), S('cpu.freq.' + i, (mac ? (i < 4 ? 'E-core ' + (i + 1) : 'P-core ' + (i - 3)) : 'CPU ' + (i + 1)) + ' clock', 'CPU clock', 'MHz', 0, mac ? 4400 : 5000));
    const names = mac ? ['Browser', 'Photo editor', 'Music', 'Mail', 'Terminal'] : ['Game', 'Browser', 'Video editor', 'Chat', 'Music'];
    let t = Math.random() * 100;
    const wave = (period, phase = 0) => (Math.sin((t + phase) / period) + 1) / 2;
    const frame = () => {
      t += 1;
      const load = Math.min(1, 0.12 + 0.55 * wave(23) * wave(7, 3) + 0.25 * Math.random() * wave(41, 9)), gpu = Math.min(1, 0.1 + 0.8 * wave(31, 5));
      const v = {
        'cpu.total': load * 100, 'cpu.power': (mac ? 3 : 25) + load * (mac ? 28 : 95), 'mem.used': (0.42 + 0.2 * wave(120)) * (mac ? 24 : 32) * 2 ** 30,
        'mem.swap_pct': 6 + 4 * wave(200), 'net.rx': 2e4 + 4e6 * wave(13) ** 4, 'net.tx': 8e3 + 6e5 * wave(17, 4) ** 3,
        'temp.cpu': 38 + load * 42, 'temp.gpu': 36 + gpu * 38, 'temp.ssd': 34 + 6 * wave(300),
        'fan.0': 600 + load * 1300, 'fan.1': 500 + gpu * 800, 'gpu.0.util': gpu * 100, 'gpu.0.mem_pct': 18 + gpu * 55, 'gpu.0.power': (mac ? 1 : 12) + gpu * (mac ? 14 : 190),
        'sys.uptime': 187000 + t,
        'proc.top': names.map((name, i) => ({ name, cpu: Math.max(0, load * 100 * [0.45, 0.25, 0.14, 0.09, 0.05][i] * (0.8 + 0.4 * Math.random())), mem: [2.4, 1.6, 0.9, 0.5, 0.2][i] * 2 ** 30 })),
      };
      v['mem.used_pct'] = (v['mem.used'] / sensors[3].max) * 100;
      let sum = 0;
      for (let i = 0; i < cores; i++) {
        const busy = Math.min(1, Math.max(0.01, load * (0.4 + 1.2 * wave(5 + i, i * 7)) + 0.08 * Math.random()));
        v['cpu.core.' + i] = busy * 100;
        sum += v['cpu.freq.' + i] = (mac ? (i < 4 ? 1100 + busy * 1600 : 1900 + busy * 2500) : 2200 + busy * 2700);
      }
      v['cpu.freq'] = sum / cores;
      return v;
    };
    const history = { t: [], v: {} };
    for (let i = 0; i < 120; i++) for (const [id, val] of Object.entries(frame())) if (typeof val === 'number') (history.v[id] ||= []).push(val);
    const actions = [['prev', 'Previous', 'prev'], ['playpause', 'Play / Pause', 'play'], ['next', 'Next', 'next'], ['voldown', 'Volume −', 'voldown'], ['volup', 'Volume +', 'volup'], ['mute', 'Mute', 'mute'], ['lock', 'Lock screen', 'lock']].map(([id, label, icon]) => ({ id, label, icon }));
    // Deferred like a real connection, so the caller has registered us first.
    setTimeout(() => {
      this.onMsg({ type: 'hello', server: { id: this.server.id, name: mac ? 'Studio MacBook' : 'Gaming PC', os: mac ? 'darwin' : 'windows' }, interval_ms: 1000, sensors, actions, history });
      this.timer = setInterval(() => this.onMsg({ type: 'data', v: frame() }), 1000);
    }, 0);
  }
  action(id, cb) { cb(true); }
  close() { this.dead = true; clearInterval(this.timer); }
}

function syncConns() {
  for (const [id, c] of conns) if (!state.servers.some((s) => s.id === id)) { c.close(); conns.delete(id); }
  for (const s of state.servers) if (!conns.has(s.id)) conns.set(s.id, DEMO ? new DemoConn(s) : new Conn(s));
  tellNative();
}
function reconnect(server) {
  conns.get(server.id)?.close();
  conns.delete(server.id);
  syncConns();
}
const splitRef = (ref) => { const i = (ref || '').indexOf(':'); return i < 0 ? [null, null] : [ref.slice(0, i), ref.slice(i + 1)]; };
function lookup(ref) {
  const [sid, id] = splitRef(ref);
  const conn = conns.get(sid);
  if (!conn) return { live: false, hist: [] };
  return { conn, id, meta: conn.sensors.get(id), val: conn.values[id], hist: conn.hist.get(id) || [], live: conn.status === 'open' };
}

// Native shells report servers found via mDNS. Servers are matched by name so
// a machine whose IP changed is updated in place instead of duplicated.
window.__pulsedeckFound = (name, host, port) => {
  const url = `${host}:${port}`;
  const known = state.servers.find((s) => s.name === name || s.url === url);
  if (known) {
    if (known.url === url) return;
    known.url = url; save(); reconnect(known);
  } else {
    state.servers.push({ id: uid(), url, token: '', name }); save(); syncConns();
  }
  render();
};

// ---------- formatting ----------
const BYTE_UNITS = { KB: 1, MB: 2, GB: 3, TB: 4 };
const group = (x) => (x >= 10000 ? Math.round(x).toLocaleString('en-US') : x >= 100 ? x.toFixed(0) : x.toFixed(1));
// fmt returns [text, unit]. `as` is the widget's own unit setting: for data
// sizes it pins the scale ("MB" shows megabytes instead of auto-scaling),
// for anything else it simply replaces the label.
function fmt(val, unit, dec, as) {
  if (val == null || Number.isNaN(val)) return ['–', ''];
  if (typeof val !== 'number') return [String(val), ''];
  const fixed = dec != null && dec !== '' ? +dec : null;
  if (unit === 'B' || unit === 'B/s') {
    const per = unit === 'B/s' ? '/s' : '';
    const pin = BYTE_UNITS[(as || '').toUpperCase()];
    if (pin) { const x = val / 1024 ** pin; return [fixed != null ? x.toFixed(fixed) : group(x), as.toUpperCase() + per]; }
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0, x = val;
    while (x >= 1000 && i < 4) { x /= 1024; i++; }
    return [fixed != null ? x.toFixed(fixed) : x >= 100 || i === 0 ? x.toFixed(0) : x.toFixed(1), u[i] + per];
  }
  if (unit === 's') {
    const d = Math.floor(val / 86400), hr = Math.floor((val % 86400) / 3600), mn = Math.floor((val % 3600) / 60);
    return [d ? `${d}d ${hr}h` : hr ? `${hr}h ${mn}m` : `${mn}m`, ''];
  }
  if (unit === 'MHz' && !as) return val >= 1000 ? [(val / 1000).toFixed(fixed ?? 2), 'GHz'] : [val.toFixed(fixed ?? 0), 'MHz'];
  const digits = fixed != null ? fixed : unit === '%' || unit === '°C' || Math.abs(val) >= 100 || Number.isInteger(val) ? 0 : 1;
  return [val.toFixed(digits), as || (unit === '°C' ? '°' : unit)];
}
const num = (x) => (x === '' || x == null || Number.isNaN(+x) ? null : +x);
function range(w, meta, hist) {
  const min = num(w.min) ?? meta?.min ?? 0;
  let max = num(w.max) ?? (meta?.max || 0);
  if (!max) {
    const peak = Math.max(1, ...hist);
    const mag = Math.pow(10, Math.floor(Math.log10(peak)));
    max = Math.ceil((peak * 1.15) / mag) * mag;
  }
  return [min, max > min ? max : min + 1];
}
function tone(w, val) {
  if (typeof val !== 'number') return '';
  const crit = num(w.crit), warn = num(w.warn);
  if (crit != null && val >= crit) return 'crit';
  if (warn != null && val >= warn) return 'warn';
  return '';
}
function setVal(el, w, s) {
  const [text, unit] = fmt(s.val, s.meta?.unit, w.decimals, w.unit);
  const v = $('.val', el), u = $('.unit', el);
  if (v.textContent !== text) {
    v.textContent = text;
    // Lets CSS shrink long readings ("20,696") so they still fit the widget.
    if (el.valLen !== text.length) { el.valLen = text.length; el.style.setProperty('--len', text.length); el.classList.toggle('long', text.length >= 4); }
  }
  if (u && u.textContent !== unit) u.textContent = unit;
}

// ---------- cheap drawing primitives ----------
// Everything that changes every tick is expressed as a CSS transform on an
// element that is painted once, so the browser only re-composites on the GPU.
// (Re-rasterising vector shapes each second is what made low-end tablets crawl.)
const fxMode = () => document.documentElement.dataset.fx;
function setT(node, transform) {
  if (node.pdT !== transform) { node.pdT = transform; node.style.transform = transform; }
}
function fraction(w, s) {
  if (typeof s.val !== 'number') return 0;
  const [min, max] = range(w, s.meta, s.hist);
  return Math.min(1, Math.max(0, (s.val - min) / (max - min)));
}
const TONE_COLORS = { warn: '#f5a524', crit: '#f43f5e' };
const colorOf = (w, el) => TONE_COLORS[el.dataset.tone] || w.color || state.theme.accent;

// The gauge sweeps 270° starting at 7:30. Internally the arc starts at 6
// o'clock (the first 45° are clipped away by CSS): the left clip shows the
// first 180° of sweep, the right clip the rest. Each half ring rotates into
// its clip. To keep the two halves looking like one continuous arc while
// animating, they move one after the other at a constant angular speed.
function setRing(el, p, conn) {
  const th = 45 + p * 270;
  if (el.ringTh === th) return;
  const a1 = Math.min(th, 180), a2 = Math.max(0, th - 180);
  const r = el.ringParts || (el.ringParts = { r1: $('.h1 .rot', el), r2: $('.h2 .rot', el), arm: $('.arm', el) });
  const prev = el.ringTh;
  el.ringTh = th;
  if (prev == null || fxMode() === 'off') {
    r.r1.style.transition = r.r2.style.transition = r.arm.style.transition = 'none';
  } else {
    const T = Math.min(500, (conn?.interval || 1000) * 0.5);
    const d1 = Math.abs(a1 - Math.min(prev, 180)), d2 = Math.abs(a2 - Math.max(0, prev - 180)), tot = d1 + d2 || 1;
    const up = th > prev, t1 = (T * d1) / tot, t2 = (T * d2) / tot;
    r.r1.style.transition = `transform ${t1.toFixed(0)}ms linear ${(up ? 0 : t2).toFixed(0)}ms`;
    // The right half appears when its turn starts and disappears once parked.
    r.r2.style.transition = `transform ${t2.toFixed(0)}ms linear ${(up ? t1 : 0).toFixed(0)}ms, visibility ${(up ? 0 : t2).toFixed(0)}ms linear ${(up ? t1 : 0).toFixed(0)}ms`;
    r.arm.style.transition = `transform ${T.toFixed(0)}ms linear`;
  }
  r.r1.style.transform = `rotate(${a1.toFixed(2)}deg)`;
  r.r2.style.transform = `rotate(${a2.toFixed(2)}deg)`;
  r.r2.style.visibility = a2 > 0 ? 'visible' : 'hidden';
  r.arm.style.transform = `rotate(${(p * 270).toFixed(2)}deg)`;
}

// Graphs are drawn on a canvas one sample wider than the visible plot, newest
// sample just off the right edge, and then slid left by one sample over the
// update interval: the line glides instead of jumping once a second.
const SPAN = HISTORY - 3; // samples across the visible plot
function drawSpark(el, w, s) {
  const cv = el.cv || (el.cv = $('canvas', el));
  const data = s.hist;
  if (el.sparkLen === data.length && el.sparkLast === data[data.length - 1] && el.sparkSize && el.sparkColor === colorOf(w, el) && data.length < HISTORY) return;
  if (!el.sparkSize) {
    const box = cv.parentElement, dpr = window.devicePixelRatio || 1;
    const W = Math.round(box.clientWidth * dpr), H = Math.round(box.clientHeight * dpr);
    if (!W || !H) return;
    const step = W / SPAN;
    el.sparkSize = { W, H, dpr, step, cw: W + Math.ceil(step) };
    cv.width = el.sparkSize.cw; cv.height = H;
    cv.style.width = el.sparkSize.cw / dpr + 'px'; cv.style.height = H / dpr + 'px';
  }
  const { H, dpr, step, cw } = el.sparkSize;
  const ctx = el.ctx || (el.ctx = cv.getContext('2d'));
  const color = colorOf(w, el);
  el.sparkLen = data.length; el.sparkLast = data[data.length - 1]; el.sparkColor = color;
  ctx.clearRect(0, 0, cw, H);
  const n = data.length;
  if (n >= 2) {
    const [min, max] = range(w, s.meta, data);
    const top = 4 * dpr, usable = H * 0.9 - top;
    const first = Math.max(0, n - SPAN - 2);
    // Whole-pixel x positions keep the vertical seams between area slices crisp.
    const X = (i) => Math.round(cw - (n - 1 - i) * step);
    const Y = (i) => H - 1.5 * dpr - Math.min(1, Math.max(0, (data[i] - min) / (max - min))) * usable;
    ctx.fillStyle = color;
    ctx.globalAlpha = 0.2;
    for (let i = first + 1; i < n; i++) {
      ctx.beginPath();
      ctx.moveTo(X(i - 1), H); ctx.lineTo(X(i - 1), Y(i - 1)); ctx.lineTo(X(i), Y(i)); ctx.lineTo(X(i), H);
      ctx.fill();
    }
    ctx.globalAlpha = 1;
    ctx.strokeStyle = color; ctx.lineWidth = 2 * dpr; ctx.lineJoin = 'round'; ctx.lineCap = 'round';
    ctx.beginPath();
    ctx.moveTo(X(first), Y(first));
    for (let i = first + 1; i < n; i++) ctx.lineTo(X(i), Y(i));
    ctx.stroke();
  }
  const shift = `translateX(${(-step / dpr).toFixed(2)}px)`;
  el.glide?.cancel();
  el.glide = null;
  if (fxMode() === 'on' && s.live && !document.hidden) {
    el.glide = cv.animate([{ transform: 'translateX(0)' }, { transform: shift }], { duration: s.conn?.interval || 1000, easing: 'linear', fill: 'forwards' });
  } else cv.style.transform = shift;
}

// ---------- widget types ----------
const TYPES = {
  ring: {
    name: 'Ring gauge', hint: 'Circular gauge', w: 3, h: 3, sensor: true,
    // The value arc is two pre-drawn half rings that only ever rotate, so a
    // change is a GPU transform rather than a repaint (see setRing).
    build: () => h('div', { class: 'ring', html: `<svg viewBox="0 0 120 120"><circle class="track" cx="60" cy="60" r="52" pathLength="100" stroke-dasharray="75 100" transform="rotate(135 60 60)"/></svg><div class="arc"><div class="half h1"><div class="rot"><div class="disc"></div></div></div><div class="half h2"><div class="rot"><div class="disc"></div></div></div></div><div class="cap"></div><div class="arm"><div class="cap"></div></div><div class="ring-mid"><span class="val">–</span><span class="unit"></span></div><div class="w-title"></div>` }),
    update(el, w, s) {
      setVal(el, w, s);
      setRing(el, fraction(w, s), s.conn);
    },
  },
  bar: {
    name: 'Bar gauge', hint: 'Compact linear gauge', w: 3, h: 1, sensor: true,
    build: () => [h('div', { class: 'bar-head' }, h('div', { class: 'w-title' }), h('div', {}, h('span', { class: 'val' }, '–'), h('span', { class: 'unit' }))), h('div', { class: 'bar-track' }, h('div', { class: 'bar-fill' }))],
    update(el, w, s) {
      setVal(el, w, s);
      setT($('.bar-fill', el), `translateX(${(fraction(w, s) * 100 - 100).toFixed(2)}%)`);
    },
  },
  spark: {
    name: 'Graph', hint: 'History over time', w: 6, h: 3, sensor: true,
    build: () => [h('div', { class: 'spark-head' }, h('div', { class: 'w-title' }), h('div', {}, h('span', { class: 'val' }, '–'), h('span', { class: 'unit' }))), h('div', { class: 'spark-plot' }, h('canvas'))],
    update(el, w, s) {
      setVal(el, w, s);
      drawSpark(el, w, s);
    },
  },
  stat: {
    name: 'Number', hint: 'A single big value', w: 3, h: 2, sensor: true,
    build: () => [h('div', { class: 'w-title' }), h('div', {}, h('span', { class: 'val' }, '–'), h('span', { class: 'unit' }))],
    update: (el, w, s) => setVal(el, w, s),
  },
  cores: {
    name: 'CPU cores', hint: 'Usage or clock speed per core', w: 6, h: 3, flow: [4, 2], server: true, defTitle: 'CPU cores',
    build: () => [h('div', { class: 'bar-head' }, h('div', { class: 'w-title' }), h('div', {}, h('span', { class: 'val' }), h('span', { class: 'unit' }))), h('div', { class: 'cores' })],
    update(el, w) {
      const conn = conns.get(w.server);
      if (!conn) return;
      const freq = w.metric === 'freq', prefix = freq ? 'cpu.freq.' : 'cpu.core.';
      if (!el.coreIds || el.coreIds.n !== conn.sensors.size) {
        el.coreIds = Object.assign([...conn.sensors.keys()].filter((id) => id.startsWith(prefix)), { n: conn.sensors.size });
      }
      const ids = el.coreIds, box = $('.cores', el);
      if (freq) {
        // Clock speed: one labelled row per core with its own reading.
        if (box.children.length !== ids.length) {
          box.className = 'cores corelist';
          box.style.setProperty('--cc', ids.length <= 8 ? 1 : ids.length <= 20 ? 2 : ids.length <= 48 ? 3 : 4);
          box.replaceChildren(...ids.map((id) => {
            const short = conn.sensors.get(id).label.replace(/ clock$/, '').replace(/-core /, '').replace(/^(Core|CPU) /, '');
            return h('div', { class: 'corerow' }, h('span', {}, short), h('div', { class: 'core' }, h('i')), h('b'));
          }));
        }
        const max = conn.sensors.get('cpu.freq')?.max || 1; // one scale, so E and P cores compare
        for (let i = 0; i < ids.length; i++) {
          const v = conn.values[ids[i]], row = box.children[i];
          setT(row.children[1].firstChild, `translateX(${(Math.min(100, ((v || 0) / max) * 100) - 100).toFixed(1)}%)`);
          const text = v == null ? '–' : (v / 1000).toFixed(2);
          if (row.children[2].textContent !== text) row.children[2].textContent = text;
        }
      } else {
        while (box.children.length < ids.length) box.append(h('div', { class: 'core' }, h('i')));
        for (let i = 0; i < ids.length; i++) setT(box.children[i].firstChild, `translateY(${(100 - (conn.values[ids[i]] || 0)).toFixed(1)}%)`);
      }
      const total = freq ? 'cpu.freq' : 'cpu.total';
      setVal(el, {}, { val: conn.values[total], meta: conn.sensors.get(total) });
    },
  },
  procs: {
    name: 'Top processes', hint: 'Busiest apps right now', w: 4, h: 3, flow: [4, 3], server: true, defTitle: 'Top processes',
    build: () => [h('div', { class: 'w-title' }), h('div', { class: 'procs' }, [0, 1, 2, 3, 4].map(() => h('div', { class: 'proc' }, h('b'), h('span'), h('span'), h('i'))))],
    update(el, w) {
      const rows = conns.get(w.server)?.values['proc.top'] || [];
      if (el.procRows === rows) return; // same frame object: nothing new
      el.procRows = rows;
      const top = Math.max(1, ...rows.map((r) => r.cpu));
      const text = (node, t) => { if (node.textContent !== t) node.textContent = t; };
      [...$('.procs', el).children].forEach((row, i) => {
        const r = rows[i];
        row.style.visibility = r ? '' : 'hidden';
        if (!r) return;
        text(row.children[0], r.name);
        text(row.children[1], r.cpu.toFixed(0) + '%');
        text(row.children[2], fmt(r.mem, 'B').join(' '));
        setT(row.children[3], `scaleX(${(r.cpu / top).toFixed(3)})`);
      });
    },
  },
  clock: {
    name: 'Clock', hint: 'Time and date', w: 3, h: 2,
    build: () => [h('div', { class: 'clock-host' }, h('span', { class: 'dot' }), h('span')), h('div', { class: 'val' }), h('div', { class: 'w-title' })],
    update(el, w) {
      // Which computer this dashboard belongs to, readable from across the room.
      const host = $('.clock-host', el), c = conns.get(activeServer());
      host.hidden = w.host === false || !c;
      if (c && !host.hidden) {
        const dot = 'dot ' + c.status;
        if (host.firstChild.className !== dot) host.firstChild.className = dot;
        if (host.lastChild.textContent !== c.name) host.lastChild.textContent = c.name;
      }
      const d = new Date();
      const t = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hour12: !!w.h12 }).replace(/\s?[AP]M/i, '');
      $('.val', el).innerHTML = `${t}<small>${w.seconds ? String(d.getSeconds()).padStart(2, '0') : w.h12 ? (d.getHours() < 12 ? 'AM' : 'PM') : ''}</small>`;
      $('.w-title', el).textContent = w.title || d.toLocaleDateString([], { weekday: 'long', month: 'long', day: 'numeric' });
    },
  },
  button: {
    name: 'Action button', hint: 'Media keys, lock, launch…', w: 1, h: 1, action: true,
    build: (w) => [h('div', { html: icon(w.icon || actionOf(w)?.icon) }), h('div', { class: 'w-title' })],
    update() {},
  },
  label: {
    name: 'Text', hint: 'A heading or note', w: 4, h: 1, flow: [4, 1],
    build: (w) => h('div', { class: 'val' }, w.title || 'Text'),
    update() {},
  },
};

function actionOf(w) {
  const [sid, id] = splitRef(w.action);
  return conns.get(sid)?.actions.find((a) => a.id === id);
}
function titleOf(w) {
  if (w.title) return w.title;
  const t = TYPES[w.type];
  if (t.sensor) return lookup(w.sensor).meta?.label || '';
  if (w.type === 'cores' && w.metric === 'freq') return 'Core clock speed';
  if (t.action) return actionOf(w)?.label || 'Button';
  return t.defTitle || '';
}

function defaultProfile(conn) {
  const sid = conn.server.id, has = (id) => conn.sensors.has(id);
  const W = (type, x, y, w, hh, extra) => ({ id: uid(), type, x, y, w, h: hh, ...extra });
  const S = (id) => `${sid}:${id}`;
  const disk = [...conn.sensors.keys()].find((id) => /^disk\..*\.used_pct$/.test(id));
  const memMax = conn.sensors.get('mem.used')?.max || 0;
  const hot = { color: '#f97316', warn: 80, crit: 95 };
  const ws = [
    W('ring', 0, 0, 3, 3, { sensor: S('cpu.total'), warn: 75, crit: 90 }),
    memMax
      ? W('ring', 3, 0, 3, 3, { sensor: S('mem.used'), title: 'Memory', unit: 'MB', color: '#22d3ee', warn: Math.round(memMax * 0.85), crit: Math.round(memMax * 0.95) })
      : W('ring', 3, 0, 3, 3, { sensor: S('mem.used_pct'), color: '#22d3ee', warn: 85, crit: 95 }),
    has('gpu.0.util') ? W('ring', 6, 0, 3, 3, { sensor: S('gpu.0.util'), title: 'GPU', color: '#34d399' })
      : disk && W('ring', 6, 0, 3, 3, { sensor: S(disk), color: '#34d399', warn: 85, crit: 95 }),
    W('clock', 9, 0, 3, 2, {}),
    has('temp.cpu') ? W('bar', 9, 2, 3, 1, { sensor: S('temp.cpu'), ...hot })
      : has('temp.max') ? W('bar', 9, 2, 3, 1, { sensor: S('temp.max'), ...hot })
        : W('stat', 9, 2, 3, 1, { sensor: S('sys.uptime') }),
  ];
  // A strip of compact gauges for whatever power, fan and GPU data exists.
  const strip = [
    ['cpu.power', 'CPU power', '#f5a524'], ['gpu.0.power', 'GPU power', '#f5a524'],
    ['fan.0', '', '#22d3ee'], ['fan.1', '', '#22d3ee'],
    ['gpu.0.mem_pct', 'GPU memory', '#34d399'], [has('temp.gpu') ? 'temp.gpu' : 'gpu.0.temp', 'GPU temp', '#f97316'],
    ['gpu.0.fan', 'GPU fan', '#22d3ee'], ['sys.power', 'System power', '#f5a524'], ['temp.ssd', 'SSD temp', '#f97316'],
    ['mem.swap_pct', 'Swap', '#22d3ee'], [disk, '', '#34d399'],
  ].filter(([id]) => id && has(id)).slice(0, 6);
  strip.forEach(([id, title, color], i) => ws.push(W('bar', i * 2, 3, 2, 1, { sensor: S(id), title, color })));
  let y = strip.length ? 4 : 3;
  ws.push(
    W('spark', 0, y, 6, 2, { sensor: S('cpu.total') }),
    W('spark', 6, y, 3, 2, { sensor: S('net.rx'), color: '#22d3ee' }),
    W('spark', 9, y, 3, 2, { sensor: S('net.tx'), color: '#f472b6' }));
  y += 2;
  // Action buttons (media keys, lock…) are left for the user to add.
  if (has('cpu.freq.0')) {
    ws.push(W('cores', 0, y, 4, 3, { server: sid }), W('cores', 4, y, 4, 3, { server: sid, metric: 'freq', color: '#f5a524' }), W('procs', 8, y, 4, 3, { server: sid }));
  } else {
    ws.push(W('cores', 0, y, 6, 3, { server: sid }), W('procs', 6, y, 6, 3, { server: sid }));
  }
  return { id: uid(), name: 'Overview', server: sid, machine: conn.machine || '', widgets: ws };
}

// ---------- rendering ----------
const grid = $('#grid');
let editing = false;
const els = new Map();
const isFlow = () => window.innerWidth < 700;

function applyTheme() {
  const t = state.theme, r = document.documentElement;
  const mode = t.mode === 'auto' ? (matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark') : t.mode;
  // Blur, glow and animated transitions are costly on low-end tablets, which
  // is exactly the hardware dashboards tend to run on.
  const weak = (navigator.hardwareConcurrency || 8) <= 4 || (navigator.deviceMemory || 8) <= 2;
  r.dataset.fx = t.fx === 'auto' ? (weak ? 'lite' : 'on') : t.fx;
  // Thin bars are sized in whole device pixels. On screens with a fractional
  // pixel ratio (1.2, 1.5…) a "6px" bar otherwise lands on 7 or 8 physical
  // pixels depending on where it sits, so neighbours look unevenly thick.
  const dpr = window.devicePixelRatio || 1;
  r.style.setProperty('--bar', Math.max(3, Math.round(6 * dpr)) / dpr + 'px');
  r.style.setProperty('--hair', Math.max(2, Math.round(2 * dpr)) / dpr + 'px');
  r.dataset.mode = mode; r.dataset.card = t.card; r.dataset.font = t.font; r.dataset.bg = t.bg;
  r.style.setProperty('--accent', t.accent);
  r.style.setProperty('--radius', t.radius + 'px');
  r.style.setProperty('--gap', t.gap + 'px');
  $('#backdrop').style.backgroundImage = t.bg === 'image' && t.bgImage ? `url("${t.bgImage}")` : '';
  $('meta[name=theme-color]').content = mode === 'light' ? '#f1f2f6' : '#0a0c11';
}

function buildWidget(w) {
  const t = TYPES[w.type];
  const el = h('div', { class: `w w-${w.type}`, 'data-id': w.id },
    h('div', { class: 'w-body' }, t.build(w)),
    h('div', { class: 'w-resize' }));
  if (w.color) el.style.setProperty('--wc', w.color);
  const title = $('.w-title', el);
  if (title && w.type !== 'clock') title.textContent = titleOf(w);
  // In edit mode a tap (not a drag) opens the widget's settings.
  el.addEventListener('click', () => {
    if (editing) { if (!el.dragged) openWidgetEditor(w); } else if (w.type === 'button') fire(w, el);
  });
  el.addEventListener('pointerdown', (e) => startDrag(e, w, el));
  return el;
}

function fire(w, el) {
  if (editing) return;
  const [sid, id] = splitRef(w.action);
  const conn = conns.get(sid);
  if (!conn) return toast('This button has no action yet');
  conn.action(id, (ok, err) => {
    el.classList.add(ok ? 'fired' : 'failed');
    setTimeout(() => el.classList.remove('fired', 'failed'), 500);
    if (!ok) toast(err || 'Action failed');
  });
}

function place(w, el) {
  if (isFlow()) {
    // Phones get a 4-column flowing layout; sizes derive from the desktop ones.
    const fw = w.w <= 2 ? 1 : w.w <= 4 ? 2 : 4;
    const [cw, ch] = TYPES[w.type].flow || [fw, Math.max(1, Math.round(w.h * (fw / w.w) * 0.9))];
    el.style.gridColumn = `span ${cw}`;
    el.style.gridRow = `span ${ch}`;
  } else {
    el.style.gridColumn = `${w.x + 1} / span ${w.w}`;
    el.style.gridRow = `${w.y + 1} / span ${w.h}`;
  }
}

function layout() {
  const p = profile();
  const flow = isFlow(), cols = flow ? 4 : 12, gap = state.theme.gap;
  const cs = getComputedStyle(grid);
  const innerW = grid.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
  const cell = (innerW - gap * (cols - 1)) / cols;
  let cellH = cell;
  if (!flow && state.theme.fit && p) {
    // Stretch rows so the whole dashboard fills the screen without scrolling.
    const rows = Math.max(1, ...p.widgets.map((w) => w.y + w.h)) + (editing ? 1 : 0);
    const availH = window.innerHeight - parseFloat(cs.paddingTop) - (editing ? 110 : parseFloat(cs.paddingBottom));
    cellH = Math.max(44, (availH - gap * (rows - 1)) / rows);
  }
  grid.classList.toggle('flow', flow);
  grid.style.setProperty('--cols', cols);
  grid.style.setProperty('--cell', cell + 'px');
  grid.style.setProperty('--cell-h', cellH + 'px');
  for (const el of els.values()) el.sparkSize = null; // canvases re-measure on next draw
  return { cell, cellH, gap, cols };
}

function render() {
  applyTheme();
  const p = profile();
  els.clear();
  grid.replaceChildren();
  const empty = $('#empty');
  if (!p) {
    empty.hidden = false;
    const any = state.servers.length > 0;
    empty.replaceChildren(h('div', { class: 'box' },
      h('img', { src: 'icon.svg', alt: '' }),
      h('h1', {}, any ? `Connecting to ${conns.get(activeServer())?.name || 'your computer'}…` : 'Looking for your computer'),
      h('p', {}, any ? 'Waiting for the Pulsedeck server to answer.' : 'Start the Pulsedeck server on your computer. It will show up here automatically when both devices are on the same network.'),
      h('button', { class: 'btn primary', html: icon('plus') + 'Add by address', onclick: openComputers })));
  } else {
    empty.hidden = true;
    const ws = isFlow() ? [...p.widgets].sort((a, b) => a.y - b.y || a.x - b.x) : p.widgets;
    for (const w of ws) {
      if (!TYPES[w.type]) continue;
      const el = buildWidget(w);
      place(w, el);
      els.set(w.id, el);
      grid.append(el);
    }
  }
  layout();
  renderDock();
  update();
}

let queued = false;
function scheduleUpdate() {
  if (queued) return;
  queued = true;
  requestAnimationFrame(() => { queued = false; update(); });
}
function update() {
  updateSwitcher();
  const p = profile();
  if (!p) return;
  for (const w of p.widgets) {
    const el = els.get(w.id), t = TYPES[w.type];
    if (!el) continue;
    if (t.sensor) {
      const s = lookup(w.sensor);
      el.classList.toggle('stale', !s.live);
      el.dataset.tone = tone(w, s.val);
      t.update(el, w, s);
    } else t.update(el, w);
  }
}
setInterval(() => { for (const w of profile()?.widgets || []) if (w.type === 'clock' && els.has(w.id)) TYPES.clock.update(els.get(w.id), w); }, 1000);

// ---------- dock ----------
function renderDock() {
  const dock = $('#dock');
  const btn = (ic, title, onclick, on, text) => h('button', { title, class: on ? 'on' : '', onclick, html: icon(ic) + (text ? `<span>${text}</span>` : '') });
  const kids = [];
  const sid = activeServer(), ac = conns.get(sid);
  if (ac) {
    // One pill for the computer on screen; tapping it opens the switcher.
    const others = [...conns.values()].filter((c) => c !== ac);
    kids.push(h('button', { class: 'server-pill', title: 'Switch computer', onclick: toggleSwitcher },
      h('span', { class: 'dot ' + ac.status }), h('span', {}, ac.name),
      others.length ? h('span', { class: 'more' }, others.map((c) => h('span', { class: 'dot ' + c.status }))) : null), h('div', { class: 'sep' }));
  }
  const list = serverProfiles();
  if (list.length > 1) {
    kids.push(h('div', { class: 'tabs' }, list.map((p) => h('button', { class: p === profile() ? 'on' : '', onclick: () => { state.sel[sid] = p.id; save(); render(); } }, p.name))), h('div', { class: 'sep' }));
  }
  if (editing) kids.push(btn('plus', 'Add widget', openPalette, false, 'Add'));
  if (profile()) kids.push(btn(editing ? 'check' : 'edit', 'Edit layout', toggleEdit, editing, editing ? 'Done' : ''));
  kids.push(btn('settings', 'Settings', () => openSettings()));
  if (!native && document.documentElement.requestFullscreen) kids.push(btn('full', 'Fullscreen', () => (document.fullscreenElement ? document.exitFullscreen() : document.documentElement.requestFullscreen().catch(() => {}))));
  dock.replaceChildren(...kids);
}
// ---------- computer switcher ----------
const OS_NAMES = { darwin: 'macOS', linux: 'Linux', windows: 'Windows' };
const SW_STATS = [['CPU', ['cpu.total']], ['Memory', ['mem.used_pct']], ['GPU', ['gpu.0.util']], ['Temp', ['temp.cpu', 'temp.max']]];
function switchServer(sid) {
  if (editing) toggleEdit();
  state.server = sid;
  save(); closeSwitcher(); render();
}
function closeSwitcher() { $('#switcher').hidden = true; if ($('#sheet').hidden) $('#scrim').hidden = true; }
function toggleSwitcher() {
  const box = $('#switcher');
  if (!box.hidden) return closeSwitcher();
  const cur = activeServer();
  box.replaceChildren(h('div', { class: 'sw-grid' },
    state.servers.map((sv) => {
      const c = conns.get(sv.id);
      return h('button', { class: 'sw-card' + (sv.id === cur ? ' on' : ''), 'data-sid': sv.id, onclick: () => switchServer(sv.id) },
        h('div', { class: 'sw-top' }, h('span', { class: 'dot ' + (c?.status || '') }), h('b', {}, c?.name || sv.name || sv.url), h('small', { class: 'sw-os' })),
        h('div', { class: 'sw-stats' }, SW_STATS.map(([label]) => h('div', {}, h('i', {}, label), h('span', {}, '–')))),
        h('small', { class: 'sw-addr' }));
    }),
    h('button', { class: 'sw-card sw-add', html: icon('plus') + '<span>Add computer</span>', onclick: openComputers })),
    h('div', { class: 'sw-foot' }, h('button', { class: 'btn', html: icon('settings') + 'Manage computers', onclick: openComputers })));
  box.hidden = false;
  $('#scrim').hidden = false;
  updateSwitcher(); wake();
}
function updateSwitcher() {
  const box = $('#switcher');
  if (box.hidden) return;
  for (const card of box.querySelectorAll('[data-sid]')) {
    const c = conns.get(card.dataset.sid);
    if (!c) continue;
    $('.dot', card).className = 'dot ' + c.status;
    $('.sw-os', card).textContent = OS_NAMES[c.os] || '';
    $('.sw-addr', card).textContent = (c.server.url || location.host) + (c.status === 'open' ? '' : c.status === 'connecting' ? ' · connecting…' : ' · offline');
    card.classList.toggle('off', c.status !== 'open');
    SW_STATS.forEach(([, ids], i) => {
      const id = ids.find((x) => c.sensors.has(x)), v = id && c.status === 'open' ? c.values[id] : null;
      const text = typeof v === 'number' ? fmt(v, c.sensors.get(id).unit).join('') : '–';
      const span = card.querySelectorAll('.sw-stats span')[i];
      if (span.textContent !== text) span.textContent = text;
    });
  }
}
// Swiping sideways on the dashboard steps through the computers.
let swipe = null;
grid.addEventListener('touchstart', (e) => { swipe = e.touches.length === 1 ? { x: e.touches[0].clientX, y: e.touches[0].clientY, t: Date.now() } : null; }, { passive: true });
grid.addEventListener('touchend', (e) => {
  if (!swipe || editing || state.servers.length < 2) return;
  const dx = e.changedTouches[0].clientX - swipe.x, dy = e.changedTouches[0].clientY - swipe.y, quick = Date.now() - swipe.t < 700;
  swipe = null;
  if (!quick || Math.abs(dx) < 90 || Math.abs(dy) > Math.abs(dx) * 0.5) return;
  const i = state.servers.findIndex((sv) => sv.id === activeServer());
  const next = state.servers[(i + (dx < 0 ? 1 : -1) + state.servers.length) % state.servers.length];
  switchServer(next.id);
  toast(conns.get(next.id)?.name || next.name || next.url);
}, { passive: true });

function toggleEdit() {
  editing = !editing;
  document.body.classList.toggle('editing', editing);
  if (!editing) closeSheet();
  layout(); renderDock(); wake();
}

// The dock gets out of the way after a few seconds; any touch brings it back.
let idleTimer;
function wake() {
  document.body.classList.remove('idle');
  clearTimeout(idleTimer);
  idleTimer = setTimeout(() => { if (!editing && $('#sheet').hidden && $('#switcher').hidden && profile()) document.body.classList.add('idle'); }, 5000);
}
['pointerdown', 'pointermove', 'keydown'].forEach((ev) => addEventListener(ev, wake, { passive: true }));

// ---------- drag & resize ----------
const overlap = (a, b) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
function settle(moving, widgets) {
  const placed = [moving];
  for (const o of widgets.filter((w) => w !== moving).sort((a, b) => a.y - b.y || a.x - b.x)) {
    let hit;
    while ((hit = placed.find((p) => overlap(p, o)))) o.y = hit.y + hit.h;
    placed.push(o);
  }
}
function startDrag(e, w, el) {
  el.dragged = false;
  if (!editing || isFlow() || e.button > 0) return;
  e.preventDefault();
  const resize = !!e.target.closest('.w-resize');
  const { cell, cellH, gap } = layout();
  const widgets = profile().widgets;
  const snap = new Map(widgets.map((x) => [x, { x: x.x, y: x.y }]));
  const start = { px: e.clientX, py: e.clientY, x: w.x, y: w.y, w: w.w, h: w.h };
  el.setPointerCapture(e.pointerId);
  el.classList.add('dragging');
  const move = (ev) => {
    const dx = Math.round((ev.clientX - start.px) / (cell + gap)), dy = Math.round((ev.clientY - start.py) / (cellH + gap));
    if (Math.abs(ev.clientX - start.px) + Math.abs(ev.clientY - start.py) > 6) el.dragged = true;
    for (const [x, pos] of snap) if (x !== w) Object.assign(x, pos);
    if (resize) {
      w.w = Math.min(12 - w.x, Math.max(1, start.w + dx));
      w.h = Math.max(1, start.h + dy);
    } else {
      w.x = Math.min(12 - w.w, Math.max(0, start.x + dx));
      w.y = Math.max(0, start.y + dy);
    }
    settle(w, widgets);
    for (const x of widgets) if (els.has(x.id)) place(x, els.get(x.id));
  };
  const up = () => {
    el.removeEventListener('pointermove', move);
    el.removeEventListener('pointerup', up);
    el.removeEventListener('pointercancel', up);
    el.classList.remove('dragging');
    save(); layout(); update();
  };
  el.addEventListener('pointermove', move);
  el.addEventListener('pointerup', up);
  el.addEventListener('pointercancel', up);
}

// ---------- sheets & form controls ----------
function openSheet(title, ...content) {
  const sheet = $('#sheet');
  sheet.replaceChildren(
    h('header', {}, h('h2', {}, title), h('button', { class: 'icon-btn', title: 'Close', html: icon('x'), onclick: closeSheet })),
    h('div', { class: 'content' }, content));
  sheet.hidden = false;
  $('#scrim').hidden = false;
  wake();
}
function closeSheet() { refreshComputers = () => {}; $('#sheet').hidden = true; if ($('#switcher').hidden) $('#scrim').hidden = true; wake(); }
$('#scrim').addEventListener('click', () => { closeSheet(); closeSwitcher(); });

let toastTimer;
function toast(msg) {
  const t = $('#toast');
  t.textContent = msg; t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { t.hidden = true; }, 3200);
}

const field = (label, control, col) => h('label', { class: 'field' + (col ? ' col' : '') }, h('span', {}, label), control);
const textInput = (value, oninput, attrs = {}) => h('input', { type: 'text', value: value ?? '', autocomplete: 'off', autocapitalize: 'off', spellcheck: 'false', ...attrs, oninput: (e) => oninput(e.target.value) });
const numInput = (value, oninput, placeholder) => h('input', { type: 'number', value: value ?? '', placeholder, step: 'any', oninput: (e) => oninput(e.target.value === '' ? null : +e.target.value) });
function select(groups, value, onchange) {
  const sel = h('select', { onchange: (e) => onchange(e.target.value) });
  for (const [g, opts] of Object.entries(groups)) {
    const mk = ([v, l]) => h('option', { value: v, selected: v === value }, l);
    if (g) sel.append(h('optgroup', { label: g }, opts.map(mk))); else sel.append(...opts.map(mk));
  }
  return sel;
}
function seg(options, value, onpick) {
  const box = h('div', { class: 'seg' });
  for (const [v, l] of options) box.append(h('button', { type: 'button', class: v === value ? 'on' : '', onclick: (e) => { for (const b of box.children) b.classList.remove('on'); e.currentTarget.classList.add('on'); onpick(v); } }, l));
  return box;
}
const PALETTE = ['#7c5cff', '#3b82f6', '#22d3ee', '#34d399', '#a3e635', '#f5a524', '#f97316', '#f43f5e', '#f472b6', '#e5e7eb'];
function swatches(value, onpick, allowAuto) {
  const box = h('div', { class: 'swatches' });
  const mark = (el) => { for (const b of box.children) b.classList.remove('on'); el?.classList.add('on'); };
  if (allowAuto) box.append(h('button', { type: 'button', class: 'auto' + (!value ? ' on' : ''), title: 'Use accent', onclick: (e) => { mark(e.currentTarget); onpick(''); } }));
  for (const c of PALETTE) box.append(h('button', { type: 'button', class: c === value ? 'on' : '', style: `--s:${c}`, title: c, onclick: (e) => { mark(e.currentTarget); onpick(c); } }));
  const custom = h('label', { title: 'Custom colour', class: value && !PALETTE.includes(value) ? 'on' : '' }, h('input', { type: 'color', value: value || state.theme.accent, oninput: (e) => { mark(custom); onpick(e.target.value); } }));
  box.append(custom);
  return box;
}

// A dashboard belongs to one computer, so its widgets only ever offer that
// computer's sensors and buttons.
function sensorGroups() {
  const out = {}, c = conns.get(activeServer());
  if (!c) return out;
  for (const s of c.sensors.values()) {
    if (s.kind === 'list') continue;
    (out[s.group] ||= []).push([`${c.server.id}:${s.id}`, s.label]);
  }
  return out;
}

function openWidgetEditor(w) {
  const t = TYPES[w.type];
  const apply = () => {
    save();
    const old = els.get(w.id), el = buildWidget(w);
    place(w, el); els.set(w.id, el); old?.replaceWith(el); update();
  };
  const set = (k) => (v) => { if (v == null || v === '') delete w[k]; else w[k] = v; apply(); };
  const body = [];
  if (t.sensor) {
    body.push(field('Shows', select(sensorGroups(), w.sensor, set('sensor')), true));
    body.push(field('Style', seg([['ring', 'Ring'], ['bar', 'Bar'], ['spark', 'Graph'], ['stat', 'Number']], w.type, (v) => { w.type = v; apply(); openWidgetEditor(w); })));
  }
  if (t.action) {
    const own = conns.get(activeServer());
    const acts = own ? own.actions.map((x) => [`${own.server.id}:${x.id}`, x.label]) : [];
    body.push(field('Action', select({ '': [['', 'Choose…'], ...acts] }, w.action || '', set('action')), true));
    body.push(field('Icon', select({ '': [['', 'Automatic'], ...['play', 'next', 'prev', 'volup', 'voldown', 'mute', 'lock', 'link', 'terminal', 'bolt', 'power'].map((i) => [i, i])] }, w.icon || '', set('icon'))));
    body.push(h('p', { class: 'hint' }, 'Actions are defined in the server’s config file, so a dashboard can only run what you allowed there.'));
  }
  if (w.type === 'cores' && conns.get(activeServer())?.sensors.has('cpu.freq.0')) {
    body.push(field('Shows', seg([['usage', 'Usage'], ['freq', 'Clock speed']], w.metric || 'usage', set('metric'))));
  }
  body.push(field(w.type === 'label' ? 'Text' : 'Title', textInput(w.title, set('title'), { placeholder: titleOf({ ...w, title: '' }) || 'Automatic' }), true));
  if (w.type !== 'clock') body.push(h('h3', {}, 'Colour'), swatches(w.color || '', set('color'), true));
  if (t.sensor) {
    body.push(h('h3', {}, 'Scale'),
      field('Range', h('div', { class: 'pair' }, numInput(w.min, set('min'), 'min'), numInput(w.max, set('max'), 'max'))),
      field('Warn / critical at', h('div', { class: 'pair' }, numInput(w.warn, set('warn'), 'warn'), numInput(w.crit, set('crit'), 'crit'))),
      field('Unit', textInput(w.unit, set('unit'), { placeholder: 'Automatic' })),
      field('Decimals', numInput(w.decimals, set('decimals'), 'auto')),
      h('p', { class: 'hint' }, 'Leave the range empty to use the sensor’s own range, or auto-scale when it has none. For memory and data sizes, a unit of MB or GB pins the scale. The widget turns amber and red at the warn and critical values.'));
  }
  if (w.type === 'clock') body.push(field('Computer name', seg([[true, 'On'], [false, 'Off']], w.host !== false, (v) => { if (v) delete w.host; else w.host = false; apply(); })), field('12-hour', seg([[false, 'Off'], [true, 'On']], !!w.h12, set('h12'))), field('Seconds', seg([[false, 'Off'], [true, 'On']], !!w.seconds, set('seconds'))));
  body.push(h('div', { class: 'row', style: 'margin-top:24px' },
    h('button', { class: 'btn', html: icon('copy') + 'Duplicate', onclick: () => { const c = { ...w, id: uid(), y: w.y + w.h }; profile().widgets.push(c); settle(c, profile().widgets); save(); render(); } }),
    h('button', { class: 'btn danger', html: icon('trash') + 'Remove', onclick: () => { profile().widgets = profile().widgets.filter((x) => x !== w); save(); closeSheet(); render(); } })));
  openSheet(t.name, body);
}

function openPalette() {
  const first = conns.get(activeServer()) || conns.values().next().value;
  const add = (type) => {
    const t = TYPES[type], p = profile();
    const w = { id: uid(), type, x: 0, y: Math.max(0, ...p.widgets.map((x) => x.y + x.h)), w: t.w, h: t.h };
    if (t.sensor && first) w.sensor = `${first.server.id}:cpu.total`;
    if (t.server && first) w.server = first.server.id;
    p.widgets.push(w); save(); render(); openWidgetEditor(w);
    els.get(w.id)?.scrollIntoView({ behavior: 'smooth', block: 'center' });
  };
  openSheet('Add widget', h('div', { class: 'palette' }, Object.entries(TYPES).map(([k, t]) => h('button', { onclick: () => add(k) }, t.name, h('small', {}, t.hint)))));
}

let refreshComputers = () => {}; // keeps the open Computers panel's status lines live
// Computers have their own panel (reached from the switcher), separate from
// the look-and-feel settings.
function openComputers() {
  closeSwitcher();
  const list = h('div', {});
  let draw = () => {
    list.replaceChildren(...state.servers.map((sv) => {
      const c = conns.get(sv.id);
      return h('div', { class: 'pc-card', 'data-sid': sv.id },
        h('div', { class: 'pc-head' }, h('span', { class: 'dot ' + (c?.status || '') }),
          h('div', { class: 'grow' }, h('b', {}, c?.name || sv.name || sv.url), h('small')),
          sv.id !== 'local' && h('button', { class: 'icon-btn', title: 'Forget this computer', html: icon('trash'), onclick: () => {
            if (!confirm(`Forget ${c?.name || sv.name || sv.url} and its dashboards?`)) return;
            state.servers = state.servers.filter((x) => x !== sv); state.profiles = state.profiles.filter((x) => x.server !== sv.id); delete state.sel[sv.id];
            save(); syncConns(); draw(); render();
          } })),
        field('Pairing token', textInput(sv.token, (v) => { sv.token = v.trim(); save(); }, { placeholder: 'needed for buttons' })));
    }));
    if (!state.servers.length) list.append(h('p', { class: 'hint' }, native ? 'No computers yet. Start Pulsedeck on a computer on this network and it will appear here.' : 'No computers yet.'));
  };
  draw();
  refreshComputers = () => {
    for (const card of list.querySelectorAll('[data-sid]')) {
      const c = conns.get(card.dataset.sid);
      if (!c) continue;
      $('.dot', card).className = 'dot ' + c.status;
      $('b', card).textContent = c.name;
      const text = [OS_NAMES[c.os], c.server.url || location.host, c.status === 'open' ? 'Connected' : c.status === 'connecting' ? 'Connecting…' : 'Offline'].filter(Boolean).join(' · ');
      if ($('small', card).textContent !== text) $('small', card).textContent = text;
    }
  };
  const drawList = draw;
  draw = () => { drawList(); refreshComputers(); };
  refreshComputers();
  let addr = '';
  const add = () => {
    addr = addr.trim().replace(/^https?:\/\//, '').replace(/\/.*$/, '');
    if (!addr) return;
    if (!/:\d+$/.test(addr)) addr += ':' + DEFAULT_PORT;
    if (state.servers.some((x) => x.url === addr)) return toast('That computer is already added');
    state.servers.push({ id: uid(), url: addr, token: '' }); save(); syncConns(); render(); openComputers();
  };
  openSheet('Computers',
    list,
    h('p', { class: 'hint' }, 'The pairing token is printed by Pulsedeck on the computer when it starts. Monitoring works without it; buttons that control the computer need it.'),
    h('h3', {}, 'Add a computer'),
    h('div', { class: 'row' },
      textInput('', (v) => { addr = v; }, { placeholder: '192.168.1.20:7878', inputmode: 'url', style: 'flex:1;background:var(--track);border:0;border-radius:10px;padding:9px 11px;min-width:0', onkeydown: (e) => { if (e.key === 'Enter') add(); } }),
      h('button', { class: 'btn primary', html: icon('plus') + 'Add', onclick: add })),
    h('p', { class: 'hint' }, native ? 'Computers on the same network are found automatically. Add one by address only if it does not show up.' : 'Enter the address Pulsedeck prints on that computer.'),
    native?.setWake && h('h3', {}, 'Screen'),
    native?.setWake && field('Wake with computer', seg([[true, 'On'], [false, 'Off']], !!state.wake, (v) => { state.wake = v; save(); tellNative(); })),
    native?.setWake && h('p', { class: 'hint' }, 'The screen turns on when a computer comes online and is allowed to sleep when none is connected. Keep this device on its charger for that to work reliably.'));
}

function openSettings() {
  const t = state.theme;
  const setT = (k) => (v) => { t[k] = v; save(); applyTheme(); layout(); };
  const profiles = h('div', {});
  const drawProfiles = () => profiles.replaceChildren(...serverProfiles().map((p) => h('div', { class: 'list-item' },
    h('div', { class: 'grow' }, textInput(p.name, (v) => { p.name = v || 'Dashboard'; save(); renderDock(); }, { style: 'width:100%;background:transparent;border:0;padding:0;font-weight:600' })),
    h('button', { class: 'icon-btn', title: 'Duplicate', html: icon('copy'), onclick: () => { state.profiles.push({ ...JSON.parse(JSON.stringify(p)), id: uid(), name: p.name + ' copy' }); save(); drawProfiles(); renderDock(); } }),
    serverProfiles().length > 1 && h('button', { class: 'icon-btn', title: 'Delete', html: icon('trash'), onclick: () => { state.profiles = state.profiles.filter((x) => x !== p); save(); drawProfiles(); render(); } }))));
  drawProfiles();

  const fileIn = h('input', { type: 'file', accept: 'image/*', hidden: true, onchange: (e) => e.target.files[0] && loadBackground(e.target.files[0]) });
  const importIn = h('input', { type: 'file', accept: '.json,application/json', hidden: true, onchange: (e) => e.target.files[0] && importFile(e.target.files[0]) });

  openSheet('Settings',
    h('h3', {}, 'Appearance'),
    field('Theme', seg([['dark', 'Dark'], ['light', 'Light'], ['auto', 'Auto']], t.mode, setT('mode'))),
    field('Accent', swatches(t.accent, setT('accent')), true),
    field('Cards', seg([['glass', 'Glass'], ['solid', 'Solid'], ['outline', 'Outline'], ['bare', 'Bare']], t.card, setT('card')), true),
    field('Font', seg([['sans', 'Sans'], ['rounded', 'Rounded'], ['mono', 'Mono'], ['serif', 'Serif']], t.font, setT('font')), true),
    field('Corner radius', h('input', { type: 'range', min: 0, max: 36, value: t.radius, oninput: (e) => setT('radius')(+e.target.value) })),
    field('Spacing', h('input', { type: 'range', min: 2, max: 28, value: t.gap, oninput: (e) => setT('gap')(+e.target.value) })),
    field('Effects', seg([['auto', 'Auto'], ['on', 'Full'], ['lite', 'Lite'], ['off', 'Off']], t.fx, (v) => { setT('fx')(v); update(); })),
    field('Fit to screen', seg([[true, 'On'], [false, 'Off']], t.fit, setT('fit'))),
    field('Background', seg([['aurora', 'Aurora'], ['plain', 'Plain'], ['grid', 'Grid'], ['image', 'Image']], t.bg, (v) => { if (v === 'image' && !t.bgImage) fileIn.click(); setT('bg')(v); }), true),
    h('div', { class: 'row' }, h('button', { class: 'btn', onclick: () => fileIn.click() }, 'Choose image…'), fileIn),

    h('h3', {}, 'Dashboards' + (state.servers.length > 1 && conns.get(activeServer()) ? ' · ' + conns.get(activeServer()).name : '')),
    profiles,
    h('div', { class: 'row' },
      h('button', { class: 'btn', html: icon('plus') + 'New dashboard', onclick: () => { const np = { id: uid(), name: 'Dashboard ' + (serverProfiles().length + 1), server: activeServer(), machine: machineOf(activeServer()), widgets: [] }; state.profiles.push(np); state.sel[np.server] = np.id; save(); render(); drawProfiles(); if (!editing) toggleEdit(); } }),
      h('button', { class: 'btn', onclick: exportFile }, 'Export'),
      h('button', { class: 'btn', onclick: () => importIn.click() }, 'Import'), importIn),
    h('div', { class: 'row' },
      h('button', { class: 'btn danger', onclick: () => {
        const c = conns.get(activeServer());
        if (c?.status !== 'open') return toast('This computer is not connected');
        if (!confirm('Replace the current dashboard with the default layout?')) return;
        const cur = profile(), fresh = { ...defaultProfile(c), name: cur?.name || 'Overview' };
        if (cur) state.profiles[state.profiles.indexOf(cur)] = fresh; else state.profiles.push(fresh);
        state.sel[fresh.server] = fresh.id; save(); render();
      } }, 'Reset this dashboard')));
}

function loadBackground(file) {
  const img = new Image();
  img.onload = () => {
    // Downscale so the image fits comfortably in local storage.
    const k = Math.min(1, 1920 / Math.max(img.width, img.height));
    const c = h('canvas', { width: Math.round(img.width * k), height: Math.round(img.height * k) });
    c.getContext('2d').drawImage(img, 0, 0, c.width, c.height);
    state.theme.bgImage = c.toDataURL('image/jpeg', 0.82);
    state.theme.bg = 'image';
    URL.revokeObjectURL(img.src);
    save(); applyTheme();
  };
  img.onerror = () => toast('Could not read that image');
  img.src = URL.createObjectURL(file);
}
function exportFile() {
  const data = JSON.stringify({ pulsedeck: 1, theme: state.theme, profiles: serverProfiles() }, null, 2);
  const a = h('a', { href: URL.createObjectURL(new Blob([data], { type: 'application/json' })), download: 'pulsedeck-dashboard.json' });
  if (native?.saveFile) native.saveFile('pulsedeck-dashboard.json', data); else a.click();
}
async function importFile(file) {
  try {
    const d = JSON.parse(await file.text());
    if (d.pulsedeck !== 1 || !Array.isArray(d.profiles)) throw new Error('bad file');
    // An imported dashboard replaces the ones of the computer on screen, and
    // its widgets are re-pointed at that computer.
    const sid = activeServer();
    if (!sid) throw new Error('no computer');
    const fix = (ref) => { const id = splitRef(ref)[1]; return id ? `${sid}:${id}` : ref; };
    for (const p of d.profiles) {
      p.id = uid(); p.server = sid; p.machine = machineOf(sid);
      for (const w of p.widgets || []) {
        if (w.sensor) w.sensor = fix(w.sensor);
        if (w.action) w.action = fix(w.action);
        if (w.server) w.server = sid;
      }
    }
    const mine = new Set(serverProfiles(sid));
    state.profiles = state.profiles.filter((p) => !mine.has(p)).concat(d.profiles);
    delete state.sel[sid];
    if (d.theme) state.theme = { ...defaults().theme, ...d.theme };
    save(); render(); toast('Dashboard imported');
  } catch { toast('That file is not a Pulsedeck dashboard'); }
}

// Called by the native shells for the system back button/gesture.
window.__pulsedeckBack = () => {
  if (!$('#switcher').hidden) { closeSwitcher(); return true; }
  if (!$('#sheet').hidden) { closeSheet(); return true; }
  if (editing) { toggleEdit(); return true; }
  return false;
};

// ---------- boot ----------
addEventListener('resize', () => { const flow = grid.classList.contains('flow'); if (flow !== isFlow()) render(); else { layout(); update(); } });
matchMedia('(prefers-color-scheme: light)').addEventListener('change', applyTheme);
document.addEventListener('visibilitychange', () => {
  if (document.hidden) return;
  keepAwake();
  // Coming back (e.g. the screen just woke): reconnect now, not on the next retry.
  for (const c of conns.values()) if (c.status === 'closed') { c.retry = 1000; c.connect(); }
});
// In the native shells the shell itself decides when the screen may sleep.
async function keepAwake() { if (native) return; try { await navigator.wakeLock?.request('screen'); } catch { /* not allowed right now */ } }

syncConns();
render();
wake();
keepAwake();
native?.startDiscovery?.();
// Screenshot aids for the demo: ?demo&switcher opens the computer switcher, ?demo&edit the editor.
if (DEMO) setTimeout(() => { if (/[?&]switcher\b/.test(location.search)) toggleSwitcher(); if (/[?&]edit\b/.test(location.search)) { toggleEdit(); openWidgetEditor(profile().widgets[0]); } }, 1500);
