// SPDX-License-Identifier: AGPL-3.0-only
// Shared by scripts/screenshots.js and scripts/recording.js: the demo (scripts/demo.sh) runs on 127.0.0.1:18443 and its
// mock routers listen on 127.0.0.x. Pictures and recordings show them as 192.168.88.x (the rewriting happens on the way
// to the browser, nothing in mtmon changes), and a capture fails when the page would still show a loopback address, a
// commit-style or "-dirty" version or a half-loaded overview.
const BASE = process.env.MTMON_URL || 'http://127.0.0.1:18443'

// what the mocks really are -> what the pictures show
const SHOW = [
  [/127\.0\.0\.11(?!\d)/g, '192.168.88.1'], [/127\.0\.0\.12(?!\d)/g, '192.168.88.2'], [/127\.0\.0\.13(?!\d)/g, '192.168.88.3'],
  [/127\.0\.0\.14(?!\d)/g, '192.168.88.4'], [/127\.0\.0\.20(?!\d)/g, '192.168.88.5'], [/127\.0\.0\.1(?!\d)/g, '192.168.88.10'],
]
// the wizard sends the shown address back; the server needs the real one
const REAL = [
  [/192\.168\.88\.1(?!\d)/g, '127.0.0.11'], [/192\.168\.88\.2(?!\d)/g, '127.0.0.12'], [/192\.168\.88\.3(?!\d)/g, '127.0.0.13'],
  [/192\.168\.88\.4(?!\d)/g, '127.0.0.14'], [/192\.168\.88\.5(?!\d)/g, '127.0.0.20'], [/192\.168\.88\.10(?!\d)/g, '127.0.0.1'],
]
const swap = (s, table) => table.reduce((t, [re, to]) => t.replace(re, to), s)

// one run, one look: English, dark theme, comfortable density, orange accent, 24 h range
const PREFS = { lang: 'en', theme: 'dark', range: '24h', density: 'comfortable', chart: 'modern', accent: 'orange', own: '0' }

// a browser context whose API answers show the mock addresses as 192.168.88.x
async function newContext(browser, opts = {}) {
  const ctx = await browser.newContext({ colorScheme: 'dark', deviceScaleFactor: 1, locale: 'en-US', ...opts })
  await ctx.route('**/api/**', async route => {
    const req = route.request()
    const u = new URL(req.url())
    const wizard = req.method() === 'POST' && /^\/api\/devices\/(probe|plan|provision|readonly)$/.test(u.pathname)
    const res = await route.fetch(wizard ? { postData: swap(req.postData() || '', REAL) } : {})
    if (!/json/.test(res.headers()['content-type'] || '')) return route.fulfill({ response: res })
    const headers = { ...res.headers() }
    delete headers['content-length']; delete headers['content-encoding']
    await route.fulfill({ status: res.status(), headers, body: swap(await res.text(), SHOW) })
  })
  await ctx.addInitScript(prefs => {
    try { for (const k in prefs) localStorage.setItem('mtmon.' + k, prefs[k]) } catch {}
  }, PREFS)
  return ctx
}

async function login(p) {
  await p.goto(BASE + '/')
  await p.locator('input').first().fill('admin')
  await p.locator('input').nth(1).fill(process.env.MTMON_PASSWORD || 'demo-password-1')
  await p.keyboard.press('Enter')
}

// wait until the demo is "rich": clients with traffic and names (the first poll takes ~35 s), the seeded history, countries,
// blocked firewall events and alerts. Throws when it never gets there, so a thin demo never ends up in a picture.
async function waitForDemo(p, tries = 60) {
  let last = {}
  for (let i = 0; i < tries; i++) {
    last = await p.evaluate(async () => {
      try {
        const j = async u => (await fetch(u)).json()
        const [cl, o, fw, al] = await Promise.all([j('/api/clients?range=1h'), j('/api/overview?range=24h'), j('/api/firewall/events?range=24h&limit=50'), j('/api/alerts')])
        return {
          traffic: cl.filter(x => x.bytes_down > 0).length, clients: o.clients_total, top: o.top_clients.length,
          series: o.series.length, countries: o.top_countries.length, fw: Array.isArray(fw) ? fw.length : 0, alerts: Array.isArray(al) ? al.length : 0,
        }
      } catch { return {} }
    })
    if (last.traffic >= 3 && last.clients >= 20 && last.top > 0 && last.series >= 20 && last.countries >= 3 && last.fw >= 20 && last.alerts >= 3) return last
    await p.waitForTimeout(3000)
  }
  throw new Error('the demo never became rich enough for pictures: ' + JSON.stringify(last))
}

// the text of the rendered page, right now: no loopback address, no commit-style or "-dirty" version, a clean release
// version in the sidebar, and on the overview no empty chart
function checker(p) {
  return async (name, hero) => {
    const bad = await p.evaluate(hero => {
      const t = document.body.innerText
      const out = [/127\.0\.0\.\d+/, /-dirty/, /-g[0-9a-f]{7,}/, ...(hero ? [/Collecting data/i] : [])].map(re => (t.match(re) || [])[0]).filter(Boolean)
      const v = (document.querySelector('.ver .vtxt') || {}).textContent || ''
      if (!/^mtmon v\d+\.\d+\.\d+$/.test(v.trim())) out.push('sidebar version "' + v.trim() + '"')
      return out
    }, !!hero)
    if (bad.length) throw new Error(`capture "${name}" would show: ${bad.join(', ')}`)
  }
}

module.exports = { BASE, SHOW, REAL, swap, newContext, login, waitForDemo, checker }
