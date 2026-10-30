// Minimal i18n. Source strings in the code are ENGLISH (the default); German lives in dictionaries.
//   t('Add device')                      -> UI text
//   t('{n} devices', { n: 3 })           -> with placeholders
//   tr(serverText)                       -> text produced by the Go backend (English); translated via templates
import { de } from './i18n/de.js'
import server from './i18n/server.js'
import v07 from './i18n/v07_server.js'
import v014 from './i18n/v014_server.js'
import v015 from './i18n/v015_server.js'
// files i18n/x_srv_*.js (one per feature) are picked up automatically
const extraSrv = Object.assign({}, ...Object.values(import.meta.glob('./i18n/x_srv_*.js', { eager: true, import: 'default' })))
const srv = { ...server, ...v07, ...v014, ...v015, ...extraSrv }

function load() {
  try { const v = localStorage.getItem('mtmon.lang'); if (v === 'de' || v === 'en') return v } catch {}
  return 'en'
}
let lang = $state(load())

export const i18n = {
  get lang() { return lang },
  set(l) { lang = l === 'de' ? 'de' : 'en'; try { localStorage.setItem('mtmon.lang', lang) } catch {}; document.documentElement.lang = lang },
  get locale() { return lang === 'de' ? 'de-DE' : 'en-GB' },
}
if (typeof document !== 'undefined') document.documentElement.lang = lang

const fill = (s, v) => (v ? s.replace(/\{(\w+)\}/g, (m, k) => (v[k] ?? m)) : s)

export function t(key, vars) {
  return fill(lang === 'de' ? (de[key] ?? key) : key, vars)
}

// Plural: two separate keys (one / many), each with its own German text; {n} is filled in automatically.
export function tn(one, many, n, vars) {
  return t(n === 1 ? one : many, { n, ...vars })
}

// Backend strings: exact match first, then templates such as "Enabled logging on {n} firewall rules".
let tpl = null
function templates() {
  if (tpl) return tpl
  tpl = Object.keys(srv).filter(k => k.includes('{')).map(k => {
    const names = []
    const re = new RegExp('^' + k.replace(/[.*+?^$()|[\]\\]/g, '\\$&').replace(/\{(\w+)\}/g, (m, n) => { names.push(n); return '(.+?)' }) + '$', 's')
    return { re, names, to: srv[k] }
  })
  return tpl
}
const MAINT = ' (maintenance window)'
const SELF = ' (made through mtmon)'
const BYLIST = 'changed by: ' // config_changed alert: sources of the change, separated by "; "
export function tr(s) {
  if (lang !== 'de' || !s) return s
  if (s.endsWith(SELF)) return tr(s.slice(0, -SELF.length)) + ' (über mtmon vorgenommen)'
  if (s.startsWith(BYLIST)) return 'geändert durch: ' + s.slice(BYLIST.length).split('; ').map(tr).join('; ')
  if (s.endsWith(MAINT)) return tr(s.slice(0, -MAINT.length)) + ' (Wartungsfenster)' // appended by the alert engine
  if (de[s]) return de[s]
  if (srv[s]) return srv[s]
  for (const { re, names, to } of templates()) {
    const m = re.exec(s)
    if (m) return fill(to, Object.fromEntries(names.map((n, i) => [n, m[i + 1]])))
  }
  // messages that grew notes after the fact ("… – resolved: back after 2m – the device rebooted …"): translate the parts
  if (s.includes(' – ')) {
    const parts = s.split(' – ')
    const out = parts.map(p => tr(p))
    if (out.some((p, i) => p !== parts[i])) return out.join(' – ')
  }
  return s
}
