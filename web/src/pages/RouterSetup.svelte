<script>
  import { api } from '../lib/api.js'
  import { t, tr } from '../lib/i18n.svelte.js'
  import { toast } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  const setup = poll(() => api('/setup'), 0)
  async function copy(text) { try { await navigator.clipboard.writeText(text); toast(t('Copied')) } catch { toast(t('Copy failed – select the text manually')) } }
  const u = $derived(setup.data)
  const blocks = $derived(u ? [
    [t('1 · Enable Traffic-Flow (IPFIX) on each MikroTik'), u.flow],
    [t('2 · Create a read-only API user'), u.api_user],
    [t('3 · Enable REST over HTTPS (www-ssl) restricted to mtmon'), u.rest_tls]
  ] : [])
</script>

<details class="card rs" style="margin-top:16px"><summary class="card-h"><h2>{t('Router setup')}</h2><span class="muted">{t('for devices you set up by hand – the wizard does this for you')} · {t('mtmon address detected:')} <span class="mono">{u?.mtmon_ip}</span></span></summary>
  <div class="card-b" style="display:flex;flex-direction:column;gap:16px;border-top:1px solid var(--border)">
    {#each blocks as [title, code]}
      <div><div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px"><b>{title}</b><button class="btn sm" onclick={() => copy(code)}>{t('Copy')}</button></div><pre class="code">{code}</pre></div>
    {/each}
    {#if u}
      <div class="warnbox"><b>FastTrack:</b> {tr(u.fasttrack)}</div>
      <div class="muted">{t('Pin the router certificate:')} <span class="mono">{u.fingerprint}</span>. {t('Then test everything with')} <span class="mono">mtmon check</span>.</div>
    {/if}
  </div></details>
<style>.rs > summary { cursor: pointer; list-style: none; flex-wrap: wrap; gap: 6px 14px; } .rs > summary::-webkit-details-marker { display: none; } .rs[open] > summary { border-bottom: 0; }</style>
