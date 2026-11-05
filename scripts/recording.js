// SPDX-License-Identifier: AGPL-3.0-only
// Records the demo for the README (scripts/demo.sh must be running; `make screenshots SHOTS="screenshots.js recording.js"` does it all):
// Overview -> Clients -> Firewall -> Suggestions, about 18 s, English, dark theme, no wizard. Writes bin/recording/demo.webm; the screenshots
// workflow turns it into docs/img/demo.gif with scripts/make-demo-gif.sh. Same address rewriting and the same text checks as
// scripts/screenshots.js: the run fails when a page would show a loopback address, a "-dirty" version or an empty chart.
const { chromium } = require('playwright')
const fs = require('fs')
const path = require('path')
const { BASE, newContext, login, waitForDemo, checker } = require('./demo-common.js')

const OUT = path.join(__dirname, '..', 'bin', 'recording')
const SIZE = { width: 1280, height: 760 }

;(async () => {
  fs.mkdirSync(OUT, { recursive: true })
  const b = await chromium.launch()
  // first a plain context: sign in, wait until the demo is rich; the recording must not contain the login or the waiting
  const first = await newContext(b, { viewport: SIZE })
  const p0 = await first.newPage()
  await login(p0)
  await waitForDemo(p0)
  const state = await first.storageState()
  await first.close()

  const ctx = await newContext(b, { viewport: SIZE, storageState: state, recordVideo: { dir: OUT, size: SIZE } })
  const p = await ctx.newPage()
  const verify = checker(p)
  const scroll = async (dy, steps = 10) => { for (let i = 0; i < steps; i++) { await p.mouse.wheel(0, dy / steps); await p.waitForTimeout(120) } }
  const stops = [
    ['overview', '#/', 'svg', 2800, async () => scroll(380)],
    ['clients', '#/clients', 'tbody tr', 2800, async () => scroll(300)],
    ['firewall', '#/firewall', 'tbody tr', 2800, async () => scroll(380)],
    ['suggestions', '#/suggestions', 'h1', 2800, async () => scroll(300)],
  ]
  await p.goto(BASE + '/')
  for (const [name, hash, wait, dwell, move] of stops) {
    await p.goto(BASE + '/' + hash)
    await p.waitForSelector(wait, { timeout: 15000 })
    await p.waitForTimeout(1200)
    await verify(name, name === 'overview')
    await p.mouse.move(700, 380)
    const t0 = Date.now()
    await move()
    await p.waitForTimeout(Math.max(0, dwell - (Date.now() - t0)))
    await verify(name + ' (end)', false)
  }
  const video = p.video()
  await ctx.close()
  await video.saveAs(path.join(OUT, 'demo.webm'))
  await b.close()
  console.log('wrote bin/recording/demo.webm')
})().catch(e => { console.error(e); process.exit(1) })
