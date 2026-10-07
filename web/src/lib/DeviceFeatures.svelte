<script>
  import { api } from './api.js'
  import { t, tr } from './i18n.svelte.js'
  // Switch the optional router-side parts of the auto setup on and off for one registered device.
  // Flow: toggle → "Preview" (nothing is changed yet) → "Apply". Admin login is needed once and never stored.
  // preset: feature keys to switch on right away (used by the one-click fix on the Suggestions page)
  let { name, onchanged, preset = [] } = $props()
  let data = $state(null), want = $state({}), err = $state(''), loading = $state(true)
  let user = $state('admin'), pass = $state(''), busy = $state(false), prev = $state(null), res = $state(null)
  let fwRules = $state([]), allowRules = $state([])

  async function load() {
    loading = true
    try {
      data = await api('/devices/' + encodeURIComponent(name) + '/features')
      want = Object.fromEntries(data.features.map(f => [f.key, f.on || (preset.includes(f.key) && f.available)]))
      err = ''
    } catch (e) { err = e.message } finally { loading = false }
  }
  load()

  const enable = $derived(data ? data.features.filter(f => want[f.key] && !f.on).map(f => f.key) : [])
  const disable = $derived(data ? data.features.filter(f => !want[f.key] && f.on).map(f => f.key) : [])
  const changed = $derived(enable.length + disable.length > 0)
  const dropRules = $derived((data?.filter_rules || []).filter(r => !r.disabled && !r.managed && (r.action === 'drop' || r.action === 'reject')))
  // accept rules that match every packet of an established connection would flood the log: listed, but not preselected
  const noisy = r => /established|related|untracked/.test(r.summary || '')
  const allowList = $derived((data?.filter_rules || []).filter(r => !r.disabled && !r.managed && r.action === 'accept'))
  const body = dryRun => ({ user, pass, enable, disable, fw_rules: fwRules, allow_rules: allowRules, dry_run: dryRun })
  // switching the firewall log off also switches off what builds on its log target
  function toggle(k) {
    want[k] = !want[k]
    if (k === 'firewall_logs' && !want[k]) { want.allow_log = false; want.log_new = false }
  }
  const pick = (list, id) => (list.includes(id) ? list.filter(x => x !== id) : [...list, id])
  $effect(() => { enable.length; disable.length; prev = null; res = null })  // a changed selection invalidates the preview
  $effect(() => { if (enable.includes('firewall_logs') && !fwRules.length) fwRules = dropRules.map(r => r.id) })
  $effect(() => { if (enable.includes('allow_log') && !allowRules.length) allowRules = allowList.filter(r => !r.log && !noisy(r)).map(r => r.id) })

  async function preview() {
    busy = true; err = ''; prev = null
    try { prev = await api('/devices/' + encodeURIComponent(name) + '/features', { method: 'POST', body: body(true) }) }
    catch (e) { err = e.message } finally { busy = false }
  }
  async function apply() {
    busy = true; err = ''
    try {
      res = await api('/devices/' + encodeURIComponent(name) + '/features', { method: 'POST', body: body(false) })
      pass = ''
      if (res.ok) { prev = null; await load(); onchanged?.() }
    } catch (e) { err = e.message } finally { busy = false }
  }
  const riskCls = { none: 'ok', low: '', medium: 'warn' }
</script>

{#if loading}<div class="muted">{t('Loading…')}</div>
{:else if data}
  <p class="muted" style="margin-top:0">{t('Choose what mtmon should set up on this device. Nothing changes until you preview and confirm. Switching a feature off undoes exactly what mtmon recorded for it.')}</p>
  <div class="feats">
    {#each data.features as f (f.key)}
      <div class="feat" class:dis={!f.available && !f.on}>
        <div class="fh">
          <div><b>{tr(f.title)}</b>
            {#if f.on}<span class="badge ok">{t('set up by mtmon')}</span>{:else if f.external}<span class="badge acc">{t('already active (set up outside mtmon)')}</span>{/if}</div>
          <button class="sw" class:on={want[f.key]} role="switch" aria-checked={want[f.key]} aria-label={tr(f.title)} disabled={busy || (!f.available && !f.on)} onclick={() => toggle(f.key)}><i></i></button>
        </div>
        <div class="muted fw">{tr(f.why)}{#if !f.available && !f.on} <i>{tr(f.unavailable_reason)}</i>{/if}</div>
        {#if f.key === 'firewall_logs' && want[f.key] && !f.on}
          <div class="sub">
            <div class="muted" style="font-size:12.5px;margin-bottom:6px">{t('Log on these drop rules:')}</div>
            {#each dropRules as r (r.id)}
              <label class="chk"><input type="checkbox" checked={fwRules.includes(r.id)} onchange={() => (fwRules = pick(fwRules, r.id))} />
                <span><b>{r.chain}/{r.action}</b> <span class="muted">{r.comment || r.summary}</span></span></label>
            {:else}<div class="muted" style="font-size:12.5px">{t('No drop rules found – only the log target is set up.')}</div>{/each}
          </div>
        {/if}
        {#if f.key === 'allow_log' && want[f.key] && !f.on}
          <div class="sub">
            <div class="muted" style="font-size:12.5px;margin-bottom:6px">{t('Log on these allow rules:')}</div>
            {#each allowList as r (r.id)}
              <label class="chk"><input type="checkbox" checked={allowRules.includes(r.id)} onchange={() => (allowRules = pick(allowRules, r.id))} />
                <span><b>{r.chain}/{r.action}</b> <span class="muted">{r.comment || r.summary}</span>{#if noisy(r)} <span class="badge warn">{t('noisy')}</span>{/if}{#if r.log} <span class="badge">{t('already logs')}</span>{/if}</span></label>
            {:else}<div class="muted" style="font-size:12.5px">{t('No allow rules found.')}</div>{/each}
            <div class="muted" style="font-size:12px;margin-top:6px">{t('The firewall log target is set up as well if it is missing. “Noisy” rules match every packet of established connections.')}</div>
          </div>
        {/if}
      </div>
    {/each}
  </div>

  {#if changed}
    <div class="creds">
      <div class="muted" style="font-size:12.5px;margin-bottom:6px">{t('Admin login of the router – used once for this change, never stored.')}</div>
      <div class="row"><label>{t('User')}<input class="input" bind:value={user} autocomplete="off" /></label>
        <label>{t('Password')}<input class="input" type="password" bind:value={pass} autocomplete="new-password" /></label></div>
    </div>
  {/if}
  {#if err}<div class="warnbox" style="margin-top:12px">{tr(err)}</div>{/if}

  {#if prev}
    <div class="prev">
      <b>{t('What will happen')}</b>
      {#if prev.will_undo?.length}<div class="undo">{t('Reverted:')}<ul>{#each prev.will_undo as u}<li>{tr(u)}</li>{/each}</ul></div>{/if}
      {#each prev.plan || [] as st (st.key)}
        <details class="stp"><summary><span class="badge {riskCls[st.risk]}">{st.risk === 'none' ? t('no impact') : st.risk === 'low' ? t('low') : t('medium')}</span> {tr(st.title)}</summary>
          <p class="muted" style="margin:6px 0">{tr(st.why)}</p>
          <pre class="code">{st.commands.join('\n')}</pre>
          <div class="muted" style="font-size:12.5px;margin-top:4px">{t('Undo:')} {tr(st.undo)}</div></details>
      {/each}
    </div>
  {/if}
  {#if res}
    <div class="warnbox" style="margin-top:12px;background:{res.ok ? 'var(--ok-bg)' : 'var(--bad-bg)'}">
      {res.ok ? t('Done.') : t('Not everything worked – changes of this attempt were undone where possible.')}
      {#each (res.result?.steps || []).concat(res.undone || []) as s}<div style="font-size:12.5px"><span class="badge {s.ok ? 'ok' : 'bad'}">{s.ok ? '✓' : '✗'}</span> {tr(s.title)}{s.msg ? ' – ' + tr(s.msg) : ''}</div>{/each}
      {#if res.result?.error}<div style="font-size:12.5px">{tr(res.result.error)}</div>{/if}
    </div>
    {#if res.manual_script}<pre class="code" style="margin-top:10px">{res.manual_script}</pre>{/if}
  {/if}

  <div class="acts">
    <button class="btn" disabled={!changed || busy || !user || !pass} onclick={preview}>{busy && !prev ? t('Checking …') : t('Preview changes')}</button>
    {#if prev}<button class="btn primary" disabled={busy || (!prev.plan?.length && !prev.will_undo?.length)} onclick={apply}>{busy ? t('Applying …') : t('Apply')}</button>{/if}
  </div>
{:else if err}<div class="warnbox">{tr(err)}</div>{/if}

<style>
  .feats { display: flex; flex-direction: column; gap: 10px; }
  .feat { border: 1px solid var(--border); border-radius: 10px; padding: 10px 12px; background: var(--card-2); }
  .feat.dis { opacity: .6; }
  .fh { display: flex; justify-content: space-between; align-items: center; gap: 10px; } .fh .badge { margin-left: 6px; }
  .fw { font-size: 12.5px; margin-top: 4px; } .sub { margin-top: 10px; padding-top: 8px; border-top: 1px dashed var(--border-2); }
  label.chk { display: flex; gap: 8px; align-items: flex-start; font-size: 13px; margin: 3px 0; } label.chk input { margin-top: 3px; }
  .creds { margin-top: 14px; padding: 10px 12px; border: 1px solid var(--border); border-radius: 10px; }
  .row { display: flex; gap: 12px; flex-wrap: wrap; } .row label { flex: 1; min-width: 150px; display: grid; gap: 4px; font-weight: 500; font-size: 13px; }
  .prev { margin-top: 14px; display: flex; flex-direction: column; gap: 8px; } .stp { border: 1px solid var(--border); border-radius: 8px; padding: 8px 10px; } .stp summary { cursor: pointer; }
  .undo ul { margin: 4px 0 0; padding-left: 18px; font-size: 13px; }
  .acts { display: flex; gap: 8px; margin-top: 14px; flex-wrap: wrap; }
</style>
