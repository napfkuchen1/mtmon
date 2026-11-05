// SPDX-License-Identifier: AGPL-3.0-only
// The 2FA enrolment QR code, used the way SecurityCard.svelte uses the library: it must render and scan back to the URI.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import jsQR from 'jsqr'
import qrcode from 'qrcode-generator'

const uri = 'otpauth://totp/mtmon:admin?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=mtmon&algorithm=SHA1&digits=6&period=30'

function make(text) {
  const q = qrcode(0, 'M')
  q.addData(text)
  q.make()
  return q
}

test('renders an SVG tag', () => {
  const svg = make(uri).createSvgTag({ cellSize: 4, margin: 2, scalable: true })
  assert.match(svg, /^<svg[\s>]/)
  assert.match(svg, /viewBox/)
  assert.match(svg, /<path /)
})

test('scans back to the otpauth URI', () => {
  const q = make(uri)
  const cell = 4
  const quiet = 4
  const n = q.getModuleCount()
  const size = (n + 2 * quiet) * cell
  const rgba = new Uint8ClampedArray(size * size * 4).fill(255)
  for (let y = 0; y < n * cell; y++) {
    for (let x = 0; x < n * cell; x++) {
      if (!q.isDark(Math.floor(y / cell), Math.floor(x / cell))) continue
      const at = ((y + quiet * cell) * size + x + quiet * cell) * 4
      rgba[at] = rgba[at + 1] = rgba[at + 2] = 0
    }
  }
  const got = jsQR(rgba, size, size)
  assert.ok(got, 'QR code not readable')
  assert.equal(got.data, uri)
})
