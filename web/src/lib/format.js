import { ICONS } from './icons.js'
import { t, i18n } from './i18n.svelte.js'
const fmtN = (i, n) => (i === 0 || n >= 100 ? Math.round(n) : Math.round(n * 10) / 10).toLocaleString(i18n.locale, { maximumFractionDigits: 1 })
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
export function num(n) { return (Number(n) || 0).toLocaleString(i18n.locale) }
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
  if (s < 5) return t('just now')
  if (s < 60) return t('{n}s ago', { n: s })
  if (s < 3600) return t('{n}m ago', { n: Math.floor(s / 60) })
  if (s < 86400) return t('{n}h ago', { n: Math.floor(s / 3600) })
  return t('{n}d ago', { n: Math.floor(s / 86400) })
}
export function clock(ts) {
  return new Date(ts * 1000).toLocaleTimeString(i18n.locale, { hour12: false })
}
export function datetime(ts) {
  return new Date(ts * 1000).toLocaleString(i18n.locale, { hour12: false, month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
// "DE" -> "Germany" / "Deutschland" (browser locale data); falls back to the code
const regionNames = {}
export function countryName(cc, lang = 'en') {
  if (!cc || cc.length !== 2) return cc || ''
  try { return (regionNames[lang] ||= new Intl.DisplayNames([lang], { type: 'region' })).of(cc.toUpperCase()) || cc } catch { return cc }
}
export const protoName = p => ({ 1: 'ICMP', 6: 'TCP', 17: 'UDP', 47: 'GRE', 50: 'ESP', 58: 'ICMPv6' }[p] || 'P' + p)
// label > hostname > "Vendor AB:CD" (MAC tail) > IP > MAC
export function display(c) {
  if (c.label || c.hostname) return c.label || c.hostname
  const tail = (c.mac || '').slice(-5).toUpperCase()
  if (c.vendor && tail) return (c.vendor.startsWith('Private') ? 'Randomized MAC' : c.vendor) + ' ' + tail
  return c.ip || c.mac
}
export function signalQuality(dbm) {
  if (!dbm) return { cls: '', txt: '—' }
  if (dbm >= -60) return { cls: 'ok', txt: dbm + ' dBm' }
  if (dbm >= -72) return { cls: 'warn', txt: dbm + ' dBm' }
  return { cls: 'bad', txt: dbm + ' dBm' }
}

// Guess an icon for a client from what we know about it (label, hostname, vendor). Order matters: first match wins.
const KINDS = [
  ['printer3d', /bambu|prusa|creality|anycubic|elegoo|\bender[-_ ]?\d|voron|klipper|octoprint|3d[-_ ]?print|\bx2d\b|\b[xpa]1[cps]?\b|\bh2d\b/],
  ['network', /mikrotik|routerboard|ubiquiti|tp-link|\bavm\b|fritz|netgear|zyxel|cisco|aruba|lancom|router|gateway|\bwap\b|\bap[-_ ]|switch.*(poe|port)/],
  ['tv', /\btv\b|bravia|webos|roku|chromecast|fire ?tv|apple-?tv|smarttv|vizio|\bshield\b/],
  ['speaker', /sonos|echo|alexa|homepod|speaker|\bmc-|musiccast|yamaha|bose|nest ?(mini|audio)|google-?home/],
  ['printer', /printer|brother|epson|canon|laserjet|deskjet|officejet|lexmark|\bmfc-/],
  ['camera', /\bcam\b|camera|reolink|hikvision|dahua|wyze|eufy|ipcam|doorbell/],
  ['console', /playstation|\bps[45]\b|xbox|nintendo/],
  ['server', /synology|qnap|proxmox|\bnas\b|server|docker|homeassistant|home-assistant|raspberry|alpine|ubuntu|debian|jellyfin|nginx|plex|pihole|esxi|truenas|unraid|automation|cloudflared|vaultwarden|portainer|\bvm\b|mtmon|immich|myspeed|esphome|nginx|realtek semi|asustek/],
  ['bulb', /\bhue\b|signify|bulb|lamp|light|wled|ikea/],
  ['iot', /esp[-_]|espressif|shelly|tasmota|tuya|sonoff|zigbee|plug|thermostat|dishwasher|neff|bosch|siemens|miele|washer|dryer|texas instruments|amazon tech|\bamazon-/],
  ['tablet', /ipad|tablet|\btab[-_ ]?[as]\d|galaxy[-_ ]?tab|fire[-_ ]?hd/],
  ['watch', /watch|garmin|fitbit|amazfit/],
  ['car', /\btesla\b|\bvw\b|volkswagen|\bbmw\b|\baudi\b|wallbox|go-?e[-_ ]?charger|evcc/],
  ['phone', /iphone|pixel|galaxy|android|oneplus|xiaomi|redmi|huawei|phone|handy|poco|\bsm-|\bs2\d/],
  ['laptop', /laptop|macbook|notebook|thinkpad|surface|dell|lenovo|desktop|\bpc\b|\bhp-|intel|apple|\bmac\b|ieee registration/]
]
export function deviceKind(c) {
  if (c.icon && ICONS[c.icon]) return c.icon // the user's own choice always wins
  const txt = [c.label, c.hostname, c.vendor, c.name].filter(Boolean).join(' ').toLowerCase()
  for (const [kind, re] of KINDS) if (re.test(txt)) return kind
  if (c.wifi && (c.vendor || '').startsWith('Private')) return 'phone' // randomized MAC on Wi-Fi: almost always a phone
  return 'generic'
}
