<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script>
  // Hardware health of one device (#300): SFP optics, PoE-out ports, LTE signal. Shown only when the device has
  // such hardware. A value the router did not report is "—", never 0.
  import { api } from './api.js'
  import { t, tr } from './i18n.svelte.js'
  import { app, toast } from './state.svelte.js'
  import { ago } from './format.js'
  import Chart from './Chart.svelte'

  let { hw } = $props()
  const num = (v, unit, dec = 1) => (v === null || v === undefined ? '—' : Number(v).toFixed(dec) + ' ' + unit)
  const limit = $derived(typeof hw?.sfp_rx_warn_dbm === 'number' ? hw.sfp_rx_warn_dbm : null)
  const low = p => limit !== null && p.rx_dbm !== null && p.rx_dbm !== undefined && p.rx_dbm < limit
  const colors = ['var(--accent)', 'var(--down)', 'var(--up)', 'var(--bad)']
  const series = $derived.by(() => {
    const by = new Map()
    for (const s of hw?.sfp_history || []) {
      if (s.rx_dbm === null || s.rx_dbm === undefined) continue
      if (!by.has(s.iface)) by.set(s.iface, [])
      by.get(s.iface).push({ x: s.ts, y: s.rx_dbm })
    }
    return [...by.entries()].map(([n, points], i) => ({ name: n, color: colors[i % colors.length], points }))
  })
  let edit = $state(null)
  const field = $derived(edit === null ? (limit === null ? '' : String(limit)) : edit)
  async function save() {
    const txt = String(field).trim()
    const v = txt === '' ? null : Number(txt)
    if (v !== null && !Number.isFinite(v)) { toast(t('Enter a number in dBm')); return }
    try {
      await api('/hardware-settings', { method: 'PUT', body: { sfp_rx_warn_dbm: v } })
      toast(t('Saved')); edit = null
    } catch (e) { toast(tr(e.message)) }
  }
  const poeLabel = p => p.status || '—'
</script>

{#if hw}
  <div class="card" style="margin-top:16px">
    <div class="card-h"><h2>{t('Hardware health')}</h2><span class="muted" style="font-size:12.5px">{t('read every 5 minutes')}{hw.ts ? ' · ' + ago(hw.ts) : ''}</span></div>

    {#if hw.sfp?.length}
      <div class="card-b" style="padding-bottom:0"><b>{t('SFP modules')}</b></div>
      <div class="scroll"><table>
        <thead><tr><th>{t('Port')}</th><th class="r">{t('Receive power')}</th><th class="r">{t('Transmit power')}</th><th class="r">{t('Temperature')}</th><th></th></tr></thead>
        <tbody>
          {#each hw.sfp as p (p.iface)}
            <tr><td><b>{p.iface}</b></td><td class="r num">{num(p.rx_dbm, 'dBm')}</td><td class="r num">{num(p.tx_dbm, 'dBm')}</td><td class="r num">{num(p.temp_c, '°C', 0)}</td>
              <td>{#if low(p)}<span class="badge warn">{t('below limit')}</span>{/if}</td></tr>
          {/each}
        </tbody></table></div>
      {#if series.length}
        <div class="card-b"><Chart {series} fmt={v => v.toFixed(1) + ' dBm'} height={130} empty={t('Collecting metrics (1 sample/min)…')} /></div>
      {/if}
      {#if app.role === 'admin'}
        <div class="card-b row" style="gap:8px;flex-wrap:wrap;align-items:center;font-size:12.5px">
          <label for="sfp-limit">{t('Warn when receive power is below')}</label>
          <input id="sfp-limit" type="text" inputmode="decimal" style="width:90px" placeholder="—" value={field} oninput={e => (edit = e.currentTarget.value)} />
          <span class="muted">dBm</span>
          <button class="btn sm" onclick={save}>{t('Save')}</button>
          <span class="muted">{t('Empty = no warning.')}</span>
        </div>
      {/if}
    {/if}

    {#if hw.poe?.length}
      <div class="card-b" style="padding-bottom:0"><b>{t('PoE ports')}</b></div>
      <div class="scroll"><table>
        <thead><tr><th>{t('Port')}</th><th>{t('Status')}</th><th class="r">{t('Power')}</th></tr></thead>
        <tbody>
          {#each hw.poe as p (p.iface)}
            <tr><td><b>{p.iface}</b></td><td><span class="badge" class:bad={p.fault} class:ok={!p.fault}>{poeLabel(p)}</span></td>
              <td class="r num">{num(p.power_w, 'W')}</td></tr>
          {/each}
        </tbody></table></div>
    {/if}

    {#if hw.lte?.length}
      <div class="card-b" style="padding-bottom:0"><b>{t('LTE signal')}</b></div>
      <div class="scroll"><table>
        <thead><tr><th>{t('Interface')}</th><th class="r">RSRP</th><th class="r">RSRQ</th><th class="r">SINR</th><th class="r">RSSI</th></tr></thead>
        <tbody>
          {#each hw.lte as l (l.iface)}
            <tr><td><b>{l.iface}</b> <span class="muted">{l.tech || ''}</span></td><td class="r num">{num(l.rsrp, 'dBm', 0)}</td><td class="r num">{num(l.rsrq, 'dB', 0)}</td>
              <td class="r num">{num(l.sinr, 'dB', 0)}</td><td class="r num">{num(l.rssi, 'dBm', 0)}</td></tr>
          {/each}
        </tbody></table></div>
    {/if}
  </div>
{/if}
