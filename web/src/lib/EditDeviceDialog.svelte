<script>
  import { api } from './api.js'
  import { t, tr } from './i18n.svelte.js'
  import { toast } from './state.svelte.js'
  import Modal from './Modal.svelte'
  import DeviceFeatures from './DeviceFeatures.svelte'
  // Change how mtmon reaches a device (address, port, role, site, credentials). The new settings are tested first.
  // Nothing on the router is changed.
  let { device, onclose, ondone } = $props()
  let tab = $state('conn')  // conn | features
  let addr = $state(device.addr), port = $state(device.port || 443), role = $state(device.role || 'router'), site = $state(device.site || ''),
    user = $state(''), pass = $state(''), busy = $state(false), err = $state('')
  async function save() {
    busy = true; err = ''
    try {
      await api('/devices/' + encodeURIComponent(device.name), { method: 'PUT', body: { addr, port: Number(port), scheme: device.scheme, role, site, user, pass } })
      toast(t('Saved')); ondone?.(); onclose()
    } catch (e) { err = e.message } finally { busy = false }
  }
</script>
<Modal title={t('Edit device: {name}', { name: device.name })} wide={tab === 'features'} {onclose}>
  <div class="tabs" role="tablist" style="margin-bottom:14px">
    <button role="tab" class:on={tab === 'conn'} aria-selected={tab === 'conn'} onclick={() => (tab = 'conn')}>{t('Connection')}</button>
    <button role="tab" class:on={tab === 'features'} aria-selected={tab === 'features'} onclick={() => (tab = 'features')}>{t('Router features')}</button>
  </div>
  {#if tab === 'features'}
    <DeviceFeatures name={device.name} onchanged={ondone} />
  {:else}
  <p class="muted" style="margin-top:0">{t('mtmon tests the new settings against the device before saving. Nothing on the router is changed. Leave user and password empty to keep the stored ones.')}</p>
  <div class="grid2">
    <label>{t('Address')}<input class="input" bind:value={addr} /></label>
    <label>{t('Port')}<input class="input" type="number" min="1" max="65535" bind:value={port} /></label>
    <label>{t('Role')}<select class="input" bind:value={role}><option value="router">router</option><option value="ap">ap</option><option value="switch">switch</option></select></label>
    <label>{t('Site')}<input class="input" bind:value={site} placeholder={t('optional')} /></label>
    <label>{t('User')}<input class="input" bind:value={user} autocomplete="off" placeholder={t('unchanged')} /></label>
    <label>{t('Password')}<input class="input" type="password" bind:value={pass} autocomplete="new-password" placeholder={t('unchanged')} /></label>
  </div>
  {#if err}<div class="warnbox" style="margin-top:12px">{tr(err)}</div>{/if}
  {/if}
  {#snippet footer()}
    <button class="btn" onclick={onclose}>{tab === 'features' ? t('Close') : t('Cancel')}</button>
    {#if tab === 'conn'}<button class="btn primary" disabled={busy || !addr} onclick={save}>{busy ? t('Testing …') : t('Test & save')}</button>{/if}
  {/snippet}
</Modal>
<style>
  .grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; } label { display: grid; gap: 4px; font-weight: 500; font-size: 13px; }
  @media (max-width: 560px) { .grid2 { grid-template-columns: 1fr; } }
</style>
