<script>
  import { app, checkSession } from '../lib/state.svelte.js'
  import { api } from '../lib/api.js'
  let user = $state('admin'), password = $state(''), err = $state(''), busy = $state(false)
  async function submit(e) {
    e.preventDefault(); busy = true; err = ''
    try { await api('/login', { method: 'POST', body: { user, password } }); await checkSession() }
    catch (ex) { err = ex.message } finally { busy = false }
  }
</script>

<div class="wrap">
  <form class="card" onsubmit={submit}>
    <div class="logo">
      <svg width="34" height="34" viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="7" fill="var(--accent)"/><path d="M6 21l5-7 4 4 5-9 6 12" stroke="#fff" stroke-width="2.6" fill="none" stroke-linecap="round" stroke-linejoin="round"/></svg>
      <h1>Sign in to mtmon</h1>
    </div>
    <label>Username<input class="input" bind:value={user} autocomplete="username" required /></label>
    <label>Password<input class="input" type="password" bind:value={password} autocomplete="current-password" required /></label>
    {#if err}<div class="badge bad" role="alert">{err}</div>{/if}
    <button class="btn primary" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
  </form>
</div>

<style>
  .wrap { min-height: 100vh; display: grid; place-items: center; padding: 20px; }
  form { width: 100%; max-width: 380px; padding: 28px; display: flex; flex-direction: column; gap: 16px; }
  .logo { display: flex; align-items: center; gap: 12px; margin-bottom: 4px; }
  .logo h1 { font-size: 19px; }
  label { display: flex; flex-direction: column; gap: 6px; font-weight: 500; }
  .btn { justify-content: center; padding: 9px 12px; }
</style>
