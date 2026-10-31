// SPDX-License-Identifier: AGPL-3.0-only
//
// Licenses of everything that ends up in the mtmon binary (Go modules) and in the embedded UI (npm runtime packages).
//   node scripts/licenses.js --check          fail when a dependency is not under an AGPL-compatible permissive licence
//   node scripts/licenses.js --out FILE       write THIRD_PARTY_NOTICES (package, version, licence, full licence text)
// Go modules need the module cache (go build/mod download), npm packages need `npm ci` in web/.
const { execFileSync } = require('node:child_process')
const fs = require('node:fs')
const path = require('node:path')

const ALLOWED = new Set(['MIT', 'BSD-2-Clause', 'BSD-3-Clause', 'Apache-2.0', 'ISC', 'MPL-2.0', '0BSD', 'OFL-1.1', 'Unlicense', 'CC0-1.0'])
// Runtime parts of the UI: everything else in web/package.json is build tooling and not shipped.
const UI_ROOTS = ['country-flag-icons', 'qrcode-generator', 'topojson-client', 'world-atlas', 'svelte', '@fontsource-variable/inter']

const args = process.argv.slice(2)
const out = args.includes('--out') ? args[args.indexOf('--out') + 1] : ''
const root = path.resolve(__dirname, '..')

function sniff(text) {
  const t = text.replace(/\s+/g, ' ')
  if (/Apache License,? Version 2\.0/i.test(t)) return 'Apache-2.0'
  if (/Mozilla Public License,? (Version|v\.?) 2\.0/i.test(t)) return 'MPL-2.0'
  if (/SIL OPEN FONT LICENSE Version 1\.1/i.test(t)) return 'OFL-1.1'
  if (/Redistribution and use in source and binary forms/i.test(t)) return /Neither the name|nor the names of/i.test(t) ? 'BSD-3-Clause' : 'BSD-2-Clause'
  if (/Permission is hereby granted, free of charge/i.test(t)) return 'MIT'
  if (/Permission to use, copy, modify, and\/or distribute this software for any purpose with or without fee/i.test(t)) return 'ISC'
  if (/This is free and unencumbered software released into the public domain/i.test(t)) return 'Unlicense'
  return 'UNKNOWN'
}

function licenseFile(dir) {
  const f = fs.readdirSync(dir).find(n => /^(LICEN[CS]E|COPYING|UNLICEN[CS]E)(\.\w+)?$/i.test(n))
  return f ? fs.readFileSync(path.join(dir, f), 'utf8') : ''
}

const pkgs = []

// Go: the modules the main binary is actually linked from
const goDeps = execFileSync('go', ['list', '-deps', '-f', '{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}', './cmd/mtmon'],
  { cwd: root, encoding: 'utf8', maxBuffer: 1 << 26 }).split('\n').filter(Boolean)
for (const line of [...new Set(goDeps)].sort()) {
  const [name, version, dir] = line.split('\t')
  const text = licenseFile(dir)
  pkgs.push({ name, version, kind: 'go', license: sniff(text), text })
}

// npm: runtime roots and their transitive dependencies
const nm = path.join(root, 'web', 'node_modules')
const seen = new Set()
const walk = name => {
  if (seen.has(name)) return
  seen.add(name)
  const dir = path.join(nm, name)
  if (!fs.existsSync(dir)) throw new Error(`${name} missing in web/node_modules (run npm ci in web/)`)
  const pj = JSON.parse(fs.readFileSync(path.join(dir, 'package.json'), 'utf8'))
  const lic = typeof pj.license === 'string' ? pj.license : pj.license?.type || ''
  const text = licenseFile(dir)
  pkgs.push({ name, version: pj.version, kind: 'npm', license: lic || sniff(text), text })
  for (const d of Object.keys(pj.dependencies || {})) walk(d)
}
UI_ROOTS.forEach(walk)

const ok = l => l.split(/\s+(?:OR|AND)\s+|[()]/).map(s => s.trim()).filter(Boolean).some(s => ALLOWED.has(s))
const bad = pkgs.filter(p => !ok(p.license))
for (const p of bad) console.error(`licence not allowed: ${p.kind} ${p.name}@${p.version}: ${p.license}`)
if (bad.length) process.exit(1)

if (out) {
  const parts = ['# Third-party notices\n\nmtmon is licensed under AGPL-3.0-only (see LICENSE). It includes the following third-party software.\n']
  for (const p of pkgs.sort((a, b) => a.name.localeCompare(b.name))) {
    parts.push(`\n---\n\n## ${p.name} ${p.version} (${p.license})\n\n${p.text.trim() || '(no licence text shipped by the package; licence taken from its metadata)'}\n`)
  }
  fs.writeFileSync(out, parts.join(''))
  console.log(`${pkgs.length} packages -> ${out}`)
} else {
  console.log(`${pkgs.length} packages, all licences allowed`)
}
