<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script>
  import Flag from '../lib/Flag.svelte'
  import { api, download } from '../lib/api.js'
  import { app, go, setOwn } from '../lib/state.svelte.js'
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
  const tabs = $derived([['clients', t('Clients')], ['hosts', t('Destinations')], ['ports', t('Ports')], ['asn', t('ASNs')], ['country', t('Countries')], ['protocols', t('Protocols')], ['internal', t('Internal talkers')], ['interfaces', t('Interfaces')]])
  const what = $derived(tabs.some(tb => tb[0] === app.route.params.id) ? app.route.params.id : 'clients')
  const rg = () => (app.range === 'live' ? '1h' : app.range)
  let grp = $state('')
  const gl = poll(() => api('/groups'), 60000)
  // interface / VLAN filter (#299): token "device:index" from /api/interfaces; '' = all traffic
  let iface = $state(takeIface())
  // the Internet page hands over the WAN interface it wants to look at
  function takeIface() { try { const v = sessionStorage.getItem('mtmon.iface') || ''; sessionStorage.removeItem('mtmon.iface'); return v } catch { return '' } }
  const ifs = poll(() => api(`/interfaces?range=${rg()}` + (app.own ? '&own=1' : '')), 30000)
  const ifQ = () => (iface ? '&iface=' + encodeURIComponent(iface) : '')
  const ifName = i => (i.self ? t('Router itself') : i.name || 'if' + i.idx)
  const ifAll = $derived((ifs.data?.interfaces || []).concat(ifs.data?.wan || []))
  const ifLabel = i => ifName(i) + (ifAll.filter(o => o.name === i.name).length > 1 ? ' (' + i.dev + ')' : '')
  const d =poll(() => what === 'interfaces' ? Promise.resolve(null) : api(`/top/${what}?range=${rg()}&limit=100` + (app.own ? '&own=1' : '') + ifQ() + (grp && (what === 'clients' || what === 'internal') ? '&group=' + encodeURIComponent(grp) : '')), 8000)
  const sinceNote = $derived(ifs.data?.since ? new Date(ifs.data.since * 1000).toLocaleDateString(i18n.lang) : '')
  const mxMax = $derived((ifs.data?.matrix || []).reduce((m, r) => Math.max(m, r.bytes), 0) || 1)
  const ifMax = $derived(ifAll.reduce((m, r) => Math.max(m, r.up + r.down + r.internal), 0) || 1)
  // the ranking is by bytes and would re-rank on every poll; keep the order for 30 s unless tab, range or group change
  let ord = null
  const rows = $derived.by(() => {
    ord = stableOrder(ord, d.data?.rows || [], r => r.key + '|' + r.sub, { ctx: what + '|' + rg() + '|' + grp + '|' + iface })
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

<div class="head"><h1>{t('Insights')}</h1><span class="muted">{t('Aggregated from flow rollups · last {range}', { range: rg() })}</span><label class="chk" style="margin-left:auto"><input type="checkbox" checked={app.own} onchange={e => setOwn(e.currentTarget.checked)} /> {t("Show mtmon's own traffic")}</label></div>
<div class="card">
  <div class="card-h">
    <div class="tabs" role="tablist" style="flex-wrap:wrap">
      {#each tabs as [id, l]}<button role="tab" class:on={what === id} aria-selected={what === id} onclick={() => go('insights/' + id)}>{l}</button>{/each}
      <button role="tab" aria-selected="false" title={t('Opens the Services page')} onclick={() => go('services')}>{t('Services')} →</button>
    </div>
    <span style="display:flex;gap:8px">{#if (what === 'clients' || what === 'internal') && (gl.data?.groups || []).length}<select class="input" style="width:auto" bind:value={grp} aria-label={t('Group')}><option value="">{t('All groups')}</option><option value="none">{t('No group')}</option>{#each gl.data.groups as g}<option value={g.name}>{g.name}</option>{/each}</select>{/if}{#if what !== 'interfaces' && ifAll.length}<select class="input" style="width:auto" bind:value={iface} aria-label={t('Interface')}><option value="">{t('All interfaces')}</option>{#each ifAll as i}<option value={i.token}>{ifLabel(i)}</option>{/each}</select>{/if}
    {#if what === 'interfaces'}<button class="btn" onclick={() => download(`/export/interfaces?range=${rg()}&format=xlsx`)}>{t('Export Excel')}</button>
    <button class="btn" onclick={() => download(`/export/interfaces?range=${rg()}` + (app.own ? '&own=1' : ''))}>{t('Export CSV')}</button>
    {:else}<button class="btn" onclick={() => download(`/export/top/${what}?range=${rg()}&format=xlsx` + ifQ())}>{t('Export Excel')}</button>
    <button class="btn" onclick={() => download(`/export/top/${what}?range=${rg()}` + (app.own ? '&own=1' : '') + ifQ())}>{t('Export CSV')}</button>{/if}</span>
  </div>
  {#if what === 'interfaces'}
  <div class="scroll"><table>
    <thead><tr><th>{t('Interface')}</th><th>{t('Router')}</th><th class="r">{t('↓ Down')}</th><th class="r">{t('↑ Up')}</th><th class="r">{t('⇄ LAN')}</th><th style="width:160px">{t('Share')}</th><th class="r">{t('Clients')}</th><th class="r">{t('Flows')}</th></tr></thead>
    <tbody>
      {#each ifAll as i (i.token)}
        <tr class="click" onclick={() => { iface = i.token; go('insights/clients') }} title={t('Show the clients of this interface')}>
          <td><b>{ifName(i)}</b>{#if !i.known && !i.self} <span class="muted" style="font-size:11.5px">{t('not listed by the router')}</span>{/if}{#if i.type} <span class="muted" style="font-size:11.5px">{i.type}</span>{/if}</td>
          <td class="muted">{i.dev}</td>
          <td class="r num">{bytes(i.down)}</td><td class="r num">{bytes(i.up)}</td><td class="r num">{bytes(i.internal)}</td>
          <td><div class="bar"><i style="width:{((i.up + i.down + i.internal) / ifMax) * 100}%"></i></div></td>
          <td class="r num muted">{num(i.clients)}</td><td class="r num muted">{num(i.flows)}</td>
        </tr>
      {/each}
    </tbody></table>
    {#if !ifAll.length}<div class="empty">{ifs.loading ? t('Loading…') : t('No interface data yet. It is recorded from the moment flows carry interface numbers; older traffic has none.')}</div>{/if}
  </div>
  {#if sinceNote}<div class="card-b muted" style="font-size:12px">{t('Interface data exists since {date}; older traffic is not attributed to an interface. Per bridge port is not covered.', { date: sinceNote })}</div>{/if}
  <div class="card-h"><h2>{t('Traffic between interfaces')}</h2><span class="muted" style="font-size:12px">{t('Routed LAN traffic from one interface or VLAN to another, without the router itself')}</span></div>
  <div class="scroll"><table>
    <thead><tr><th>{t('From interface')}</th><th>{t('To interface')}</th><th class="r">{t('Traffic')}</th><th style="width:160px">{t('Share')}</th></tr></thead>
    <tbody>
      {#each ifs.data?.matrix || [] as m (m.dev + '|' + m.from + '|' + m.to)}
        <tr><td><b>{m.from_name}</b> <span class="muted">{m.dev}</span></td><td><b>{m.to_name}</b></td><td class="r num">{bytes(m.bytes)}</td>
          <td><div class="bar"><i style="width:{(m.bytes / mxMax) * 100}%"></i></div></td></tr>
      {/each}
    </tbody></table>
    {#if !(ifs.data?.matrix || []).length}<div class="empty">{t('No routed traffic between interfaces in this range')}</div>{/if}
  </div>
  {:else}
  {#if iface}<div class="card-b muted" style="font-size:12px">{t('Only traffic of one interface is shown.')} {#if sinceNote}{t('Interface data exists since {date}; older traffic is not attributed to an interface. Per bridge port is not covered.', { date: sinceNote })}{/if}</div>{/if}
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
  {/if}
</div>
{#if usage}<UsageDialog what={usage.what} keyOf={usage.key} title={usage.title} range={rg()} onclose={() => (usage = null)} />{/if}
{#if dlg}<ClassifyDialog init={dlg} onclose={() => (dlg = null)} onsaved={() => d.data = null} />{/if}
<style>.head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; }</style>
