import { tr } from './i18n.svelte.js'
export class AuthError extends Error {}

export async function api(path, opts = {}) {
  const res = await fetch('/api' + path, {
    method: opts.method || 'GET',
    headers: { ...(opts.body ? { 'Content-Type': 'application/json' } : opts.raw ? { 'Content-Type': 'text/plain' } : {}), ...opts.headers },
    body: opts.body ? JSON.stringify(opts.body) : opts.raw,
    credentials: 'same-origin'
  })
  if (res.status === 401 && path !== '/login') throw new AuthError('unauthorized')
  const data = await res.json().catch(() => ({}))
  if (res.status === 403 && data.totp_required && !opts.headers) { // 2FA is on: router-login actions need a fresh code
    const code = window.prompt(tr('Enter your two-factor code for this action:'))
    if (code) return api(path, { ...opts, headers: { 'X-TOTP-Code': code.trim() } })
  }
  if (!res.ok) { const e = new Error(tr(data.error || res.statusText)); e.data = data; throw e }
  return data
}

export function download(path) { window.location.href = '/api' + path }
