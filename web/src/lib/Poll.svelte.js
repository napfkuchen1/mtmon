import { onAuthError } from './state.svelte.js'

// Reactive polling helper: const d = poll(() => api('/x'), 5000); d.data / d.error / d.loading
export function poll(fn, ms = 5000) {
  const s = $state({ data: null, error: '', loading: true })
  $effect(() => {
    let stop = false, t
    const run = async () => {
      try { s.data = await fn(); s.error = '' }
      catch (e) { if (!onAuthError(e)) s.error = e.message }
      s.loading = false
      if (!stop && ms) t = setTimeout(run, ms)
    }
    run()
    return () => { stop = true; clearTimeout(t) }
  })
  return s
}
