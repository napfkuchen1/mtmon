<script>
  // Settings -> Two-factor login (#192): authenticator app (TOTP, RFC 6238) plus ten single-use recovery codes.
  import qrcode from 'qrcode-generator'
  import { api } from './api.js'
  import { toast } from './state.svelte.js'
  import { t, tr } from './i18n.svelte.js'
  let st = $state({ enabled: false, recovery_left: 0 })
  let setup = $state(null) // { secret, uri, svg }
  let code = $state('')
  let pw = $state('')
  let recovery = $state([])
  let busy = $state(false)
  async function load() { try { st = await api('/totp') } catch (e) { toast(tr(e.message)) } }
  load()
  async function start() {
    busy = true
    try {
      const r = await api('/totp/setup', { method: 'POST' })
      const q = qrcode(0, 'M'); q.addData(r.uri); q.make()
      setup = { ...r, svg: q.createSvgTag({ cellSize: 4, margin: 2, scalable: true }) }
      code = ''
    } catch (e) { toast(tr(e.message)) }
    busy = false
  }
  async function enable() {
    busy = true
    try {
      const r = await api('/totp/enable', { method: 'POST', body: { code } })
      recovery = r.recovery; setup = null; code = ''
      await load()
    } catch (e) { toast(tr(e.message)) }
    busy = false
  }
  async function disable() {
    busy = true
    try {
      await api('/totp/disable', { method: 'POST', body: { password: pw, code } })
      pw = ''; code = ''; recovery = []
      await load()
    } catch (e) { toast(tr(e.message)) }
    busy = false
  }
</script>
<div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Two-factor login')}</h2>
  <span class="badge {st.enabled ? 'ok' : ''}">{st.enabled ? t('On') : t('Off')}</span></div>
  <div class="card-b muted" style="font-size:13px">{t('Sign-in then needs a code from an authenticator app as well as the password. Actions that use a router admin login (adding or removing devices, applying fixes) ask for a fresh code too.')}</div>
  {#if recovery.length}
    <div class="card-b">
      <b>{t('Recovery codes')}</b>
      <div class="muted" style="font-size:13px">{t('Each code works once. Keep them somewhere safe – they are not shown again.')}</div>
      <pre class="mono" style="margin:8px 0">{recovery.join('\n')}</pre>
      <button class="btn sm" onclick={() => (recovery = [])}>{t('I saved them')}</button>
    </div>
  {:else if setup}
    <div class="card-b" style="display:flex;gap:16px;flex-wrap:wrap;align-items:flex-start">
      <div style="width:180px;background:#fff;padding:4px;border-radius:6px">{@html setup.svg}</div>
      <div style="flex:1;min-width:220px">
        <div class="muted" style="font-size:13px">{t('Scan the code with an authenticator app, or enter this key by hand:')}</div>
        <div class="mono" style="margin:6px 0;word-break:break-all">{setup.secret}</div>
        <label style="display:flex;flex-direction:column;gap:4px">{t('6-digit code')}
          <input class="input" inputmode="numeric" autocomplete="one-time-code" bind:value={code} style="max-width:160px" /></label>
        <div class="row" style="gap:8px;margin-top:10px">
          <button class="btn primary" disabled={busy || code.trim().length < 6} onclick={enable}>{t('Turn on')}</button>
          <button class="btn" onclick={() => (setup = null)}>{t('Cancel')}</button>
        </div>
      </div>
    </div>
  {:else if st.enabled}
    <div class="card-b">
      <div class="muted" style="font-size:13px">{t('{n} recovery codes left.', { n: st.recovery_left })}</div>
      <div class="row" style="gap:8px;flex-wrap:wrap;margin-top:8px">
        <input class="input" type="password" placeholder={t('Password')} autocomplete="current-password" bind:value={pw} style="max-width:200px" />
        <input class="input" inputmode="numeric" placeholder={t('6-digit code')} autocomplete="one-time-code" bind:value={code} style="max-width:160px" />
        <button class="btn" disabled={busy || !pw || !code.trim()} onclick={disable}>{t('Turn off')}</button>
      </div>
    </div>
  {:else}
    <div class="card-b"><button class="btn primary" disabled={busy} onclick={start}>{t('Set up')}</button></div>
  {/if}
</div>
