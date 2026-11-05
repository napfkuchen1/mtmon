// SPDX-License-Identifier: AGPL-3.0-only
// Switches the README's top picture to the freshly recorded demo: `node scripts/readme-media.js gif|mp4`.
// The README keeps a still image (docs/img/overview.png) between the two demo-media markers until a recording exists, so it
// never shows a broken image. The screenshots workflow runs this after scripts/make-demo-gif.sh.
const fs = require('fs')
const path = require('path')

const kind = process.argv[2]
if (kind !== 'gif' && kind !== 'mp4') { console.error('usage: readme-media.js gif|mp4'); process.exit(2) }
const file = path.join(__dirname, '..', 'README.md')
const src = fs.readFileSync(file, 'utf8')
const nl = src.includes('\r\n') ? '\r\n' : '\n'
const re = /<!-- demo-media:start -->[\s\S]*?<!-- demo-media:end -->/
if (!re.test(src)) { console.error('README.md has no demo-media markers'); process.exit(1) }
const inner = kind === 'gif'
  ? '<p align="center"><img src="docs/img/demo.gif" alt="mtmon demo: overview, clients, firewall, suggestions" width="900"></p>'
  : '<p align="center"><a href="docs/img/demo.mp4"><img src="docs/img/demo-poster.png" alt="mtmon demo (click to play the video)" width="900"></a><br><sub>Click the picture to play the 20 second demo video.</sub></p>'
fs.writeFileSync(file, src.replace(re, ['<!-- demo-media:start -->', inner, '<!-- demo-media:end -->'].join(nl)))
console.log('README.md now shows the demo ' + kind)
