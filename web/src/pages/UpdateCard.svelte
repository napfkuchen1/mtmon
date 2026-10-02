<script>
  import { api, AuthError } from '../lib/api.js'
  import { t, tr, i18n } from '../lib/i18n.svelte.js'
  import { app, toast } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { ago } from '../lib/format.js'
  import Modal from '../lib/Modal.svelte'

  const st = poll(() => api('/update'), 6000)
  const s = $derived(st.data)
  let checking = $state(false), confirming = $state(false), waiting = $state(false), err = $state('')
  let root = $state()

  const vlabel = v => (!v || v === 'dev' ? 'dev' : /^\d/.test(v) ? 'v' + v : v)
  const when = ts => (ts ? new Date(ts * 1000).toLocaleString(i18n.locale) : '—')
  const safeUrl = u => (/^https?:\/\//.test(u || '') ? u : '')
  const sleep = ms => new Promise(r => setTimeout(r, ms))
  const busy = $derived(waiting || ['downloading', 'staged', 'restarting'].includes(s?.apply?.state))

  $effect(() => { if (root && app.route.params.id === 'updates') root.scrollIntoView({ block: 'start' }) })

  function refreshShell() { window.dispatchEvent(new Event('mtmon:update')) }

  async function check() {
    checking = true; err = ''
    try { st.data = await api('/update/check', { method: 'POST' }); refreshShell() } catch (e) { err = e.message }
    checking = false
  }

  async function setMode(mode) {
    err = ''
    try { st.data = await api('/update/mode', { method: 'PUT', body: { mode } }); refreshShell(); toast(t('Saved')) } catch (e) { err = e.message }
  }

  // After "Update now" the service exits and systemd restarts it. Sessions live in memory, so the first answer
  // from the new process is usually a 401: that means "back again" and we reload to the sign-in screen.
  async function apply() {
    confirming = false; err = ''
    const before = s.current
    try { st.data = await api('/update/apply', { method: 'POST' }) } catch (e) { err = e.message; return }
    waiting = true
    const deadline = Date.now() + 4 * 60 * 1000
    let down = false
    while (Date.now() < deadline) {
      await sleep(2000)
      try {
        const x = await api('/update')
        st.data = x
        if (x.apply.state === 'failed') { waiting = false; err = tr(x.apply.message); return }
        if (x.current !== before) { toast(t('Updated to {v}', { v: vlabel(x.current) })); await sleep(1200); location.reload(); return }
        if (down && x.apply.state === 'idle') {
          waiting = false
          err = x.last_rollback ? t('The new version did not start and was rolled back automatically.') : t('The service restarted, but the version did not change.')
          return
        }
      } catch (e) {
        if (e instanceof AuthError) { toast(t('mtmon restarted – please sign in again')); await sleep(1200); location.reload(); return }
        down = true
      }
    }
    waiting = false
    err = t('The service did not come back within 4 minutes. Check the container: journalctl -u mtmon')
  }
</script>

<div class="card" id="updates" bind:this={root} style="margin-top:16px">
  <div class="card-h"><h2>{t('Updates')}</h2>
    {#if s}
      {#if s.available}<span class="badge acc">{t('Update available: {v}', { v: vlabel(s.latest) })}</span>
      {:else if !s.comparable}<span class="badge">{t('Development build')}</span>
      {:else if s.latest}<span class="badge ok">{t('Up to date')}</span>{/if}
    {/if}
  </div>
  {#if s}
    <table><tbody>
      <tr><td class="muted">{t('Installed version')}</td><td class="r mono">{vlabel(s.current)}</td></tr>
      <tr><td class="muted">{t('Latest release')}</td><td class="r mono">{s.latest ? vlabel(s.latest) : '—'}{#if safeUrl(s.html_url)}<span class="muted">&ensp;·&ensp;</span><a href={safeUrl(s.html_url)} target="_blank" rel="noopener noreferrer">GitHub</a>{/if}</td></tr>
      <tr><td class="muted">{t('Last check')}</td><td class="r">{s.last_check ? `${ago(s.last_check)} · ${when(s.last_check)}` : t('never')}</td></tr>
      <tr><td class="muted">{t('Source')}</td><td class="r mono">{s.repo}</td></tr>
      <tr><td class="muted"><label for="upd-mode">{t('Update mode')}</label></td><td class="r">
        <select id="upd-mode" class="input sm" value={s.mode} onchange={e => setMode(e.target.value)} disabled={busy}>
          <option value="off">{t('Off')}</option>
          <option value="notify">{t('Notify only')}</option>
          <option value="auto">{t('Install automatically at night')}</option>
        </select></td></tr>
    </tbody></table>
    <div class="card-b" style="display:flex;flex-direction:column;gap:12px;border-top:1px solid var(--border)">
      <div class="muted">
        {#if s.mode === 'off'}{t('No automatic checks. Use “Check now” when you want to look for a new version.')}
        {:else if s.mode === 'auto'}{t('mtmon checks every 6 hours and installs a newer release between 03:00 and 04:00 (container time), once per release.')}
        {:else}{t('mtmon checks GitHub every 6 hours and shows a notice here and in the sidebar. Nothing is installed without your click.')}{/if}
      </div>
      {#if !s.comparable}<div class="muted">{t('This is a development build; updates are never offered for it automatically.')}</div>{/if}
      {#if s.last_error}<div class="warnbox">{t('Update check failed:')} {tr(s.last_error)}</div>{/if}
      {#if s.last_rollback}<div class="warnbox"><b>{t('The last update failed and was rolled back.')}</b>
        {#if s.last_rollback.version}<span class="mono">{vlabel(s.last_rollback.version)}</span>&ensp;·&ensp;{/if}{when(s.last_rollback.at)}<br />{tr(s.last_rollback.reason)}</div>{/if}
      {#if err}<div class="warnbox" role="alert">{err}</div>{/if}
      {#if busy}
        <div class="progress" role="status"><span class="dot pulse"></span>
          {#if s.apply.state === 'downloading'}{t('Downloading and verifying {v} …', { v: vlabel(s.apply.version) })}
          {:else if waiting && (s.apply.state === 'restarting' || s.apply.state === 'staged' || s.apply.state === 'idle')}{t('Restarting mtmon … this page reloads when the service is back.')}
          {:else}{t('Working …')}{/if}</div>
      {/if}
      {#if s.available && !s.can_apply}<div class="muted">{tr(s.cannot_apply)}</div>{/if}
      <div style="display:flex;gap:8px;flex-wrap:wrap">
        <button class="btn" onclick={check} disabled={checking || busy}>{checking ? t('Checking …') : t('Check now')}</button>
        {#if s.available}<button class="btn primary" onclick={() => (confirming = true)} disabled={busy || !s.can_apply}>{t('Update now')}</button>{/if}
      </div>
      {#if s.notes}
        <details class="notes"><summary>{t('Release notes')} {vlabel(s.latest)}</summary><pre class="code wrap">{s.notes}</pre></details>
      {/if}
    </div>
  {:else}
    <div class="card-b muted">{st.error || t('Loading …')}</div>
  {/if}
</div>

{#if confirming && s}
  <Modal title={t('Update to {v}?', { v: vlabel(s.latest) })} onclose={() => (confirming = false)}>
    <p>{t('mtmon downloads {v} from GitHub, verifies its checksum and restarts itself. Monitoring is interrupted for a few seconds, and you have to sign in again.', { v: vlabel(s.latest) })}</p>
    <p>{t('If the new version does not start, the previous version is restored automatically.')}</p>
    <p class="muted">{t('For large version jumps, a snapshot of the container (installer: “update”) is the safest backup.')}</p>
    {#snippet footer()}
      <button class="btn" onclick={() => (confirming = false)}>{t('Cancel')}</button>
      <button class="btn primary" onclick={apply}>{t('Update now')}</button>
    {/snippet}
  </Modal>
{/if}

<style>
  .progress { display: flex; align-items: center; gap: 10px; padding: 10px 14px; border: 1px solid var(--border); border-radius: 8px; background: var(--card-2); }
  details.notes summary { cursor: pointer; font-weight: 500; }
  pre.wrap { white-space: pre-wrap; word-break: break-word; margin-top: 8px; max-height: 360px; overflow: auto; }
</style>
