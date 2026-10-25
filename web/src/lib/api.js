import { tr } from './i18n.svelte.js'
export class AuthError extends Error {}

export async function api(path, opts = {}) {
  const res = await fetch('/api' + path, {
    method: opts.method || 'GET',
    headers: opts.body ? { 'Content-Type': 'application/json' } : opts.raw ? { 'Content-Type': 'text/plain' } : {},
    body: opts.body ? JSON.stringify(opts.body) : opts.raw,
    credentials: 'same-origin'
  })
  if (res.status === 401 && path !== '/login') throw new AuthError('unauthorized')
  const data = await res.json().catch(() => ({}))
  if (!res.ok) throw new Error(tr(data.error || res.statusText))
  return data
}

export function download(path) { window.location.href = '/api' + path }
