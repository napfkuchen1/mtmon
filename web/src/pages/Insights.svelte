<script>
  import { api, download } from '../lib/api.js'
  import { app, go } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bytes, num, flag } from '../lib/format.js'
  import ClassifyDialog from '../lib/ClassifyDialog.svelte'
  let dlg = $state(null)

  const tabs = [['clients', 'Clients'], ['hosts', 'Destinations'], ['services', 'Services'], ['ports', 'Ports'], ['asn', 'ASNs'], ['country', 'Countries'], ['protocols', 'Protocols'], ['internal', 'Internal talkers']]
  const what = $derived(tabs.some(t => t[0] === app.route.params.id) ? app.route.params.id : 'clients')
  const rg = () => (app.range === 'live' ? '1h' : app.range)
  const d = poll(() => api(`/top/${what}?range=${rg()}&limit=100`), 8000)
  const rows = $derived(d.data?.rows || [])
  const max = $derived(rows.reduce((m, r) => Math.max(m, r.up + r.down + r.internal), 0) || 1)
  const pick = r => { if (what === 'clients' || what === 'internal') go('client/' + encodeURIComponent(r.key)); else if (what === 'services') go('services/' + encodeURIComponent(r.key)) }
  const classify = (r, e) => {
    e.stopPropagation()
    if (what === 'hosts') dlg = { kind: 'ip', value: r.key, name: '', hint: `Alle Verbindungen zu ${r.label || r.key} einem Service zuordnen.` }
    else if (what === 'ports') { const [p, port] = r.key.split('/'); dlg = { kind: 'port', value: port, proto: +p === 6 || +p === 17 ? +p : 0, name: r.sub || '', hint: `Alles auf Port ${port} einem Service zuordnen.` } }
    else if (what === 'asn') dlg = { kind: 'asn', value: r.key, name: r.label || '', hint: 'Alle Ziele dieses Anbieters einem Service zuordnen.' }
  }
  const canClassify = $derived(['hosts', 'ports', 'asn'].includes(what))
  const protoName = p => ({ 1: 'ICMP', 6: 'TCP', 17: 'UDP', 47: 'GRE', 50: 'ESP', 58: 'ICMPv6' }[p] || 'proto ' + p)
  const nameOf = r => what === 'protocols' ? protoName(+r.key) : what === 'asn' ? (r.label || 'AS' + r.key) : r.label
</script>

<div class="head"><h1>Insights</h1><span class="muted">Aggregated from flow rollups · last {rg()}</span></div>
<div class="card">
  <div class="card-h">
    <div class="tabs" role="tablist" style="flex-wrap:wrap">
      {#each tabs as [id, l]}<button role="tab" class:on={what === id} aria-selected={what === id} onclick={() => go('insights/' + id)}>{l}</button>{/each}
    </div>
    <button class="btn" onclick={() => download(`/export/top/${what}?range=${rg()}`)}>Export CSV</button>
  </div>
  <div class="scroll"><table>
    <thead><tr><th style="width:36px">#</th><th>{tabs.find(t => t[0] === what)[1]}</th><th>Detail</th><th class="r">↓ Down</th><th class="r">↑ Up</th>{#if what === 'internal' || what === 'clients'}<th class="r">⇄ LAN</th>{/if}<th style="width:160px">Share</th><th class="r">Flows</th>{#if canClassify}<th></th>{/if}</tr></thead>
    <tbody>
      {#each rows as r, i (r.key + '|' + r.sub + i)}
        <tr class:click={what === 'clients' || what === 'internal' || what === 'services'} onclick={() => pick(r)}>
          <td class="muted num">{i + 1}</td>
          <td><b>{#if what === 'country'}{flag(r.key)} {/if}{nameOf(r)}</b>{#if (what === 'hosts') && r.label !== r.key}<div class="muted mono" style="font-size:11.5px">{r.key}</div>{/if}</td>
          <td class="muted">{r.sub}</td>
          <td class="r num">{bytes(r.down)}</td><td class="r num">{bytes(r.up)}</td>
          {#if what === 'internal' || what === 'clients'}<td class="r num">{bytes(r.internal)}</td>{/if}
          <td><div class="bar"><i style="width:{((r.up + r.down + r.internal) / max) * 100}%"></i></div></td>
          <td class="r num muted">{num(r.flows)}</td>
          {#if canClassify}<td class="r"><button class="btn sm" onclick={e => classify(r, e)}>Klassifizieren</button></td>{/if}
        </tr>
      {/each}
    </tbody></table>
    {#if !rows.length}<div class="empty">{d.loading ? 'Loading…' : what === 'country' || what === 'asn' ? 'No GeoIP/ASN database configured — see Settings' : 'No traffic recorded in this range'}</div>{/if}
  </div>
</div>
{#if dlg}<ClassifyDialog init={dlg} onclose={() => (dlg = null)} onsaved={() => d.data = null} />{/if}
<style>.head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; }</style>
