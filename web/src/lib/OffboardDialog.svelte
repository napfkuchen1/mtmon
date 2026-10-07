<script>
  import { api } from './api.js'
  import { t, tr } from './i18n.svelte.js'
  import { toast } from './state.svelte.js'
  import Modal from './Modal.svelte'
  // Remove a UI-managed device. "Undo & remove" first reverts every change mtmon made on the router (needs admin
  // credentials once, never stored); "Just forget" only removes it from mtmon.
  let { name, managed = false, onclose, ondone } = $props()
  let man = $state([]), user = $state('admin'), pass = $state(''), purge = $state(false), busy = $state(false), res = $state(null), err = $state('')
  api('/devices/' + encodeURIComponent(name) + '/manifest').then(m => (man = m || [])).catch(() => {})
  async function go(mode) {
    busy = true; err = ''; res = null
    try {
      res = await api('/devices/' + encodeURIComponent(name) + '/offboard', { method: 'POST', body: { user, pass, mode, purge } })
      pass = ''
      if (res.ok) { toast(t('Device removed')); ondone?.(); setTimeout(onclose, 900) }
    } catch (e) { err = e.message } finally { busy = false }
  }
</script>
<Modal title={t('Offboarding: {name}', { name })} {onclose}>
  <p style="margin-top:0">{t('What happens:')}</p>
  <ol class="steps">
    <li>{t('mtmon signs in to the router')} <b>{t('once')}</b> {t('with admin credentials (not stored).')}</li>
    <li>{t('All {n} recorded changes are undone in', { n: man.length })} <b>{t('reverse order')}</b> {t('(traffic flow target, syslog, firewall rule log settings back to the old value, read-only user, group).')}</li>
    <li>{t('Objects that have been changed by someone else in the meantime are')} <b>{t('not')}</b> {t('touched and are reported.')}</li>
    <li>{t('Afterwards the device is removed from mtmon. The collected statistics are kept (unless you choose to delete them).')}</li>
  </ol>
  {#if managed}
    <div class="row"><label>{t('Admin user')}<input class="input" bind:value={user} autocomplete="off" /></label><label>{t('Password')}<input class="input" type="password" bind:value={pass} autocomplete="new-password" /></label></div>
  {/if}
  <label class="chk"><input type="checkbox" bind:checked={purge} /> {t('Also delete this device’s stored metrics/firewall events')}</label>
  {#if err}<div class="warnbox" style="margin-top:12px">{tr(err)}</div>{/if}
  {#if res}
    <div class="warnbox" style="margin-top:12px;background:{res.ok ? 'var(--ok-bg)' : 'var(--bad-bg)'}">
      {res.ok ? t('Everything undone.') : t('Not everything could be undone – the device stays registered. Retry or run the commands manually.')}
      {#each res.rollback || [] as s}<div style="font-size:12.5px"><span class="badge {s.ok ? 'ok' : 'bad'}">{s.ok ? '✓' : '✗'}</span> {tr(s.title)}{s.msg ? ' – ' + tr(s.msg) : ''}</div>{/each}
    </div>
    {#if res.manual_script}<pre class="code" style="margin-top:10px">{res.manual_script}</pre>{/if}
  {/if}
  {#snippet footer()}
    <button class="btn" onclick={onclose}>{t('Cancel')}</button>
    <button class="btn" disabled={busy} title={t('Removes the device from mtmon only; the router stays as it is')} onclick={() => go('forget')}>{t('Just forget')}</button>
    <button class="btn primary" disabled={busy || (managed && (!user || !pass))} onclick={() => go('rollback')}>{busy ? t('Undoing …') : t('Undo & remove')}</button>
  {/snippet}
</Modal>
<style>
  .steps { padding-left: 20px; display: grid; gap: 6px; margin: 0 0 14px; } .row { display: flex; gap: 12px; margin-bottom: 12px; flex-wrap: wrap; } .row label { flex: 1; min-width: 160px; display: grid; gap: 4px; font-weight: 500; font-size: 13px; }
  label.chk { display: flex; gap: 8px; align-items: center; }
</style>
