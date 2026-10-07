// Minimal i18n. Source strings in the code are ENGLISH (the default); German lives in dictionaries.
//   t('Add device')                      -> UI text
//   t('{n} devices', { n: 3 })           -> with placeholders
//   tr(serverText)                       -> text produced by the Go backend (English); translated via templates
import { de } from './i18n/de.js'
import server from './i18n/server.js'
import v07 from './i18n/v07_server.js'
const srv = { ...server, ...v07 }

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
export function tr(s) {
  if (lang !== 'de' || !s) return s
  if (de[s]) return de[s]
  if (srv[s]) return srv[s]
  for (const { re, names, to } of templates()) {
    const m = re.exec(s)
    if (m) return fill(to, Object.fromEntries(names.map((n, i) => [n, m[i + 1]])))
  }
  return s
}
