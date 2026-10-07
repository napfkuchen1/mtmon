<script>
  import Avatar from '../lib/Avatar.svelte'
  import Empty from '../lib/Empty.svelte'
  import { api } from '../lib/api.js'
  import { go, toast } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { ago, display } from '../lib/format.js'
  import { t, tr } from '../lib/i18n.svelte.js'

  let mode = $state('new'), sel = $state({}), edits = $state({}), pattern = $state(''), busy = $state(false)
  const d = poll(() => api('/clients/inbox?mode=' + mode), 10000)
  const rows = $derived(d.data?.clients || [])
  const picked = $derived(rows.filter(r => sel[r.mac]))
  const confCls = { medium: 'acc', low: '' }
  const confTxt = c => ({ medium: t('Confidence: medium'), low: t('Confidence: low') }[c] || '')

  async function reload() {
    try { d.data = await api('/clients/inbox?mode=' + mode); d.error = '' } catch (e) { d.error = e.message }
    window.dispatchEvent(new Event('mtmon-inbox-changed'))
  }
  function setMode(m) { mode = m; sel = {}; edits = {}; reload() }
  const labelOf = r => edits[r.mac] ?? r.label ?? ''
  async function save(items, msg) {
    if (!items.length) return
    busy = true
    try {
      const r = await api('/clients/review', { method: 'POST', body: { items } })
      toast(msg || t('{n} devices saved', { n: r.updated }))
      for (const i of items) { delete sel[i.mac]; delete edits[i.mac] }
      await reload()
    } catch (e) { toast(tr(e.message)) } finally { busy = false }
  }
  const saveRow = r => save([{ mac: r.mac, label: labelOf(r).trim() }])
  const acceptSuggestions = () => save(picked.map(r => ({ mac: r.mac, label: labelOf(r).trim() || r.suggest || '' })), t('Suggestions applied'))
  const markReviewed = () => save(picked.map(r => ({ mac: r.mac, label: labelOf(r).trim() })), t('Marked as reviewed'))
  function applyPattern() {
    if (!pattern.trim()) return
    let n = 1
    for (const r of picked) edits[r.mac] = pattern.includes('{n}') ? pattern.replace('{n}', String(n++)) : pattern.trim() + ' ' + n++
  }
  const allOn = $derived(rows.length > 0 && rows.every(r => sel[r.mac]))
  function toggleAll() { const on = !allOn; sel = on ? Object.fromEntries(rows.map(r => [r.mac, true])) : {} }
</script>

<div class="head"><h1>{t('New devices')}</h1>
  <span class="muted">{mode === 'new' ? t('Devices that appeared on the network – confirm the ones you know, name the rest') : t('Devices that only have a MAC address – a name makes them recognisable everywhere')}</span></div>

<div class="card">
  <div class="card-h">
    <div class="tabs" role="tablist">
      <button role="tab" class:on={mode === 'new'} aria-selected={mode === 'new'} onclick={() => setMode('new')}>{t('New')}{#if d.data?.new} ({d.data.new}){/if}</button>
      <button role="tab" class:on={mode === 'unnamed'} aria-selected={mode === 'unnamed'} onclick={() => setMode('unnamed')}>{t('MAC only')}</button>
    </div>
    {#if picked.length}
      <div class="bulk">
        <b>{t('{n} selected', { n: picked.length })}</b>
        <input class="input sm" placeholder={t('Name pattern, e.g. Kids-{n}')} bind:value={pattern} aria-label={t('Name pattern')} onkeydown={e => e.key === 'Enter' && applyPattern()} />
        <button class="btn sm" onclick={applyPattern} disabled={!pattern.trim()}>{t('Fill names')}</button>
        <button class="btn sm" onclick={acceptSuggestions} disabled={busy}>{t('Accept suggestions')}</button>
        <button class="btn sm primary" onclick={markReviewed} disabled={busy}>{t('Save selected')}</button>
      </div>
    {/if}
  </div>
  {#if d.error}<div class="warnbox" style="margin:12px">{tr(d.error)}</div>{/if}
  <div class="scroll">
    <table>
      <thead><tr><th style="width:32px"><input type="checkbox" checked={allOn} onchange={toggleAll} aria-label={t('Select all')} /></th>
        <th>{t('Device')}</th><th>{t('Connected to')}</th><th>{t('First seen')}</th><th>{t('Name')}</th><th></th></tr></thead>
      <tbody>
        {#each rows as r (r.mac)}
          <tr>
            <td><input type="checkbox" bind:checked={sel[r.mac]} aria-label={t('Select')} /></td>
            <td><div style="display:flex;gap:10px;align-items:center"><Avatar client={r} online={r.online} size={30} />
              <div><a href="#/client/{encodeURIComponent(r.mac)}"><b>{display(r)}</b></a>
                <div class="muted mono" style="font-size:11.5px">{r.mac}{#if r.vendor} · {tr(r.vendor)}{/if}{#if r.ip} · {r.ip}{/if}</div></div></div></td>
            <td>{#if r.device}{r.device}{#if r.wifi}<div><span class="badge acc">{r.ssid || t('Wi-Fi')}</span></div>{/if}{:else}<span class="muted">—</span>{/if}</td>
            <td class="muted">{ago(r.first_seen)}</td>
            <td>
              <input class="input sm" style="min-width:200px" placeholder={r.suggest || r.hostname || t('Name…')} value={labelOf(r)} maxlength="64" aria-label={t('Client label')}
                oninput={e => (edits[r.mac] = e.target.value)} onkeydown={e => e.key === 'Enter' && saveRow(r)} />
              {#if r.suggest && !labelOf(r)}
                <div><button class="chip" title={tr(r.reason)} onclick={() => (edits[r.mac] = r.suggest)}>{t('Suggestion')}: <b>{r.suggest}</b></button>
                  <span class="badge {confCls[r.conf]}">{confTxt(r.conf)}</span></div>
              {/if}
            </td>
            <td class="r"><button class="btn sm" disabled={busy} onclick={() => saveRow(r)}>{labelOf(r).trim() ? t('Save') : t('OK, known')}</button></td>
          </tr>
        {/each}
      </tbody>
    </table>
    {#if !rows.length}{#if d.loading}<div class="empty">{t('Loading…')}</div>{:else}<Empty icon="bulb" title={mode === 'new' ? t('No new devices – everything has been looked at') : t('Every device has a name')} hint={mode === 'new' ? t('New devices show up here the moment they join the network.') : ''} />{/if}{/if}
  </div>
</div>

<style>
  .head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 18px; flex-wrap: wrap; }
  .bulk { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin-left: auto; }
  .chip { border: 1px dashed var(--accent); background: var(--accent-bg); color: var(--text); border-radius: 999px; padding: 1px 10px; font-size: 12px; cursor: pointer; margin-top: 4px; }
  .chip:hover { filter: brightness(.97); }
  .r { text-align: right; }
</style>
