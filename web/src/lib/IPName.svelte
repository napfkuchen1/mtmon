<script>
  import Flag from './Flag.svelte'
  // Human name + IP for an address: name (bold) on the first line, IP (+ org · country) muted below.
  // The raw IP stays selectable. Falls back to the bare IP when nothing is known.
  
  import { tr } from './i18n.svelte.js'
  let { ip, name = '', org = '', cc = '', port = 0, local = false } = $props()
  const primary = $derived(tr(name) || org || '')
  const second = $derived([org && name ? org : '', cc ? cc : ''].filter(Boolean).join(' · '))
  const addr = $derived(ip + (port ? ':' + port : ''))
  const tip = $derived([primary, addr, org && org !== primary ? org : '', cc].filter(Boolean).join(' · '))
</script>
{#if primary}
  <div class="ipn" title={tip} aria-label={tip}>
    <div class="n">{primary}</div>
    <div class="s"><span class="mono ip">{addr}</span>{#if second}<span class="muted"> · {second}</span>{/if}</div>
  </div>
{:else}
  <div class="ipn" title={tip || addr}><span class="mono ip">{addr}</span>{#if cc}<div class="s muted"><Flag {cc} />{cc}</div>{/if}</div>
{/if}
<style>
  .ipn { min-width: 0; }
  .n { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 280px; }
  .s { font-size: 12px; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 280px; }
  .ip { user-select: all; }
</style>
