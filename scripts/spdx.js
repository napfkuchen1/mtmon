// SPDX-License-Identifier: AGPL-3.0-only
//
// Keeps the SPDX license line at the top of every tracked source file.
//   node scripts/spdx.js          report files without the line (exit 1 if any)
//   node scripts/spdx.js --write  add the line, keeping each file's line endings
const { execFileSync } = require('node:child_process')
const fs = require('node:fs')

const ID = 'AGPL-3.0-only'
const TAG = 'SPDX-License-Identifier'
const write = process.argv.includes('--write')

const files = execFileSync('git', ['ls-files', '-z'], { encoding: 'utf8', maxBuffer: 1 << 26 })
  .split('\0')
  .filter(f => /\.(go|js|svelte|sh)$/.test(f) && fs.existsSync(f))

let missing = 0
for (const f of files) {
  const src = fs.readFileSync(f, 'utf8')
  if (src.slice(0, 600).includes(TAG)) continue
  missing++
  if (!write) {
    console.log('missing SPDX line: ' + f)
    continue
  }
  const nl = src.includes('\r\n') ? '\r\n' : '\n'
  const ext = f.slice(f.lastIndexOf('.') + 1)
  let head = src.startsWith('﻿') ? '﻿' : ''
  let body = src.slice(head.length)
  let line, gap = ''
  if (ext === 'go') { line = `// ${TAG}: ${ID}`; gap = nl }
  else if (ext === 'js') line = `// ${TAG}: ${ID}`
  else if (ext === 'svelte') line = `<!-- ${TAG}: ${ID} -->`
  else line = `# ${TAG}: ${ID}`
  if (body.startsWith('#!')) {
    const i = body.indexOf('\n') + 1
    head += body.slice(0, i)
    body = body.slice(i)
  }
  fs.writeFileSync(f, head + line + nl + gap + body)
}
if (missing && !write) process.exit(1)
if (write) console.log(`added to ${missing} files`)
