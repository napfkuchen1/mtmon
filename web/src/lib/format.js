const fmtN = (i, n) => (i === 0 || n >= 100 ? n.toFixed(0) : n.toFixed(1).replace(/\.0$/, ''))
export function bytes(n) {
  n = Number(n) || 0
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  while (n >= 1000 && i < u.length - 1) { n /= 1000; i++ }
  return fmtN(i, n) + ' ' + u[i]
}
export function bps(n) {
  n = Number(n) || 0
  const u = ['bit/s', 'Kbit/s', 'Mbit/s', 'Gbit/s']
  let i = 0
  while (n >= 1000 && i < u.length - 1) { n /= 1000; i++ }
  return fmtN(i, n) + ' ' + u[i]
}
export function num(n) { return (Number(n) || 0).toLocaleString('en-US') }
export function dur(s) {
  s = Math.floor(Number(s) || 0)
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60)
  if (d) return `${d}d ${h}h`
  if (h) return `${h}h ${m}m`
  if (m) return `${m}m`
  return `${s}s`
}
export function ago(ts) {
  if (!ts) return '—'
  const s = Math.floor(Date.now() / 1000 - ts)
  if (s < 5) return 'just now'
  if (s < 60) return s + 's ago'
  if (s < 3600) return Math.floor(s / 60) + 'm ago'
  if (s < 86400) return Math.floor(s / 3600) + 'h ago'
  return Math.floor(s / 86400) + 'd ago'
}
export function clock(ts) {
  return new Date(ts * 1000).toLocaleTimeString([], { hour12: false })
}
export function datetime(ts) {
  return new Date(ts * 1000).toLocaleString([], { hour12: false, month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
export function flag(cc) {
  if (!cc || cc.length !== 2) return ''
  return String.fromCodePoint(...[...cc.toUpperCase()].map(c => 127397 + c.charCodeAt(0)))
}
export const protoName = p => ({ 1: 'ICMP', 6: 'TCP', 17: 'UDP', 47: 'GRE', 50: 'ESP', 58: 'ICMPv6' }[p] || 'P' + p)
export function display(c) { return c.label || c.hostname || c.ip || c.mac }
export function signalQuality(dbm) {
  if (!dbm) return { cls: '', txt: '—' }
  if (dbm >= -60) return { cls: 'ok', txt: dbm + ' dBm' }
  if (dbm >= -72) return { cls: 'warn', txt: dbm + ' dBm' }
  return { cls: 'bad', txt: dbm + ' dBm' }
}
