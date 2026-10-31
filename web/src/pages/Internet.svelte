<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script>
  // Internet page: scheduled speed tests (history, schedule, data budget) measured by mtmon from its own host.
  import { api } from '../lib/api.js'
  import { toast } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { bps, bytes, ago } from '../lib/format.js'
  import { t, tr, i18n } from '../lib/i18n.svelte.js'
  import Chart from '../lib/Chart.svelte'
  let tzName = $state('')
  api('/timezone').then(x => (tzName = x.zone)).catch(() => {})
  import Empty from '../lib/Empty.svelte'
  import Upstream from '../lib/Upstream.svelte'
  const inet = poll(() => api('/internet?range=24h'), 60000)

  const ranges = [['24h', '24 h'], ['7d', '7 d'], ['30d', '30 d'], ['90d', '90 d'], ['365d', '1 y']]
  let range = $state('30d'), busy = $state(false), more = $state(false)
  const d = poll(() => api('/internet/speedtests?range=' + range), 8000)
  const x = $derived(d.data)
  const st = $derived(x?.settings)
  const status = $derived(x?.status || {})
  const items = $derived(x?.items || [])
  const ok = $derived(items.filter(i => !i.error))
  const pts = key => ok.filter(i => i[key] > 0).map(i => ({ x: i.ts, y: key === 'ping_ms' ? i[key] : i[key] })).reverse()
  const when = ts => new Date(ts * 1000).toLocaleString(i18n.locale, { dateStyle: 'short', timeStyle: 'short' })
  const xfmt = v => new Date(v * 1000).toLocaleString(i18n.locale, range === '24h' ? { hour: '2-digit', minute: '2-digit' } : { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })
  const contract = (mbps, color, name) => (mbps > 0 && ok.length > 1 ? [{ name, color, dash: true, points: [{ x: ok[ok.length - 1].ts, y: mbps * 1e6 }, { x: ok[0].ts, y: mbps * 1e6 }] }] : [])
  const speedSeries = $derived([
    { name: t('Download'), color: 'var(--down)', points: pts('down_bps') },
    { name: t('Upload'), color: 'var(--up)', points: pts('up_bps') },
    ...contract(st?.contract_down_mbps, 'var(--muted)', t('Contract download'))
  ])
  // MySpeed's history is Ookla ("auto"/"custom" server choice); label it so, and keep it apart from mtmon's own tests
  const provName = i => (i.src === 'import' && (i.provider === 'auto' || i.provider === 'custom' || i.provider === 'myspeed') ? 'MySpeed (Ookla)' : i.provider)
  const seriesColors = ['var(--accent)', 'var(--down)', 'var(--up)', 'var(--muted)']
  const provNames = $derived([...new Set(ok.filter(i => i.ping_ms > 0).map(provName))])
  // one latency line per provider: the methods differ (Ookla ICMP-like, Cloudflare TCP connect) and must not be mixed
  const pingSeries = $derived(provNames.map((n, k) => ({ name: n, color: seriesColors[k % seriesColors.length], points: ok.filter(i => i.ping_ms > 0 && provName(i) === n).map(i => ({ x: i.ts, y: i.ping_ms })).reverse() })))
  const lastPing = $derived(ok.find(i => i.ping_ms > 0))
  const pingAvg = $derived.by(() => { const l = lastPing ? ok.filter(i => i.ping_ms > 0 && provName(i) === provName(lastPing)) : []; return l.length ? l.reduce((a, i) => a + i.ping_ms, 0) / l.length : 0 })
  const sum = $derived(x?.summary)
  const last = $derived(x?.last)
  const lastUp = $derived(ok.find(i => i.up_bps > 0))
  const intervals = [[0, 'off'], [60, 'every hour'], [180, 'every 3 hours'], [360, 'every 6 hours'], [1440, 'daily']]
  const famLabel = f => (f === '4' ? 'IPv4' : f === '6' ? 'IPv6' : 'auto')

  // settings form: a copy of the stored settings; every section saves only its own keys
  let form = $state(null)
  $effect(() => { if (st && !form) form = JSON.parse(JSON.stringify(st)) })
  const num = v => (v === '' || v == null ? 0 : Number(v))
  const NUMS = ['interval_min', 'jitter_min', 'max_mb', 'max_sec', 'streams', 'budget_day_mb', 'budget_month_mb', 'skip_busy_pct', 'ookla_server_id', 'contract_down_mbps', 'contract_up_mbps', 'alert_below_pct', 'alert_count']
  const SECTIONS = [
    ['sched', 'Schedule', ['enabled', 'paused', 'providers', 'families', 'interval_min', 'window_from', 'window_to', 'start_at', 'jitter_min']],
    ['limits', 'Test size and limits', ['max_mb', 'max_sec', 'streams', 'budget_day_mb', 'budget_month_mb', 'skip_busy_pct']],
    ['contract', 'Contract and alerts', ['contract_down_mbps', 'contract_up_mbps', 'alert_count', 'alert_below_pct']],
    ['prov', 'Providers and licences', ['hetzner_server', 'ookla_accepted', 'ookla_server_id']]
  ]
  const same = (a, c) => (Array.isArray(a) || Array.isArray(c) ? JSON.stringify(a ?? []) === JSON.stringify(c ?? []) : String(a ?? '') === String(c ?? ''))
  const dirty = keys => !!form && !!st && keys.some(k => !same(form[k], st[k]))
  function toggle(list, v) { return list.includes(v) ? list.filter(i => i !== v) : [...list, v] }
  async function save(keys) {
    try {
      const body = { ...st }
      for (const k of keys) body[k] = NUMS.includes(k) ? num(form[k]) : form[k]
      await api('/internet/speedtest/settings', { method: 'PUT', body })
      toast(t('Saved'))
      d.data = await api('/internet/speedtests?range=' + range)
    } catch (e) { toast(tr(e.message)) }
  }
  function discard(keys) { for (const k of keys) form[k] = JSON.parse(JSON.stringify(st[k] ?? '')) }
  // open/closed state of the settings per viewer; collapsed by default once a schedule exists
  const store = (k, v) => { try { if (v === undefined) return localStorage.getItem('mtmon.inet.' + k); localStorage.setItem('mtmon.inet.' + k, v) } catch { return null } }
  let openSet = $state({})
  const isOpen = id => openSet[id] ?? (store(id) != null ? store(id) === '1' : id === 'all' ? !st?.enabled : id === 'sched')
  const onToggle = (id, e) => { openSet[id] = e.currentTarget.open; store(id, e.currentTarget.open ? '1' : '0') }
  const provLabel = { cloudflare: 'Cloudflare', hetzner: 'Hetzner', ndt7: 'NDT7', ookla: 'Ookla' }
  const intervalText = $derived(intervals.find(i => i[0] === st?.interval_min)?.[1])
  const summary = $derived.by(() => {
    if (!st) return ''
    if (!st.enabled) return t('Scheduled tests are off.')
    const p = [intervalText ? t(intervalText) + (st.start_at ? ' ' + t('from {s}', { s: st.start_at }) : '') + (st.jitter_min ? ' ± ' + st.jitter_min + ' min' : '') : '']
    p.push((st.providers || []).map(k => provLabel[k] || k).join(', ') || '—')
    p.push('≤ ' + st.max_mb + ' MB / ' + st.max_sec + ' s')
    if (st.contract_down_mbps > 0 || st.contract_up_mbps > 0) p.push(t('alert below {p} % of {d}/{u}', { p: st.alert_below_pct, d: st.contract_down_mbps, u: st.contract_up_mbps }))
    return p.filter(Boolean).join(' · ')
  })
  // quick test: its own provider choice, never saved (default = the scheduled set)
  let quick = $state(null)
  const quickSel = $derived(quick ?? st?.providers ?? [])
  const quickOpts = $derived(['cloudflare', 'hetzner', 'ndt7', ...(st?.ookla_accepted ? ['ookla'] : [])])
  async function importFile(e) {
    const f = e.target.files?.[0]
    e.target.value = ''
    if (!f) return
    try {
      const r = await api('/internet/speedtests/import', { method: 'POST', raw: await f.text() })
      toast(t('Imported {a} tests ({d} already known, {s} skipped)', { a: r.added, d: r.duplicates, s: r.skipped }))
      d.data = await api('/internet/speedtests?range=' + range)
    } catch (err) { toast(tr(err.message)) }
  }
  async function runNow() {
    busy = true
    try {
      await api('/internet/speedtest', { method: 'POST', body: quick ? { providers: quick } : undefined })
      toast(t('Speed test started'))
      for (let i = 0; i < 60; i++) { // follow the run: the page refreshes every 2 s until it is done
        await new Promise(r => setTimeout(r, 2000))
        d.data = await api('/internet/speedtests?range=' + range)
        if (!d.data.status.running) break
      }
    } catch (e) { toast(tr(e.message)) }
    busy = false
  }
  const skipText = s => ({ 'off': t('off'), 'paused': t('paused'), 'outside the time window': t('outside the time window'), 'data budget reached': t('data budget reached'), 'the line is busy': t('the line is busy') }[s] || s)
</script>

<div class="head"><h1>{t('Internet')}</h1>
  <span class="muted">{t('Speed tests run from the mtmon host, not from a router, so they measure your line.')}</span></div>
<Upstream items={inet.data?.upstream || []} compact />

{#if !x}
  <div class="muted">{t('Loading…')}</div>
{:else}
  <div class="toolbar">
    <div class="tabs" role="tablist">{#each ranges as [id, label]}<button role="tab" aria-selected={range === id} class:on={range === id} onclick={() => (range = id)}>{label}</button>{/each}</div>
    <span class="spacer"></span>
    {#if status.running || busy}<span class="badge info">{t('running…')}</span>
    {:else if !st.enabled}<span class="muted">{t('Scheduled tests are off.')}</span>
    {:else if st.paused}<span class="badge warn">{t('paused')}</span>
    {:else if status.skipped}<span class="muted" title={when(status.skipped_ts)}>{t('Last run skipped: {why}', { why: skipText(status.skipped) })}</span>
    {:else if status.next_ts}<span class="muted">{t('next test {when}', { when: when(status.next_ts) })}</span>{/if}
    <a class="btn" href={'/api/internet/speedtests/export?format=csv&range=' + range} download>CSV</a>
    <a class="btn" href={'/api/internet/speedtests/export?format=json&range=' + range} download>JSON</a>
  </div>

  <div class="card" style="margin-bottom:16px"><div class="card-h"><h2>{t('Quick test')}</h2>
    <span class="muted">{t('One test now with the providers chosen here. The schedule is not changed.')}</span></div>
    <div class="card-b quick">
      {#each quickOpts as pr}<label class="chip" class:on={quickSel.includes(pr)}><input type="checkbox" checked={quickSel.includes(pr)} onchange={() => (quick = toggle(quickSel, pr))} /> {provLabel[pr]}</label>{/each}
      <button class="btn primary" disabled={status.running || busy || !quickSel.length} onclick={runNow}>{status.running || busy ? t('running…') : t('Run now')}</button>
    </div></div>

  {#if !items.length}
    <div class="card"><Empty title={t('No speed tests yet')} hint={t('Switch the schedule on below or press “Run now”.')} /></div>
  {:else}
    <div class="grid g4" style="margin-bottom:16px">
      <div class="card"><div class="card-b kpi"><div class="l muted">{t('Download')}</div><div class="v num">{last?.down_bps ? bps(last.down_bps) : '—'}</div>
        <div class="l muted">{t('avg {a} · min {n} · max {m}', { a: bps(sum.down_bps.avg), n: bps(sum.down_bps.min), m: bps(sum.down_bps.max) })}</div></div></div>
      <div class="card"><div class="card-b kpi"><div class="l muted">{t('Upload')}</div><div class="v num">{lastUp ? bps(lastUp.up_bps) : '—'}</div>
        <div class="l muted">{sum.up_bps.n ? t('avg {a} · min {n} · max {m}', { a: bps(sum.up_bps.avg), n: bps(sum.up_bps.min), m: bps(sum.up_bps.max) }) : t('not measured by the selected providers')}</div></div></div>
      <div class="card"><div class="card-b kpi"><div class="l muted">{t('Latency')}</div><div class="v num">{lastPing ? lastPing.ping_ms.toFixed(0) + ' ms' : '—'}</div>
        <div class="l muted">{lastPing ? t('avg {a} ms · {p}', { a: pingAvg.toFixed(0), p: provName(lastPing) }) : '—'}</div></div></div>
      <div class="card"><div class="card-b kpi"><div class="l muted">{t('Tests')}</div><div class="v num">{sum.tests}</div>
        <div class="l muted">{sum.failed_imported ? t('{f} failed ({i} imported) · {b} used', { f: sum.failed, i: sum.failed_imported, b: bytes(sum.bytes) }) : t('{f} failed · {b} used', { f: sum.failed, b: bytes(sum.bytes) })} {#if last}· {t('last {when}', { when: ago(last.ts) })}{/if}</div></div></div>
    </div>
    <div class="card" style="margin-bottom:16px"><div class="card-h"><h2>{t('Speed over time')}</h2>
      <span class="muted">{t('Failed tests are not drawn as 0 Mbit/s.')}</span></div>
      <div class="card-b"><Chart series={speedSeries} fmt={bps} {xfmt} empty={t('No successful tests in this range.')} /></div></div>
    <div class="card" style="margin-bottom:16px"><div class="card-h"><h2>{t('Latency over time')}</h2></div>
      <div class="card-b"><Chart series={pingSeries} fmt={v => v.toFixed(0) + ' ms'} {xfmt} height={150} empty={t('No successful tests in this range.')} /></div></div>

    <div class="card" style="margin-bottom:16px"><div class="card-h"><h2>{t('Latest tests')}</h2></div>
      <div class="scroll"><table><thead><tr><th>{t('Time')}</th><th>{t('Provider')}</th><th>{t('IP')}</th><th class="r">{t('Download')}</th><th class="r">{t('Upload')}</th><th class="r">{t('Latency')}</th><th class="r">{t('Data')}</th><th></th></tr></thead>
        <tbody>{#each (more ? items : items.slice(0, 15)) as i}
          <tr><td class="num">{when(i.ts)}</td><td>{provName(i)} <span class="muted mono" style="font-size:11px">{i.server}</span>{#if i.src === 'import'} <span class="badge">{t('imported')}</span>{/if}</td>
            <td>{famLabel(i.family)}</td>
            <td class="r num">{i.error ? '—' : i.down_bps ? bps(i.down_bps) : '—'}</td><td class="r num">{i.error ? '—' : i.up_bps ? bps(i.up_bps) : '—'}</td>
            <td class="r num">{!i.error && i.ping_ms ? i.ping_ms.toFixed(0) + ' ms' : '—'}</td><td class="r num">{i.bytes ? bytes(i.bytes) : '—'}</td>
            <td>{#if i.error}<span class="badge bad" title={i.error}>{t('failed')}</span> <span class="muted err" title={i.error}>{i.error}</span>{/if}</td></tr>
        {/each}</tbody></table></div>
      {#if items.length > 15 && !more}<div class="card-b"><button class="btn" onclick={() => (more = true)}>{t('Show all {n}', { n: items.length })}</button></div>{/if}
    </div>
  {/if}

  {#if form}
    <details class="card sets" open={isOpen('all')} ontoggle={e => onToggle('all', e)}>
      <summary><h2>{t('Scheduled tests')}</h2><span class="muted sum">{summary}</span></summary>
      <div class="card-b">
        <p class="muted">{t('Tests move real data: keep size, interval and budget small on metered lines (LTE/5G).')}</p>

        <details class="sec" open={isOpen('sched')} ontoggle={e => onToggle('sched', e)}>
          <summary>{t('Schedule')}{#if dirty(SECTIONS[0][2])} <span class="badge warn">{t('unsaved')}</span>{/if}</summary>
          <div class="form">
            <label class="fld chk"><input type="checkbox" bind:checked={form.enabled} /> {t('Run scheduled speed tests')}</label>
            <label class="fld chk"><input type="checkbox" bind:checked={form.paused} /> {t('Pause (keeps the schedule)')}</label>
            <div class="fld"><span>{t('Providers for scheduled runs')}</span>
              <span class="inl">{#each ['cloudflare', 'hetzner', 'ndt7', 'ookla'] as pr}<label class="chk"><input type="checkbox" checked={form.providers.includes(pr)} onchange={() => (form.providers = toggle(form.providers, pr))} /> {provLabel[pr]}</label>{/each}</span>
              <span class="muted" style="font-weight:400">{t('Cloudflare, NDT7 and Ookla measure download, upload and latency; Hetzner download only. Licences and server choice: see Providers and licences.')}</span></div>
            <div class="fld"><span>{t('IP version')}</span>
              <span class="inl">{#each [['', 'auto'], ['4', 'IPv4'], ['6', 'IPv6']] as [v, label]}<label class="chk"><input type="checkbox" checked={form.families.includes(v)} onchange={() => (form.families = toggle(form.families, v))} /> {label}</label>{/each}</span></div>
            <label class="fld"><span>{t('Interval')}</span><select class="input" bind:value={form.interval_min}>{#each intervals as [v, label]}<option value={v}>{t(label)}</option>{/each}</select></label>
            <div class="fld"><span>{t('Only between (local time, empty = any time)')}</span>
              <span class="inl"><input class="input sm" type="time" bind:value={form.window_from} /> – <input class="input sm" type="time" bind:value={form.window_to} /> <span class="muted">{tzName}</span></span></div>
            <label class="fld"><span>{t('Start time (runs land on this time + n × interval, empty = interval after the last test)')}</span><input class="input sm" type="time" bind:value={form.start_at} /> <span class="muted">{tzName}</span></label>
            <label class="fld"><span>{t('Random delay up to (minutes)')}</span><input class="input sm" type="number" min="0" max="240" bind:value={form.jitter_min} /></label>
            {@render actions(SECTIONS[0][2])}
          </div>
        </details>

        <details class="sec" open={isOpen('limits')} ontoggle={e => onToggle('limits', e)}>
          <summary>{t('Test size and limits')}{#if dirty(SECTIONS[1][2])} <span class="badge warn">{t('unsaved')}</span>{/if}</summary>
          <div class="form">
            <div class="fld"><span>{t('Per test and direction')}</span>
              <span class="inl"><input class="input sm" type="number" min="5" max="2000" bind:value={form.max_mb} /> MB · <input class="input sm" type="number" min="3" max="60" bind:value={form.max_sec} /> s · <input class="input sm" type="number" min="1" max="8" bind:value={form.streams} /> {t('streams')}</span></div>
            <div class="fld"><span>{t('Data budget (0 = unlimited)')}</span>
              <span class="inl"><input class="input sm" type="number" min="0" bind:value={form.budget_day_mb} /> MB {t('per day')} · <input class="input sm" type="number" min="0" bind:value={form.budget_month_mb} /> MB {t('per month')}</span></div>
            <label class="fld"><span>{t('Skip when the line is busy: current traffic above … % of the last result (0 = never skip)')}</span><input class="input sm" type="number" min="0" max="100" bind:value={form.skip_busy_pct} /></label>
            {@render actions(SECTIONS[1][2])}
          </div>
        </details>

        <details class="sec" open={isOpen('contract')} ontoggle={e => onToggle('contract', e)}>
          <summary>{t('Contract and alerts')}{#if dirty(SECTIONS[2][2])} <span class="badge warn">{t('unsaved')}</span>{/if}</summary>
          <div class="form">
            <div class="fld"><span>{t('Contracted speed (Mbit/s, 0 = unknown)')}</span>
              <span class="inl"><input class="input sm" type="number" min="0" bind:value={form.contract_down_mbps} /> {t('down')} · <input class="input sm" type="number" min="0" bind:value={form.contract_up_mbps} /> {t('up')}</span></div>
            <div class="fld"><span>{t('Warn when … tests in a row stay below … % of the contract')}</span>
              <span class="inl"><input class="input sm" type="number" min="1" max="50" bind:value={form.alert_count} /> {t('tests')} · <input class="input sm" type="number" min="1" max="100" bind:value={form.alert_below_pct} /> %</span></div>
            {@render actions(SECTIONS[2][2])}
          </div>
        </details>

        <details class="sec" open={isOpen('prov')} ontoggle={e => onToggle('prov', e)}>
          <summary>{t('Providers and licences')}{#if dirty(SECTIONS[3][2])} <span class="badge warn">{t('unsaved')}</span>{/if}</summary>
          <div class="form">
            <label class="fld"><span>Hetzner ({t('download only')})</span><select class="input sm" bind:value={form.hetzner_server}>{#each x.hetzner_servers as s}<option value={s}>{s}</option>{/each}</select></label>
            <div class="fld"><span>M-Lab NDT7</span><span class="muted" style="font-weight:400">{t('M-Lab publishes every test result, including your public IP address, as open data.')}</span></div>
            <div class="fld"><span>Ookla ({t('official CLI, download, upload, latency')})</span>
              <span class="muted" style="font-weight:400">{t('mtmon does not ship the Ookla speedtest CLI: install it yourself (into the data directory under ookla/speedtest, or into PATH).')} {t('Ookla licence in short: personal, non-commercial use on a single personal computer; not on a router or server. Check that this fits before you enable it.')}</span>
              {#if status.ookla_missing}<span class="badge warn">{t('Ookla CLI not found: Ookla is skipped until it is installed.')}</span>{/if}
              <label class="chk"><input type="checkbox" bind:checked={form.ookla_accepted} /> {t('I accept the Ookla licence and privacy terms (needed to run it)')}</label>
              <span class="inl">{t('Server id (0 = automatic)')} <input class="input sm" type="number" min="0" bind:value={form.ookla_server_id} /></span></div>
            {@render actions(SECTIONS[3][2])}
          </div>
        </details>

        <details class="sec" open={isOpen('import')} ontoggle={e => onToggle('import', e)}>
          <summary>{t('Import')}</summary>
          <div class="form"><span class="muted">{t('Import the CSV or JSON export of MySpeed (Mbit/s, ms)')}</span>
            <label class="btn" style="justify-self:start">{t('Import MySpeed…')}<input type="file" accept=".csv,.json,text/csv,application/json" hidden onchange={importFile} /></label></div>
        </details>
      </div>
    </details>
  {/if}
{/if}

{#snippet actions(keys)}
  <div><button class="btn primary" disabled={!dirty(keys)} onclick={() => save(keys)}>{t('Save')}</button>
    {#if dirty(keys)}<button class="btn" onclick={() => discard(keys)}>{t('Discard')}</button>{/if}</div>
{/snippet}

<style>
  .form { display: grid; gap: 14px; max-width: 760px; padding: 10px 0 14px; }
  .quick { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
  .chip { display: inline-flex; align-items: center; gap: 6px; padding: 4px 10px; border: 1px solid var(--border); border-radius: 999px; cursor: pointer; font-size: 13px; }
  .chip.on { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 12%, transparent); }
  .chip input { margin: 0; }
  .sets > summary { display: flex; flex-wrap: wrap; gap: 4px 14px; align-items: baseline; padding: 12px 16px; cursor: pointer; list-style: none; }
  .sets > summary h2 { margin: 0; }
  .sum { overflow-wrap: anywhere; }
  .sec { border-top: 1px solid var(--border); }
  .sec > summary { padding: 10px 0; cursor: pointer; font-weight: 600; font-size: 14px; }
  .head { margin-bottom: 18px; }
  .toolbar { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin-bottom: 14px; }
  .spacer { flex: 1; }
  .kpi .v { font-size: 22px; font-weight: 600; margin: 2px 0; } .kpi .l { font-size: 12px; }
  .fld { display: grid; gap: 4px; font-weight: 500; font-size: 13px; }
  .chk { display: flex; align-items: center; gap: 8px; font-weight: 400; }
  .inl { display: inline-flex; flex-wrap: wrap; gap: 6px; align-items: center; font-weight: 400; }
  .inl .input { width: 86px; }
  .err { font-size: 11px; display: inline-block; max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; vertical-align: bottom; }
</style>
