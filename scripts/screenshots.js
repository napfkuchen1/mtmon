// SPDX-License-Identifier: AGPL-3.0-only
// Regenerates docs/img/*.png from the demo (scripts/demo.sh must be running on 127.0.0.1:18443; `make screenshots` does it all).
// Needs Playwright with Chromium (npm i -g playwright, or the one preinstalled in the cloud image).
// One run, one look: English, dark theme, comfortable density, orange accent, 24 h range, 1400 px wide. The demo is entirely
// fictional (mockros network, bin/demoseed history, RFC 5737 remote addresses). The mocks listen on 127.0.0.x; this script
// shows them as 192.168.88.x (the rewriting happens on the way to the browser, nothing in mtmon changes), and it fails the
// run when a screenshot would still show a loopback address or a "-dirty" version.
// The Add-device wizard is driven against the spare mock router (rtr-spare, HTTPS) up to the review step; the Firewall page
// is filled by the seeded firewall log.
const { chromium } = require('playwright')
const path = require('path')

const BASE = process.env.MTMON_URL || 'http://127.0.0.1:18443'
const OUT = path.join(__dirname, '..', 'docs', 'img')

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

;(async () => {
  const b = await chromium.launch()
  const ctx = await b.newContext({ viewport: { width: 1400, height: 900 }, colorScheme: 'dark', deviceScaleFactor: 1, locale: 'en-US' })
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
  const p = await ctx.newPage()
  await p.addInitScript(() => {
    try {
      const set = { lang: 'en', theme: 'dark', range: '24h', density: 'comfortable', chart: 'modern', accent: 'orange', own: '0' }
      for (const k in set) localStorage.setItem('mtmon.' + k, set[k])
    } catch {}
  })
  // a picture must not show a loopback address, a commit-style version or a half-loaded page
  const check = async name => {
    const bad = await p.evaluate(hero => {
      const t = document.body.innerText
      return [/127\.0\.0\.\d+/, /-dirty/, /-g[0-9a-f]{7,}/, ...(hero ? [/Collecting data/i] : [])].map(re => (t.match(re) || [])[0]).filter(Boolean)
    }, name === 'overview')
    if (bad.length) throw new Error(`screenshot "${name}" would show: ${bad.join(', ')}`)
  }
  const shot = async (name, opts = {}) => {
    await check(name)
    await p.screenshot({ path: path.join(OUT, name + '.png'), ...opts })
    console.log('wrote', name)
  }

  await p.goto(BASE + '/')
  await p.locator('input').first().fill('admin')
  await p.locator('input').nth(1).fill(process.env.MTMON_PASSWORD || 'demo-password-1')
  await p.keyboard.press('Enter')
  // wait until the demo has clients with traffic and names (the first poll takes ~35 s) and the seeded history is there
  for (let i = 0; i < 60; i++) {
    const ok = await p.evaluate(async () => {
      try {
        const cl = await (await fetch('/api/clients?range=1h')).json()
        const o = await (await fetch('/api/overview?range=24h')).json()
        return cl.filter(x => x.bytes_down > 0).length >= 3 && o.clients_total >= 20 && o.top_clients.length > 0 && o.series.length >= 20 && o.top_countries.length >= 3
      } catch { return false }
    })
    if (ok) break
    await p.waitForTimeout(3000)
  }
  await p.waitForTimeout(5000)
  const shots = [
    ['overview', '#/', '.chart svg, svg'], ['clients', '#/clients', 'tbody tr'], ['live', '#/live', 'h1'], ['devices', '#/devices', 'h1'],
    ['services', '#/services', 'h1'], ['suggestions', '#/suggestions', 'h1'],
    ['topology', '#/topology', 'svg'], ['insights', '#/insights', 'tbody tr'], ['firewall', '#/firewall', 'tbody tr'],
  ]
  for (const [name, hash, wait] of shots) {
    await p.goto(BASE + '/' + hash)
    await p.waitForSelector(wait, { timeout: 15000 }).catch(() => {})
    await p.waitForTimeout(2500)
    await shot(name, { fullPage: name === 'overview' })
  }
  // a Wi-Fi client page and the first device page
  const clients = await p.evaluate(async () => (await (await fetch('/api/clients?range=1h')).json()).map(x => x.client))
  const wifi = clients.find(c => c.wifi) || clients[0]
  if (wifi) {
    await p.goto(BASE + '/#/client/' + encodeURIComponent(wifi.mac))
    await p.waitForTimeout(4000)
    await shot('client')
    await p.locator('h2', { hasText: /^Connections$/ }).first().evaluate(e => e.scrollIntoView({ block: 'start' })).catch(() => {})
    await p.waitForTimeout(1200)
    await shot('client-connections')
  }
  // the device page that shows most: the one with the most clients (the seed gives every device hours of CPU/RAM history)
  const devs = await p.evaluate(async () => (await (await fetch('/api/devices')).json()).map(d => d.name))
  let best = null
  for (let i = 0; i < 80 && devs.length; i++) {
    const info = await p.evaluate(async names => Promise.all(names.map(async n => { const d = await (await fetch('/api/devices/' + encodeURIComponent(n) + '?range=1h')).json(); return { n, clients: (d.clients || []).length, metrics: (d.metrics || []).length } })), devs)
    best = info.sort((x, y) => y.clients - x.clients || y.metrics - x.metrics)[0]
    if (best.metrics >= 4) break
    await p.waitForTimeout(3000)
  }
  if (best) {
    await p.goto(BASE + '/#/device/' + encodeURIComponent(best.n))
    await p.waitForTimeout(3500)
    await shot('device-detail')
    console.log('device-detail is', best.n)
  }
  // the Add-device wizard against the spare mock router, up to the review step with the plan
  await p.goto(BASE + '/#/devices')
  await p.waitForTimeout(1500)
  await p.getByRole('button', { name: /Add device/ }).first().click()
  const dlg = p.locator('.dlg')
  await dlg.getByPlaceholder('192.168.88.1').fill('192.168.88.5')
  await dlg.locator('input[type=number]').fill('9001')
  await dlg.locator('input[type=password]').fill('adminpw')
  await dlg.getByRole('button', { name: /Connect & check/ }).click()
  await dlg.getByText('Matches – trust this device').waitFor({ timeout: 20000 })
  await dlg.getByText('Matches – trust this device').click()
  await p.waitForTimeout(1500)
  // the dialog scrolls inside the page: make the window as tall as the dialog so nothing is cut off (padding is 4vh per side)
  const h = await dlg.evaluate(e => Math.ceil(e.getBoundingClientRect().height))
  await p.setViewportSize({ width: 1400, height: Math.max(900, Math.ceil(h / 0.9) + 8) })
  await p.waitForTimeout(800)
  await shot('wizard-plan')
  await p.setViewportSize({ width: 1400, height: 900 })
  await b.close()
})().catch(e => { console.error(e); process.exit(1) })
