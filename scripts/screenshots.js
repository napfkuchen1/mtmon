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
const { BASE, newContext, login, waitForDemo, checker } = require('./demo-common.js')

const OUT = path.join(__dirname, '..', 'docs', 'img')

;(async () => {
  const b = await chromium.launch()
  const ctx = await newContext(b, { viewport: { width: 1400, height: 900 } })
  const p = await ctx.newPage()
  const verify = checker(p)
  const shot = async (name, opts = {}) => {
    await verify(name, name === 'overview')
    await p.screenshot({ path: path.join(OUT, name + '.png'), ...opts })
    console.log('wrote', name)
  }

  await login(p)
  await waitForDemo(p)
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
