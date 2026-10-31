<script>
  // Settings -> Read-only account (#192): a second login that can look but not change anything or see settings.
  import { api } from './api.js'
  import { toast } from './state.svelte.js'
  import { t, tr } from './i18n.svelte.js'
  let st = $state({ enabled: false, user: '' })
  let user = $state('')
  let pw = $state('')
  let busy = $state(false)
  async function load() { try { st = await api('/viewer'); user = st.user } catch (e) { toast(tr(e.message)) } }
  load()
  async function save() {
    busy = true
    try { st = await api('/viewer', { method: 'PUT', body: { user, password: pw } }); pw = ''; toast(t('Saved')) } catch (e) { toast(tr(e.message)) }
    busy = false
  }
  async function remove() {
    busy = true
    try { st = await api('/viewer', { method: 'DELETE' }); user = ''; pw = '' } catch (e) { toast(tr(e.message)) }
    busy = false
  }
</script>
<div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Read-only account')}</h2>
  <span class="badge {st.enabled ? 'ok' : ''}">{st.enabled ? t('On') : t('Off')}</span></div>
  <div class="card-b muted" style="font-size:13px">{t('A second login that can look at everything operational but cannot change anything and does not see settings, the audit log or API tokens.')}</div>
  <div class="card-b row" style="gap:8px;flex-wrap:wrap">
    <input class="input" placeholder={t('Username')} autocomplete="off" bind:value={user} style="max-width:180px" />
    <input class="input" type="password" placeholder={st.enabled ? t('New password') : t('Password')} autocomplete="new-password" bind:value={pw} style="max-width:200px" />
    <button class="btn primary" disabled={busy || !user.trim() || pw.length < 10} onclick={save}>{st.enabled ? t('Save') : t('Create')}</button>
    {#if st.enabled}<button class="btn" disabled={busy} onclick={remove}>{t('Remove')}</button>{/if}
  </div>
  <div class="card-b muted" style="font-size:12px">{t('Password: at least 10 characters.')}</div>
</div>
