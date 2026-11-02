// SPDX-License-Identifier: AGPL-3.0-only
//
// Stored alert texts (written by older mtmon versions, so they cannot be re-generated) must render in the German UI
// either fully German or fully English – never English with German pieces in the middle (#195).
//   node scripts/alert-i18n.test.js
// i18n.svelte.js uses a rune and Vite's glob import; both are replaced here so Node can load it.
const fs = require('node:fs')
const path = require('node:path')
const assert = require('node:assert')

const lib = path.join(__dirname, '..', 'web', 'src', 'lib')
const tmps = []
// copy of a module in which the Vite glob import is a list of plain imports
function patch(file, dir, pattern, glob) {
  const names = fs.readdirSync(path.join(lib, 'i18n')).filter(f => pattern.test(f))
  let src = fs.readFileSync(path.join(lib, file), 'utf8')
  src = src.replace(/Object\.assign\(\{\}, \.\.\.Object\.values\(import\.meta\.glob\([^\n]*\)\)\)/, `Object.assign({}, ${names.map((_, i) => 'x' + i).join(', ')})`)
  src = names.map((f, i) => `import x${i} from '${dir}${f}'\n`).join('') + src.replace('$state(load())', 'load()')
  if (src.includes('import.meta.glob') || src.includes('$state')) throw new Error(file + ' changed shape; update this test')
  return src
}
const deTmp = path.join(lib, 'i18n', '_de_node_test.mjs')
const tmp = path.join(lib, '_i18n_node_test.mjs')
fs.writeFileSync(deTmp, patch('i18n/de.js', './', /^x_ui_.*\.js$/))
fs.writeFileSync(tmp, patch('i18n.svelte.js', './i18n/', /^x_srv_.*\.js$/).replace("'./i18n/de.js'", "'./i18n/_de_node_test.mjs'"))
tmps.push(deTmp, tmp)

;(async () => {
  global.document = { documentElement: {} }
  try {
    const { i18n, trAlert, tr } = await import(require('node:url').pathToFileURL(tmp).href)
    i18n.set('de')
    const cc1 = '{dev}: configuration changed (1 entry) – see the device page, Config history'
    const down = '{root} is unreachable ({r}); {deps} unreachable too because their uplink runs through {root}'
    const good = 'The signal is good, so this is probably not a coverage problem; check access-list reject rules for this client and roaming settings'
    const cases = [
      // config_changed of 0.20.8: key without the "changed by" slot, sources appended behind the base text
      { msg: 'ap1: configuration changed (1 entry) – see the device page, Config history – changed by: admin; claude via SSH from 192.0.2.1; script',
        msgkey: cc1, params: { dev: 'ap1' }, german: true },
      // same text of an alert that has no key at all (older versions)
      { msg: 'ap1: configuration changed (1 entry) – see the device page, Config history – changed by: admin; claude via SSH from 192.0.2.1; script', german: true },
      // wifi_disconnects of 0.20.6/0.20.7: the hint sits inside the quoted router reason
      { msg: 'Client phone keeps disconnecting from ap1: 7 times between 02:00 and 03:00 – last reason: connection lost, signal strength -52. ' + good,
        msgkey: 'Client {who} keeps disconnecting from {where}: {n} times between {first} and {last}',
        params: { who: 'phone', where: 'ap1', n: '7', first: '02:00', last: '03:00' }, german: true },
      // device_down / devices_unreachable with the uplink note: must not be replaced word by word
      { msg: 'sw1 is unreachable (i/o timeout); ap1, ap2 unreachable too because their uplink runs through sw1', german: true },
      { msg: 'sw1 is unreachable (i/o timeout); ap1, ap2 unreachable too because their uplink runs through sw1', msgkey: down,
        params: { root: 'sw1', r: 'i/o timeout', deps: 'ap1, ap2' }, german: true },
    ]
    for (const c of cases) {
      const out = trAlert({ msgkey: '', params: null, ...c })
      assert.notStrictEqual(out, c.msg, 'not translated: ' + c.msg)
      assert(!/\b(runs through|probably|changed by|via SSH|reject rules|unreachable too)\b/.test(out), 'English left in: ' + out)
      assert(!/läuft mit through|per SSH von .*(via|from)/.test(out), 'mixed text: ' + out)
      console.log('ok  ' + out)
    }
    // suggestion texts (#195): rendered sentences with names and numbers must translate through the templates
    const sugg = [
      ['Living-TV keeps hopping although ap-eg is clearly stronger', 'wechselt ständig'],
      ['Living-TV stays on two access points and ap-eg is 18 dB stronger on average, yet it keeps switching. A fixed device like this is best pinned to ap-eg.', 'ist im Mittel 18 dB stärker'],
      ['Pin the device to ap-eg: add an access-list rule that rejects it on the ap-og Wi-Fi interface (or set a minimum signal strength there).', 'Gerät an ap-eg binden'],
      ['Optional: block port 853 so devices fall back to plain DNS, which the router can redirect. Put the rule above any FastTrack or accept-all rules.', 'Port 853 sperren'],
      ['3 rules on ap1 point to deleted interfaces', 'verweisen auf gelöschte'],
      // evidence lines (#195)
      ['1 clients sent DNS traffic (port 53) to public servers in the last 24 h', 'Clients: 1'],
      ['About 120 debug log lines per hour, from the topics: dhcp, wireless', 'Debug-Logzeilen'],
      ['3 clients sent port 853 traffic to public servers in the last 24 h', 'Port-853-Verkehr'],
      ['2 clients sent port 53 traffic to public servers; the redirect rule on the router has matched packets', 'Umleitungsregel'],
      ['Stable: +0.1 points between the first and second half of the window, at least 28 MiB free', 'Stabil'],
      ['Only 12 MiB of RAM stayed free (limit 20 MiB)', 'MiB'],
      ['Memory use is rising: +6.2 points between the first and second half of the window', 'steigt'],
      ['Default peer AA:BB:CC:DD:EE:FF (tunnel address 10.0.0.2) on rb1 has connected', 'Standard-Peer'],
    ]
    // {ver} is one word (CodeQL: the escape in a plain string turned "\\s" into the letter "s")
    sugg.push(['rb1 runs 7.15.3-testing', 'läuft mit 7.15.3-testing'], ['rb1 runs v1.0.0-rc.s1', 'läuft mit v1.0.0-rc.s1'])
    const twoWords = tr('rb1 runs 7.15 beta')
    assert(!twoWords.includes('läuft'), 'a version is one word, got: ' + twoWords)
    for (const [en, de] of sugg) {
      const out = tr(en)
      assert(out.includes(de), 'suggestion not translated: ' + en + ' -> ' + out)
      console.log('ok  ' + out)
    }
  } finally {
    for (const f of tmps) fs.unlinkSync(f)
  }
})().catch(e => { console.error(e.message); process.exit(1) })
