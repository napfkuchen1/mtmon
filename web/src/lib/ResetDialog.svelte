<script>
  import { api, download } from './api.js'
  import { t, tr } from './i18n.svelte.js'
  import { toast } from './state.svelte.js'
  import Modal from './Modal.svelte'
  let { full = false, onclose, ondone } = $props()
  let keep = $state(true), word = $state(''), err = $state(''), busy = $state(false)
  const WORD = 'RESET'
  async function run() {
    busy = true; err = ''
    try {
      await api('/reset', { method: 'POST', body: { mode: full ? 'full' : 'data', keep_labels: keep, confirm: WORD } })
      toast(full ? t('Full reset done') : t('History reset done'))
      window.dispatchEvent(new Event('mtmon-inbox-changed'))
      ondone?.(); onclose()
    } catch (e) { err = e.message } finally { busy = false }
  }
</script>
<Modal title={full ? t('Full reset (including devices)') : t('Reset history')} {onclose}>
  {#if full}
    <p class="muted" style="margin-top:0">{t('Removes all history and also every device you added in the web UI, with its stored login, status and change log. Settings, API tokens and your own service rules stay. Devices from config.json stay too.')}</p>
    <div class="warnbox">{t('Changes mtmon made on your routers (flow export, syslog rules) are not undone. Use “Offboarding” on the device first if you want them gone.')}</div>
  {:else}
    <p class="muted" style="margin-top:0">{t('Empties traffic history, clients, alerts, firewall log, metrics and dismissed suggestions so mtmon starts clean. Your devices, settings, API tokens and service rules stay. Clients and traffic reappear as routers report them.')}</p>
    <label class="chk"><input type="checkbox" bind:checked={keep} /> {t('Keep clients that have a label')}</label>
  {/if}
  <label class="fld">{t('Type {w} to confirm', { w: WORD })}
    <input class="input mono" bind:value={word} autocomplete="off" spellcheck="false" placeholder={WORD} /></label>
  {#if err}<div class="warnbox" style="margin-top:12px">{tr(err)}</div>{/if}
  {#snippet footer()}
    <button class="btn" style="margin-right:auto" title={t('Safety copy of all clients (name, MAC, IP, times) before you remove anything')} onclick={() => download('/export/clients?format=xlsx')}>{t('Download list (Excel)')}</button>
    <button class="btn" onclick={onclose}>{t('Cancel')}</button>
    <button class="btn primary" disabled={busy || word !== WORD} onclick={run}>{full ? t('Reset everything') : t('Reset history')}</button>
  {/snippet}
</Modal>
<style>
  .chk { display: flex; gap: 8px; align-items: center; font-size: 13px; font-weight: 500; margin: 12px 0; }
  .fld { display: grid; gap: 4px; font-weight: 500; font-size: 13px; margin-top: 14px; }
</style>
