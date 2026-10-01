<script>
  import { api } from '../lib/api.js'
  import { app, go, toast } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bytes, bps, num, flag, protoName, ago, display } from '../lib/format.js'
  import Chart from '../lib/Chart.svelte'
  import ClassifyDialog from '../lib/ClassifyDialog.svelte'
  const rg = () => (app.range === 'live' ? '1h' : app.range)
  const name = $derived(app.route.params.id)

  const list = poll(() => api('/services?range=' + rg()), 8000)
  const rules = poll(() => api('/service-rules'), 0)
  const det = poll(() => app.route.params.id ? api('/services/' + encodeURIComponent(app.route.params.id) + '?range=' + rg()) : Promise.resolve(null), 6000)

  let q = $state(''), cat = $state(''), dlg = $state(null), showRules = $state(false)
  const services = $derived(list.data?.services || [])
  const cats = $derived([...new Set(services.map(s => s.category))].sort())
  const shown = $derived(services.filter(s => (!cat || s.category === cat) && (!q || s.name.toLowerCase().includes(q.toLowerCase()))))
  const tot = s => s.up + s.down + s.internal
  const max = $derived(shown.reduce((m, s) => Math.max(m, tot(s)), 0) || 1)
  const d = $derived(det.data?.detail)
  const bucket = $derived({ '1h': 60, '24h': 900, '7d': 3600, '30d': 14400 }[rg()] || 60)
  const eff = $derived(rg() === '1h' ? 60 : Math.max(bucket, 3600))
  const chart = $derived([
    { name: 'Download', color: 'var(--down)', points: (d?.series || []).map(p => ({ x: p.ts, y: (p.down * 8) / eff })) },
    { name: 'Upload', color: 'var(--up)', points: (d?.series || []).map(p => ({ x: p.ts, y: (p.up * 8) / eff })) }
  ])
  const dsts = $derived((d?.dests || []))
  function classifyDest(x) {
    dlg = { kind: 'ip', value: x.rip, name: '', hint: `Alle Verbindungen zu ${x.host || x.rip} einem Service zuordnen.`, alt: x }
  }
  function classifyPort(x) {
    dlg = { kind: 'port', value: String(x.port), proto: x.proto === 17 ? 17 : x.proto === 6 ? 6 : 0, name: '', hint: `Alles auf ${protoName(x.proto)}/${x.port} einem Service zuordnen.` }
  }
  async function delRule(r) {
    if (!confirm(`Regel „${r.name}“ (${r.kind} ${r.value}) löschen?`)) return
    try { await api('/service-rules/' + r.id, { method: 'DELETE' }); toast('Regel gelöscht – Zuordnung wird zurückgesetzt'); rules.data = await api('/service-rules') } catch (e) { toast(e.message) }
  }
  const saved = async () => { rules.data = await api('/service-rules'); list.data = await api('/services?range=' + rg()) }
</script>

{#if !name}
  <div class="head"><h1>Services</h1><span class="muted">Welcher Dienst wird von welchen Geräten genutzt · letzte {rg()}</span>
    <span style="flex:1"></span><button class="btn" onclick={() => (showRules = !showRules)}>Eigene Regeln ({rules.data?.length || 0})</button>
    <button class="btn primary" onclick={() => (dlg = { kind: 'port', value: '', name: '', hint: 'Lege eine neue Zuordnung an – z. B. Port 123/UDP → „NTP“, oder einen Hostnamen → „Bambu Cloud“.' })}>＋ Service definieren</button></div>
  {#if list.error}<div class="warnbox">{list.error}</div>{/if}
  {#if list.data?.reclassifying}<div class="warnbox" style="margin-bottom:12px">Alte Daten werden gerade neu zugeordnet … die Zahlen aktualisieren sich in Kürze.</div>{/if}

  {#if showRules}
    <div class="card" style="margin-bottom:16px"><div class="card-h"><h2>Eigene Klassifizierungsregeln</h2><span class="muted" style="font-size:12.5px">IP &gt; Netz &gt; Hostname &gt; AS &gt; Port &gt; Standardname</span></div>
      <table><thead><tr><th>Service</th><th>Kategorie</th><th>Typ</th><th>Wert</th><th></th></tr></thead>
        <tbody>{#each rules.data || [] as r (r.id)}<tr><td><b>{r.name}</b></td><td class="muted">{r.category || '—'}</td><td>{r.kind}</td><td class="mono">{r.value}{r.kind === 'port' && r.proto ? '/' + protoName(r.proto) : ''}</td><td class="r"><button class="btn sm" onclick={() => delRule(r)}>Löschen</button></td></tr>{/each}</tbody></table>
      {#if !rules.data?.length}<div class="empty">Noch keine eigenen Regeln. Klicke bei Zielen oder Ports auf „Klassifizieren“.</div>{/if}</div>
  {/if}

  <div class="card">
    <div class="card-h">
      <div class="pill-row"><button class="btn sm" class:primary={!cat} onclick={() => (cat = '')}>Alle</button>
        {#each cats as c}<button class="btn sm" class:primary={cat === c} onclick={() => (cat = c)}>{c}</button>{/each}</div>
      <input class="input" placeholder="Service suchen …" bind:value={q} aria-label="Service suchen" />
    </div>
    <div class="scroll"><table>
      <thead><tr><th>Service</th><th>Kategorie</th><th class="r">Clients</th><th class="r">Ziele</th><th class="r">↓ Down</th><th class="r">↑ Up</th><th style="width:150px">Anteil</th><th class="r">Flows</th></tr></thead>
      <tbody>{#each shown as s (s.name)}
        <tr class="click" onclick={() => go('services/' + encodeURIComponent(s.name))}>
          <td><b>{s.name}</b></td><td><span class="badge">{s.category}</span></td><td class="r num" title={app.range === '30d' ? 'Clients der letzten 48 h' : ''}>{num(s.clients)}</td><td class="r num">{num(s.dests)}</td>
          <td class="r num">{bytes(s.down)}</td><td class="r num">{bytes(s.up)}</td><td><div class="bar"><i style="width:{(tot(s) / max) * 100}%"></i></div></td><td class="r num muted">{num(s.flows)}</td></tr>
      {/each}</tbody></table>
      {#if !shown.length}<div class="empty">{list.loading ? 'Lade …' : 'Keine Services mit Traffic in diesem Zeitraum'}</div>{/if}
    </div>
  </div>

{:else}
  <div class="head"><a href="#/services">← Services</a><h1>{name}</h1>{#if det.data}<span class="badge">{det.data.category}</span>{/if}</div>
  {#if det.error}<div class="warnbox">{det.error}</div>{/if}
  {#if d}
    <div class="grid g4" style="margin-bottom:16px">
      <div class="card card-b kpi"><div class="l">Clients</div><div class="v num">{d.clients.length}</div><div class="l">nutzen diesen Service</div></div>
      <div class="card card-b kpi"><div class="l">Ziele</div><div class="v num">{d.dests.length}{d.dests.length >= 50 ? '+' : ''}</div><div class="l">Adresse/Port-Kombinationen</div></div>
      <div class="card card-b kpi"><div class="l">↓ Download</div><div class="v num">{bytes(d.down)}</div><div class="l">in {rg()}</div></div>
      <div class="card card-b kpi"><div class="l">↑ Upload</div><div class="v num">{bytes(d.up)}</div><div class="l">{num(d.flows)} Flows</div></div>
    </div>

    <div class="card"><div class="card-h"><h2>Wer nutzt „{name}“?</h2><span class="muted" style="font-size:12.5px">{d.clients_since ? 'Clients: nur letzte 48 h (lange Zeiträume ohne Client-Detail) · ' : ''}Klick öffnet den Client</span></div>
      <div class="scroll"><table>
        <thead><tr><th>Client</th><th>Verbunden mit</th><th class="r">Ziele</th><th class="r">↓ Down</th><th class="r">↑ Up</th><th class="r">Flows</th></tr></thead>
        <tbody>{#each d.clients as c (c.mac)}
          <tr class="click" onclick={() => go('client/' + encodeURIComponent(c.mac))}>
            <td><b>{c.label || c.mac}</b><div class="muted mono" style="font-size:11.5px">{c.ip} · {c.vendor || c.mac}</div></td><td>{c.device || '—'}</td>
            <td class="r num">{num(c.dests)}</td><td class="r num">{bytes(c.down)}</td><td class="r num">{bytes(c.up)}</td><td class="r num muted">{num(c.flows)}</td></tr>
        {/each}</tbody></table>
        {#if !d.clients.length}<div class="empty">Keine Clients in diesem Zeitraum</div>{/if}</div></div>

    <div class="card" style="margin-top:16px"><div class="card-h"><h2>Traffic</h2><div class="legend"><span><i style="background:var(--down)"></i>Download</span><span><i style="background:var(--up)"></i>Upload</span></div></div>
      <div class="card-b"><Chart series={chart} fmt={bps} height={150} /></div></div>

    <div class="card" style="margin-top:16px"><div class="card-h"><h2>Ziele</h2><span class="muted" style="font-size:12.5px">wohin geht der Traffic</span></div>
      <div class="scroll"><table>
        <thead><tr><th>Ziel</th><th>Port</th><th>Land / Anbieter</th><th class="r">Clients</th><th class="r">↓ Down</th><th class="r">↑ Up</th><th></th></tr></thead>
        <tbody>{#each dsts as x (x.rip + x.port + x.proto)}
          <tr><td><span class="mono">{x.rip}</span>{#if x.host}<div class="muted">{x.host}</div>{/if}</td><td class="mono">{protoName(x.proto)}/{x.port}</td>
            <td>{flag(x.cc)} {x.cc} <span class="muted">{x.asorg}</span></td><td class="r num">{x.clients < 0 ? '–' : x.clients}</td><td class="r num">{bytes(x.down)}</td><td class="r num">{bytes(x.up)}</td>
            <td class="r" style="white-space:nowrap"><button class="btn sm" onclick={() => classifyDest(x)}>Ziel</button> <button class="btn sm" onclick={() => classifyPort(x)}>Port</button></td></tr>
        {/each}</tbody></table>
        {#if !dsts.length}<div class="empty">Keine Ziele</div>{/if}</div></div>
  {/if}
{/if}

{#if dlg}<ClassifyDialog init={dlg} known={services.map(s => s.name)} onclose={() => (dlg = null)} onsaved={saved} />{/if}

<style>
  .head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; flex-wrap: wrap; }
</style>
