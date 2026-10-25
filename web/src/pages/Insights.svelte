<script>
  import Flag from '../lib/Flag.svelte'
  import { api, download } from '../lib/api.js'
  import { app, go } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bytes, num, countryName } from '../lib/format.js'
  import { t, tr, i18n } from '../lib/i18n.svelte.js'
  import ClassifyDialog from '../lib/ClassifyDialog.svelte'
  import UsageDialog from '../lib/UsageDialog.svelte'
  import { stableOrder } from '../lib/sortpref.js'
  let dlg = $state(null)
  let usage = $state(null)

  // the Services page is the one place for services (rules, detail, clients); an old #/insights/services link goes there
  $effect(() => { if (app.route.params.id === 'services') go('services') })
  const tabs = $derived([['clients', t('Clients')], ['hosts', t('Destinations')], ['ports', t('Ports')], ['asn', t('ASNs')], ['country', t('Countries')], ['protocols', t('Protocols')], ['internal', t('Internal talkers')]])
  const what = $derived(tabs.some(tb => tb[0] === app.route.params.id) ? app.route.params.id : 'clients')
  const rg = () => (app.range === 'live' ? '1h' : app.range)
  let grp = $state('')
  const gl = poll(() => api('/groups'), 60000)
  const d = poll(() => api(`/top/${what}?range=${rg()}&limit=100` + (grp && (what === 'clients' || what === 'internal') ? '&group=' + encodeURIComponent(grp) : '')), 8000)
  // the ranking is by bytes and would re-rank on every poll; keep the order for 30 s unless tab, range or group change
  let ord = null
  const rows = $derived.by(() => {
    ord = stableOrder(ord, d.data?.rows || [], r => r.key + '|' + r.sub, { ctx: what + '|' + rg() + '|' + grp })
    return ord.rows
  })
  const max = $derived(rows.reduce((m, r) => Math.max(m, r.up + r.down + r.internal), 0) || 1)
  const pick = r => { if (r.device) go('device/' + encodeURIComponent(r.device)); else if (what === 'clients' || what === 'internal') go('client/' + encodeURIComponent(r.key)); else if (what === 'services') go('services/' + encodeURIComponent(r.key)); else if (['hosts', 'ports', 'asn', 'country', 'protocols'].includes(what)) usage = { what, key: r.key, title: what === 'country' ? countryName(r.key, i18n.lang) : nameOf(r) } }
  const classify = (r, e) => {
    e.stopPropagation()
    if (what === 'hosts') dlg = { kind: 'ip', value: r.key, name: '', hint: t('Assign all connections to {target} to a service.', { target: r.label || r.key }) }
    else if (what === 'ports') { const [p, port] = r.key.split('/'); dlg = { kind: 'port', value: port, proto: +p === 6 || +p === 17 ? +p : 0, name: r.sub || '', hint: t('Assign everything on port {port} to a service.', { port }) } }
    else if (what === 'asn') dlg = { kind: 'asn', value: r.key, name: r.label || '', hint: t('Assign all destinations of this provider to a service.') }
  }
  const canClassify = $derived(['hosts', 'ports', 'asn'].includes(what))
  const protoName = p => ({ 1: 'ICMP', 6: 'TCP', 17: 'UDP', 47: 'GRE', 50: 'ESP', 58: 'ICMPv6' }[p] || t('proto {n}', { n: p }))
  const nameOf = r => what === 'protocols' ? protoName(+r.key) : what === 'asn' ? (r.label || 'AS' + r.key) : tr(r.label)
</script>

<div class="head"><h1>{t('Insights')}</h1><span class="muted">{t('Aggregated from flow rollups · last {range}', { range: rg() })}</span></div>
<div class="card">
  <div class="card-h">
    <div class="tabs" role="tablist" style="flex-wrap:wrap">
      {#each tabs as [id, l]}<button role="tab" class:on={what === id} aria-selected={what === id} onclick={() => go('insights/' + id)}>{l}</button>{/each}
      <button role="tab" aria-selected="false" title={t('Opens the Services page')} onclick={() => go('services')}>{t('Services')} →</button>
    </div>
    <span style="display:flex;gap:8px">{#if (what === 'clients' || what === 'internal') && (gl.data?.groups || []).length}<select class="input" style="width:auto" bind:value={grp} aria-label={t('Group')}><option value="">{t('All groups')}</option><option value="none">{t('No group')}</option>{#each gl.data.groups as g}<option value={g.name}>{g.name}</option>{/each}</select>{/if}<button class="btn" onclick={() => download(`/export/top/${what}?range=${rg()}&format=xlsx`)}>{t('Export Excel')}</button>
    <button class="btn" onclick={() => download(`/export/top/${what}?range=${rg()}`)}>{t('Export CSV')}</button></span>
  </div>
  <div class="scroll"><table>
    <thead><tr><th style="width:36px">#</th><th>{tabs.find(tb => tb[0] === what)[1]}</th><th>{t('Detail')}</th><th class="r">{t('↓ Down')}</th><th class="r">{t('↑ Up')}</th>{#if what === 'internal' || what === 'clients'}<th class="r">{t('⇄ LAN')}</th>{/if}<th style="width:160px">{t('Share')}</th><th class="r">{t('Flows')}</th>{#if canClassify}<th></th>{/if}</tr></thead>
    <tbody>
      {#each rows as r, i (r.key + '|' + r.sub + i)}
        <tr class:click={what !== 'x'} onclick={() => pick(r)}>
          <td class="muted num">{i + 1}</td>
          <td><b>{#if what === 'country'}<Flag cc={r.key} />{countryName(r.key, i18n.lang)} <span class="muted">{r.key}</span>{:else}{nameOf(r)}{/if}</b>{#if (what === 'hosts') && r.label !== r.key}<div class="muted mono" style="font-size:11.5px">{r.key}</div>{/if}</td>
          <td class="muted">{tr(r.sub)}</td>
          <td class="r num">{bytes(r.down)}</td><td class="r num">{bytes(r.up)}</td>
          {#if what === 'internal' || what === 'clients'}<td class="r num">{bytes(r.internal)}</td>{/if}
          <td><div class="bar"><i style="width:{((r.up + r.down + r.internal) / max) * 100}%"></i></div></td>
          <td class="r num muted">{num(r.flows)}</td>
          {#if canClassify}<td class="r"><button class="btn sm" onclick={e => classify(r, e)}>{t('Classify')}</button></td>{/if}
        </tr>
      {/each}
    </tbody></table>
    {#if !rows.length}<div class="empty">{d.loading ? t('Loading…') : what === 'country' || what === 'asn' ? t('No GeoIP/ASN database configured — see Settings') : t('No traffic recorded in this range')}</div>{/if}
  </div>
</div>
{#if usage}<UsageDialog what={usage.what} keyOf={usage.key} title={usage.title} range={rg()} onclose={() => (usage = null)} />{/if}
{#if dlg}<ClassifyDialog init={dlg} onclose={() => (dlg = null)} onsaved={() => d.data = null} />{/if}
<style>.head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; }</style>
