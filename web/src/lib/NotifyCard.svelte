<script>
  // Settings -> Notifications: webhook, ntfy and e-mail with quiet hours per channel and a test button (#193).
  import { api } from './api.js'
  import { toast } from './state.svelte.js'
  import { t, tr, i18n } from './i18n.svelte.js'
  let f = $state(null)
  let secret = $state('')
  let pass = $state('')
  let busy = $state('')
  async function load() {
    try { f = await api('/notify') } catch (e) { toast(tr(e.message)) }
  }
  load()
  const day = d => new Date(2024, 0, 7 + d).toLocaleDateString(i18n.locale, { weekday: 'short' })
  const order = [1, 2, 3, 4, 5, 6, 0]
  function toggle(q, d) {
    const s = new Set(q.days || [])
    s.has(d) ? s.delete(d) : s.add(d)
    q.days = [...s].sort()
  }
  function body() {
    return {
      webhook: { url: f.webhook.url.trim(), secret, quiet: f.webhook.quiet },
      ntfy: { url: f.ntfy.url.trim(), quiet: f.ntfy.quiet },
      smtp: { host: f.smtp.host.trim(), port: Number(f.smtp.port) || 0, user: f.smtp.user, pass, from: f.smtp.from.trim(), to: f.smtp.to.trim(), quiet: f.smtp.quiet },
    }
  }
  async function save() {
    busy = 'save'
    try { f = await api('/notify', { method: 'PUT', body: body() }); secret = ''; pass = ''; toast(t('Saved')) } catch (e) { toast(tr(e.message)) }
    busy = ''
  }
  async function test(ch) {
    busy = ch
    try {
      await api('/notify', { method: 'PUT', body: body() })
      const r = await api('/notify/test?channel=' + ch, { method: 'POST' })
      toast(r.ok ? t('Test message sent') : t('Test failed: {err}', { err: r.error }))
    } catch (e) { toast(tr(e.message)) }
    busy = ''
  }
</script>
{#snippet quiet(q)}
  <div class="row" style="gap:8px;flex-wrap:wrap;align-items:center;margin-top:8px">
    <label class="chk"><input type="checkbox" bind:checked={q.enabled} /> {t('Quiet hours')}</label>
    {#if q.enabled}
      <input type="time" bind:value={q.from} aria-label={t('From')} /> – <input type="time" bind:value={q.to} aria-label={t('To')} />
      <span class="row" style="gap:4px;flex-wrap:wrap">
        {#each order as d}<button type="button" class="btn sm" class:primary={!q.days?.length || q.days.includes(d)} onclick={() => toggle(q, d)}>{day(d)}</button>{/each}
      </span>
    {/if}
  </div>
{/snippet}
{#if f}
  <div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Notifications')}</h2></div>
    <div class="card-b muted" style="font-size:13px">{t('Alerts can go to a webhook, ntfy and e-mail. In the quiet hours of a channel, warnings and info are held and sent as one summary afterwards; critical alerts always go out at once. Weekdays: none selected = every day; a night window belongs to the day it starts.')}</div>
    <div class="card-b">
      <h3>Webhook</h3>
      <div class="row" style="gap:8px;flex-wrap:wrap">
        <input bind:value={f.webhook.url} placeholder="https://…" aria-label="Webhook URL" style="flex:1;min-width:220px" />
        <input type="password" bind:value={secret} autocomplete="off" placeholder={f.webhook.secret_set ? t('Secret stored – leave empty to keep') : t('Signing secret (optional)')} aria-label={t('Signing secret')} style="min-width:200px" />
        <button class="btn sm" disabled={!f.webhook.url.trim() || busy} onclick={() => test('webhook')}>{t('Send test')}</button>
      </div>
      {@render quiet(f.webhook.quiet)}
    </div>
    <div class="card-b">
      <h3>ntfy</h3>
      <div class="row" style="gap:8px;flex-wrap:wrap">
        <input bind:value={f.ntfy.url} placeholder="https://ntfy.sh/topic" aria-label="ntfy URL" style="flex:1;min-width:220px" />
        <button class="btn sm" disabled={!f.ntfy.url.trim() || busy} onclick={() => test('ntfy')}>{t('Send test')}</button>
      </div>
      {@render quiet(f.ntfy.quiet)}
    </div>
    <div class="card-b">
      <h3>{t('E-mail')}</h3>
      <div class="row" style="gap:8px;flex-wrap:wrap">
        <input bind:value={f.smtp.host} placeholder="smtp.example.com" aria-label={t('SMTP server')} style="flex:1;min-width:160px" />
        <input type="number" bind:value={f.smtp.port} placeholder="587" aria-label={t('Port')} style="width:90px" />
        <input bind:value={f.smtp.user} placeholder={t('User')} aria-label={t('User')} autocomplete="off" style="min-width:140px" />
        <input type="password" bind:value={pass} autocomplete="off" placeholder={f.smtp.pass_set ? t('Password stored – leave empty to keep') : t('Password')} aria-label={t('Password')} style="min-width:160px" />
      </div>
      <div class="row" style="gap:8px;flex-wrap:wrap;margin-top:8px">
        <input bind:value={f.smtp.from} placeholder={t('Sender')} aria-label={t('Sender')} style="flex:1;min-width:180px" />
        <input bind:value={f.smtp.to} placeholder={t('Recipients (comma separated)')} aria-label={t('Recipients')} style="flex:1;min-width:180px" />
        <button class="btn sm" disabled={!f.smtp.host.trim() || !f.smtp.to.trim() || busy} onclick={() => test('smtp')}>{t('Send test')}</button>
      </div>
      {@render quiet(f.smtp.quiet)}
    </div>
    <div class="card-b"><button class="btn primary" disabled={!!busy} onclick={save}>{t('Save')}</button></div>
  </div>
{/if}
