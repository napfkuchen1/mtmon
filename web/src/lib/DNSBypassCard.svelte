<script>
  // Settings: switch for the alert "a client started bypassing the local DNS" (off by default).
  import { api } from './api.js'
  import { toast } from './state.svelte.js'
  import { poll } from './Poll.svelte.js'
  import { t, tr } from './i18n.svelte.js'
  const d = poll(() => api('/dns-bypass'), 30000)
  async function toggle() {
    try { await api('/dns-bypass', { method: 'PUT', body: { alert: !d.data.alert } }); d.data = await api('/dns-bypass'); toast(t('Saved')) } catch (e) { toast(tr(e.message)) }
  }
</script>
{#if d.data}
  <div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('DNS bypass')}</h2>
    <span class="muted">{t('{n} clients do not use the router’s DNS (last 7 days)', { n: d.data.total })}</span>
    <button class="sw" class:on={d.data.alert} role="switch" aria-checked={d.data.alert} aria-label={t('Alert when a client starts bypassing the local DNS')} onclick={toggle}></button></div>
    <div class="card-b muted" style="font-size:13px">{t('Alert (info) when a client starts to use DNS-over-TLS/QUIC/HTTPS or a public DNS server directly. Off by default; switching it on records the current state first, so existing clients do not raise alerts.')}
      {#if d.data.total}<a href="#/clients/dns-bypass">{t('Show clients')}</a>{/if}</div></div>
{/if}
