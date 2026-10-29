<script>
  // Path view (#147): hops from this client to one destination, weakest hop highlighted. Desktop left → right, phone top → bottom.
  import { api } from './api.js'
  import { poll } from './Poll.svelte.js'
  import { bps, bytes } from './format.js'
  import { t, tr } from './i18n.svelte.js'
  let { id, range = '1h', hosts = [] } = $props()
  let dst = $state('')
  // a different client must not inherit the previous destination
  $effect(() => { id; dst = '' })
  const p = poll(() => api('/path?client=' + encodeURIComponent(id) + '&range=' + range + (dst ? '&dst=' + encodeURIComponent(dst) : '')), 8000)
  const hops = $derived(p.data?.hops || [])
  const val = (h, f) => (h.kind === 'endpoint' && f.k === 'Bytes' ? bytes(Number(f.v)) : tr(f.v))
</script>

<div class="card path" style="margin-top:16px">
  <div class="card-h"><h2>{t('Path')}</h2>
    {#if hosts.length}
      <select class="input" style="width:auto;max-width:60%" aria-label={t('Destination')} bind:value={dst}>
        <option value="">{t('Top destination')}</option>
        {#each hosts as h}<option value={h.key}>{h.label && h.label !== h.key ? h.label + ' · ' + h.key : h.key}</option>{/each}
      </select>
    {/if}
  </div>
  {#if p.error}<div class="warnbox" style="margin:12px">{tr(p.error)}</div>{/if}
  <div class="card-b">
    {#if !hops.length && !p.error}<div class="muted">{p.loading ? '…' : t('No path data')}</div>{/if}
    <ol class="hops">
      {#each hops as h, i}
        <li class="hop {h.health}" class:weak={i === p.data.weakest}>
          <div class="node">
            <span class="k">{tr(h.kind === 'client' ? 'Client' : h.kind === 'link' ? 'Link' : h.kind === 'device' ? 'Device' : h.kind === 'wan' ? 'Internet' : h.kind === 'network' ? 'Network' : 'Destination')}</span>
            <b class="ti">{tr(h.title)}</b>
            {#if h.sub}<span class="muted sub">{tr(h.sub)}</span>{/if}
            {#if h.why}<span class="why">{tr(h.why)}</span>{/if}
            {#each h.facts as f}<span class="fact"><span class="muted">{tr(f.k)}</span> {val(h, f)}</span>{/each}
            {#if h.bps > 0}<span class="fact"><span class="muted">{t('Traffic')}</span> {bps(h.bps)}</span>{/if}
          </div>
        </li>
      {/each}
    </ol>
    {#if p.data && p.data.weakest >= 0}
      <div class="sum">{t('Weakest hop')}: <b>{tr(hops[p.data.weakest].title)}</b> – {tr(hops[p.data.weakest].why)}</div>
    {:else if hops.length}
      <div class="sum muted">{t('No weak hop found')}</div>
    {/if}
  </div>
</div>

<style>
  .hops { list-style: none; margin: 0; padding: 0; display: flex; gap: 0; overflow-x: auto; padding-bottom: 6px; }
  .hop { flex: 0 0 auto; min-width: 150px; max-width: 210px; position: relative; padding-right: 18px; }
  .hop:not(:last-child)::after { content: ''; position: absolute; right: 2px; top: 22px; width: 14px; height: 2px; background: var(--border-2); }
  .node { display: flex; flex-direction: column; gap: 2px; border: 1px solid var(--border); border-left: 4px solid var(--faint); border-radius: 8px; padding: 8px 10px; background: var(--card-2); font-size: 12.5px; height: 100%; }
  .hop.ok .node { border-left-color: var(--ok); }
  .hop.warn .node { border-left-color: var(--warn); background: var(--warn-bg); }
  .hop.bad .node { border-left-color: var(--bad); background: var(--bad-bg); }
  .hop.weak .node { outline: 2px solid var(--bad); }
  .hop.weak.warn .node { outline-color: var(--warn); }
  .k { font-size: 11px; text-transform: uppercase; letter-spacing: .04em; color: var(--muted); }
  .ti { overflow-wrap: anywhere; }
  .why { color: var(--bad); font-weight: 600; }
  .hop.warn .why { color: var(--warn); }
  .fact { display: block; overflow-wrap: anywhere; }
  .sum { margin-top: 10px; font-size: 13px; }
  @media (max-width: 860px) {
    .hops { flex-direction: column; overflow: visible; }
    .hop { max-width: none; padding: 0 0 18px; }
    .hop:not(:last-child)::after { right: auto; left: 20px; top: auto; bottom: 2px; width: 2px; height: 14px; }
  }
</style>
