<script>
  // Settings -> Audit log: who did what (logins, setting changes, tokens, devices, fixes, updates), 180 days (#192).
  import { api } from './api.js'
  import { toast } from './state.svelte.js'
  import { t, tr } from './i18n.svelte.js'
  import { datetime } from './format.js'
  const kinds = [['', 'All'], ['login', 'Logins'], ['settings', 'Settings'], ['token', 'API tokens'], ['device', 'Devices'], ['fix', 'Fixes'], ['update', 'Updates']]
  let kind = $state('')
  let rows = $state([])
  let more = $state(false)
  let keep = $state(180)
  let busy = $state(false)
  async function load(append) {
    busy = true
    try {
      const q = new URLSearchParams({ limit: '50' })
      if (kind) q.set('action', kind)
      if (append && rows.length) q.set('before', String(rows[rows.length - 1].id))
      const r = await api('/audit?' + q)
      rows = append ? [...rows, ...r.entries] : r.entries
      more = r.entries.length === 50
      keep = r.keep_days
    } catch (e) { toast(tr(e.message)) }
    busy = false
  }
  load(false)
  const label = a => (a.includes('.') ? a.replace(/\./g, ' · ') : a)
</script>
<div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Audit log')}</h2></div>
  <div class="card-b muted" style="font-size:13px">{t('Who did what: logins, setting changes, API tokens, devices, fixes and updates. Passwords and request contents are never logged. Entries are kept {n} days.', { n: keep })}</div>
  <div class="card-b row" style="gap:8px;flex-wrap:wrap">
    {#each kinds as [k, l]}<button type="button" class="btn sm" class:primary={kind === k} onclick={() => { kind = k; load(false) }}>{t(l)}</button>{/each}
  </div>
  {#if rows.length}
    <div class="scroll"><table><thead><tr><th>{t('Time')}</th><th>{t('User')}</th><th>{t('Action')}</th><th>{t('Details')}</th></tr></thead><tbody>
      {#each rows as e (e.id)}
        <tr><td class="muted">{datetime(e.ts)}</td><td>{e.user}<div class="muted mono" style="font-size:11px">{e.ip}</div></td><td class="mono">{label(e.action)}</td><td class="mono" style="font-size:12px;word-break:break-word">{e.detail}</td></tr>
      {/each}
    </tbody></table></div>
    {#if more}<button class="btn sm" style="margin:8px 16px 12px" disabled={busy} onclick={() => load(true)}>{t('Show more')}</button>{/if}
  {:else}<div class="empty">{t('No entries')}</div>{/if}
</div>
