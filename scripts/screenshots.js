// SPDX-License-Identifier: AGPL-3.0-only
// Regenerates docs/img/*.png from the demo (scripts/demo.sh must be running on 127.0.0.1:18443; `make screenshots` does it all).
// Needs Playwright with Chromium (npm i -g playwright, or the one preinstalled in the cloud image).
// Dark theme at 1400 px, like the default UI most users see. Pages that need hardware (the Add-device wizard against a
// real router) are not part of this: docs/img/wizard-plan.png is made by hand, and so is docs/img/firewall.png (the demo
// has no firewall log, the page would be empty).
const { chromium } = require('playwright')
const path = require('path')

const BASE = process.env.MTMON_URL || 'http://127.0.0.1:18443'
const OUT = path.join(__dirname, '..', 'docs', 'img')

;(async () => {
  const b = await chromium.launch()
  const ctx = await b.newContext({ viewport: { width: 1400, height: 900 }, colorScheme: 'dark', deviceScaleFactor: 1 })
  const p = await ctx.newPage()
  await p.addInitScript(() => { try { localStorage.setItem('mtmon.lang', 'en') } catch {} })
  await p.goto(BASE + '/')
  await p.locator('input').first().fill('admin')
  await p.locator('input').nth(1).fill(process.env.MTMON_PASSWORD || 'demo-password-1')
  await p.keyboard.press('Enter')
  // wait until the demo has clients with traffic and names (the first poll takes ~35 s)
  for (let i = 0; i < 60; i++) {
    const ok = await p.evaluate(async () => {
      try {
        const cl = await (await fetch('/api/clients?range=1h')).json()
        const o = await (await fetch('/api/overview?range=1h')).json()
        return cl.filter(x => x.bytes_down > 0).length >= 3 && o.clients_total >= 4 && o.top_clients.length > 0
      } catch { return false }
    })
    if (ok) break
    await p.waitForTimeout(3000)
  }
  await p.waitForTimeout(5000)
  const shots = [
    ['overview', '#/', 'h1'], ['clients', '#/clients', 'tbody tr'], ['live', '#/live', 'h1'], ['devices', '#/devices', 'h1'],
    ['services', '#/services', 'h1'], ['suggestions', '#/suggestions', 'h1'],
    ['topology', '#/topology', 'svg'], ['insights', '#/insights', 'tbody tr'],
  ]
  for (const [name, hash, wait] of shots) {
    await p.goto(BASE + '/' + hash)
    await p.waitForSelector(wait, { timeout: 15000 }).catch(() => {})
    await p.waitForTimeout(2500)
    await p.screenshot({ path: path.join(OUT, name + '.png'), fullPage: name === 'overview' })
    console.log('wrote', name)
  }
  // a Wi-Fi client page and the first device page
  const clients = await p.evaluate(async () => (await (await fetch('/api/clients?range=1h')).json()).map(x => x.client))
  const wifi = clients.find(c => c.wifi) || clients[0]
  if (wifi) {
    await p.goto(BASE + '/#/client/' + encodeURIComponent(wifi.mac))
    await p.waitForTimeout(4000)
    await p.screenshot({ path: path.join(OUT, 'client.png'), fullPage: false })
    console.log('wrote client')
    await p.locator('h2', { hasText: /^Connections$/ }).first().evaluate(e => e.scrollIntoView({ block: 'start' })).catch(() => {})
    await p.waitForTimeout(1200)
    await p.screenshot({ path: path.join(OUT, 'client-connections.png') })
    console.log('wrote client-connections')
  }
  // the device page that shows most: the one with the most clients, once it has a few minutes of CPU/RAM history
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
    await p.screenshot({ path: path.join(OUT, 'device-detail.png') })
    console.log('wrote device-detail', best)
  }
  await b.close()
})().catch(e => { console.error(e); process.exit(1) })
