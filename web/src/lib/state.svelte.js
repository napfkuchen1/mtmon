import { api, AuthError } from './api.js'

export const app = $state({
  user: null,          // null = unknown, false = logged out
  range: localStorage.getItem('mtmon.range') || '1h',
  theme: localStorage.getItem('mtmon.theme') || 'auto',
  density: localStorage.getItem('mtmon.density') || 'comfortable', // comfortable | compact
  chart: localStorage.getItem('mtmon.chart') || 'modern',          // modern | classic
  route: { name: 'overview', params: {} },
  live: { connected: false, up_bps: 0, down_bps: 0, clients: [], devices: [], ifaces: [], flows_ps: 0 },
  history: [],         // rolling live totals [{t, up, down}]
  feed: [],            // live connection feed (newest first)
  paused: false,
  toast: ''
})

export function setRange(r) { app.range = r; try { localStorage.setItem('mtmon.range', r) } catch {} }
export function setTheme(t) {
  app.theme = t
  try { localStorage.setItem('mtmon.theme', t) } catch {}
  applyTheme()
}
export function setPref(key, val) { // key: density | chart
  app[key] = val
  try { localStorage.setItem('mtmon.' + key, val) } catch {}
  applyTheme()
}
export function applyTheme() {
  document.documentElement.setAttribute('data-density', app.density)
  const el = document.documentElement
  if (app.theme === 'auto') el.removeAttribute('data-theme'); else el.setAttribute('data-theme', app.theme)
}
export function toast(msg) { app.toast = msg; setTimeout(() => { if (app.toast === msg) app.toast = '' }, 2200) }

export async function checkSession() {
  try { const me = await api('/me'); app.user = me.user || false } catch { app.user = false }
}
export function onAuthError(e) { if (e instanceof AuthError) { app.user = false; closeLive(); return true } return false }

// ---- routing (hash based) ----
export function parseHash() {
  const h = location.hash.replace(/^#\/?/, '') || 'overview'
  const [name, ...rest] = h.split('/')
  app.route = { name, params: { id: rest.length ? decodeURIComponent(rest.join('/')) : '' } }
}
export function go(path) { location.hash = '#/' + path }

// ---- live websocket ----
let ws, retry = 0, timer
export function openLive() {
  if (ws && ws.readyState <= 1) return
  const proto = location.protocol === 'https:' ? 'wss' : 'ws'
  ws = new WebSocket(`${proto}://${location.host}/api/live`)
  ws.onopen = () => { app.live.connected = true; retry = 0; if (app.paused) ws.send(JSON.stringify({ Pause: true })) }
  ws.onmessage = ev => {
    const s = JSON.parse(ev.data)
    Object.assign(app.live, { up_bps: s.up_bps, down_bps: s.down_bps, clients: s.clients || [], devices: s.devices || [], ifaces: s.ifaces || [], flows_ps: s.flows_ps })
    app.history.push({ t: s.ts, up: s.up_bps, down: s.down_bps })
    if (app.history.length > 300) app.history.splice(0, app.history.length - 300)
    if (s.conns && s.conns.length) {
      app.feed = [...s.conns, ...app.feed].slice(0, 300)
    }
  }
  ws.onclose = () => {
    app.live.connected = false
    if (app.user) { retry = Math.min(retry + 1, 6); timer = setTimeout(openLive, 1000 * 2 ** retry / 2) }
  }
}
export function closeLive() { clearTimeout(timer); if (ws) { ws.onclose = null; ws.close() } app.live.connected = false }
export function setPaused(p) { app.paused = p; if (ws && ws.readyState === 1) ws.send(JSON.stringify({ Pause: p })) }
