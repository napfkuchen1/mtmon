// SPDX-License-Identifier: AGPL-3.0-only
// Renders scripts/social-preview.html to docs/social-preview.png (1280x640) with Playwright. Run after scripts/screenshots.js,
// because the picture on the right is the fresh docs/img/overview.png. Needs no demo, only Chromium.
// GitHub has no API for the social preview: the owner uploads the PNG once under Settings -> Social preview.
const { chromium } = require('playwright')
const path = require('path')
const { pathToFileURL } = require('url')

;(async () => {
  const b = await chromium.launch()
  const p = await b.newPage({ viewport: { width: 1280, height: 640 }, deviceScaleFactor: 1 })
  await p.goto(pathToFileURL(path.join(__dirname, 'social-preview.html')).href)
  await p.waitForFunction(() => [...document.images].every(i => i.complete && i.naturalWidth > 0), null, { timeout: 15000 })
  await p.screenshot({ path: path.join(__dirname, '..', 'docs', 'social-preview.png') })
  await b.close()
  console.log('wrote docs/social-preview.png')
})().catch(e => { console.error(e); process.exit(1) })
