<script>
  import { api } from '../lib/api.js'
  import { toast } from '../lib/state.svelte.js'
  import { poll } from '../lib/Poll.svelte.js'
  import { ago, datetime } from '../lib/format.js'
  import { t, tr } from '../lib/i18n.svelte.js'

  const d = poll(() => api('/suggestions'), 30000)
  let cat = $state('all'), sev = $state('all'), showHidden = $state(false), busy = $state(false)

  const cats = ['Performance', 'Security', 'Wi-Fi', 'Network hygiene', 'Monitoring quality']
  const sevs = ['warning', 'tip', 'info']
  const sevLabel = s => ({ warning: t('Warning'), tip: t('Tip'), info: t('Info') }[s] || s)
  const sevCls = { warning: 'warn', tip: 'acc', info: '' }
  const confLabel = c => ({ low: t('Confidence: low'), medium: t('Confidence: medium') }[c] || '')

  const all = $derived(d.data?.suggestions || [])
  const visible = $derived(all.filter(s => (showHidden || !s.hidden) && (cat === 'all' || s.category === cat) && (sev === 'all' || s.severity === sev)))
  const counts = $derived(d.data?.counts || { warning: 0, tip: 0, info: 0, hidden: 0 })
  const open = $derived(all.filter(s => !s.hidden).length)
  const skipped = $derived(d.data?.skipped || [])

  async function reload(force) {
    busy = true
    try { d.data = await api('/suggestions' + (force ? '?refresh=1' : '')); d.error = '' } catch (e) { d.error = e.message }
    busy = false
    window.dispatchEvent(new Event('mtmon-suggestions-changed'))
  }
  async function hide(s, days) {
    try {
      await api(`/suggestions/${encodeURIComponent(s.id)}/dismiss`, { method: 'POST', body: { days } })
      await reload(false)
      toast(days ? t('Snoozed for {n} days', { n: days }) : t('Hidden'))
    } catch (e) { toast(e.message) }
  }
  async function restore(s) {
    try { await api(`/suggestions/${encodeURIComponent(s.id)}/restore`, { method: 'POST' }); await reload(false) } catch (e) { toast(e.message) }
  }
  async function copy(text) {
    try { await navigator.clipboard.writeText(text); toast(t('Copied')) } catch { toast(t('Copy failed – select the text manually')) }
  }
  const href = l => '#/' + l.split('/').map(encodeURIComponent).join('/')
  const linkLabel = l => {
    const k = l.split('/')[0]
    return { clients: t('Open Clients'), client: t('Open client'), device: t('Open device'), firewall: t('Open Firewall'), insights: t('Open Insights') }[k] || t('Open')
  }
  const until = s => (s.hidden_until ? t('snoozed until {date}', { date: datetime(s.hidden_until) }) : t('hidden'))
</script>

<div class="head">
  <h1>{t('Suggestions')}</h1>
  <span class="muted">{t('Derived from the data mtmon already collects · checked on demand')}</span>
  <button class="btn" style="margin-left:auto" disabled={busy} onclick={() => reload(true)}>{t('Check again')}</button>
</div>

<div class="filters">
  <label>{t('Category')}
    <select class="input sm" bind:value={cat}>
      <option value="all">{t('All')}</option>
      {#each cats as c}<option value={c}>{t(c)}</option>{/each}
    </select></label>
  <label>{t('Severity')}
    <select class="input sm" bind:value={sev}>
      <option value="all">{t('All')}</option>
      {#each sevs as s}<option value={s}>{sevLabel(s)} ({counts[s]})</option>{/each}
    </select></label>
  <label class="chk"><input type="checkbox" bind:checked={showHidden} /> {t('Show hidden')}{#if counts.hidden}&nbsp;({counts.hidden}){/if}</label>
  {#if d.data}<span class="muted" style="margin-left:auto">{t('Updated {when}', { when: ago(d.data.generated_at) })}</span>{/if}
</div>

{#if d.error}<div class="warnbox" style="margin-bottom:14px">{d.error}</div>{/if}

{#if d.loading}
  <div class="card"><div class="empty">{t('Loading…')}</div></div>
{:else if !visible.length}
  <div class="card"><div class="empty big">
    <svg width="34" height="34" viewBox="0 0 24 24" fill="none" stroke="var(--ok)" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M8 12.5l3 3 5-6"/></svg>
    {#if open}
      <b>{t('No suggestion matches the current filters.')}</b>
    {:else}
      <b>{t('Nothing to improve right now')}</b>
      <span>{counts.hidden && !showHidden ? t('{n} hidden suggestions are not shown.', { n: counts.hidden }) : t('mtmon will list suggestions here as soon as it finds something worth a look.')}</span>
    {/if}
  </div></div>
{:else}
  <div class="list">
    {#each visible as s (s.id)}
      <article class="card sg" class:dim={s.hidden}>
        <div class="top">
          <span class="badge {sevCls[s.severity]}">{sevLabel(s.severity)}</span>
          <span class="badge">{t(s.category)}</span>
          {#if confLabel(s.confidence)}<span class="badge" title={tr(s.limits || '')}>{confLabel(s.confidence)}</span>{/if}
          {#if s.hidden}<span class="muted">{until(s)}</span>{/if}
        </div>
        <h2>{tr(s.title)}</h2>
        <p class="why">{tr(s.why)}</p>
        {#if s.evidence.length}
          <div class="lbl">{t('What we saw')}</div>
          <ul class="ev">{#each s.evidence as e}<li>{tr(e)}</li>{/each}</ul>
        {/if}
        {#if s.steps.length || s.commands}
          <details>
            <summary>{t('How to fix')}</summary>
            {#if s.steps.length}<ol class="steps">{#each s.steps as st}<li>{tr(st)}</li>{/each}</ol>{/if}
            {#if s.commands}
              <div class="cmdh"><span class="muted">{t('RouterOS terminal')}</span><button class="btn sm" onclick={() => copy(s.commands)}>{t('Copy')}</button></div>
              <pre class="code">{s.commands}</pre>
              <div class="muted note">{t('Review the commands before you run them, and adapt names and addresses to your setup. mtmon never changes your devices on its own here.')}</div>
            {/if}
          </details>
        {/if}
        {#if s.limits}<div class="muted note">{t('Limits')}: {tr(s.limits)}</div>{/if}
        <div class="acts">
          {#if s.link}<a class="btn sm" href={href(s.link)}>{linkLabel(s.link)}</a>{/if}
          <span class="sp"></span>
          {#if s.hidden}
            <button class="btn sm" onclick={() => restore(s)}>{t('Show again')}</button>
          {:else}
            <button class="btn sm" onclick={() => hide(s, 7)}>{t('Snooze 7 days')}</button>
            <button class="btn sm" onclick={() => hide(s, 0)}>{t('Hide')}</button>
          {/if}
        </div>
      </article>
    {/each}
  </div>
{/if}

{#if skipped.length}
  <details class="skip muted">
    <summary>{t('{n} checks could not run (not enough data)', { n: skipped.length })}</summary>
    <ul>{#each skipped as k}<li><span class="mono">{k.rule}</span> – {tr(k.reason)}</li>{/each}</ul>
  </details>
{/if}

<style>
  .head { display: flex; align-items: baseline; gap: 14px; margin-bottom: 14px; flex-wrap: wrap; }
  .filters { display: flex; flex-wrap: wrap; gap: 14px; align-items: center; margin-bottom: 14px; color: var(--muted); }
  .filters label { display: inline-flex; align-items: center; gap: 8px; }
  .filters .chk { color: var(--text); }
  .filters select { padding: 4px 8px; }
  .list { display: flex; flex-direction: column; gap: 12px; }
  .sg { padding: 16px 18px; }
  .sg.dim { opacity: .62; }
  .top { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; margin-bottom: 8px; }
  h2 { font-size: 16px; letter-spacing: -.005em; }
  .why { margin: 6px 0 10px; color: var(--muted); max-width: 78ch; }
  .lbl { font-size: 12px; font-weight: 600; color: var(--muted); text-transform: uppercase; letter-spacing: .04em; margin-bottom: 4px; }
  .ev { margin: 0 0 10px; padding-left: 18px; }
  .ev li { margin: 2px 0; overflow-wrap: anywhere; }
  details { margin: 8px 0; }
  summary { cursor: pointer; font-weight: 600; color: var(--accent-strong); }
  .steps { margin: 8px 0; padding-left: 20px; max-width: 80ch; }
  .steps li { margin: 3px 0; }
  .cmdh { display: flex; justify-content: space-between; align-items: center; margin: 8px 0 4px; }
  .note { font-size: 12.5px; margin-top: 6px; }
  .acts { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin-top: 12px; padding-top: 12px; border-top: 1px solid var(--border); }
  .sp { flex: 1; }
  .empty.big { display: flex; flex-direction: column; align-items: center; gap: 8px; padding: 44px 16px; }
  .empty.big b { color: var(--text); font-size: 16px; }
  .skip { margin-top: 18px; font-size: 12.5px; }
  .skip summary { color: var(--muted); font-weight: 500; }
  @media (max-width: 600px) { .sg { padding: 14px; } .acts .sp { display: none; } }
</style>
