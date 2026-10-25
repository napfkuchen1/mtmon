<script>
  // Settings → Groups: client groups with their own alert behaviour (IoT, Family, Servers, Guests, Lab, …).
  import { api } from './api.js'
  import { toast } from './state.svelte.js'
  import { poll } from './Poll.svelte.js'
  import { t, tr } from './i18n.svelte.js'
  import { bytes } from './format.js'
  const gl = poll(() => api('/groups'), 0)
  const groups = $derived(gl.data?.groups || [])
  let name = $state('')
  async function reload() { gl.data = await api('/groups') }
  async function save(g, patch) {
    try { await api('/groups/' + encodeURIComponent(g.name), { method: 'PUT', body: { ...g, ...patch } }); await reload() } catch (e) { toast(tr(e.message)) }
  }
  async function add() {
    const n = name.trim(); if (!n) return
    try { await api('/groups/' + encodeURIComponent(n), { method: 'PUT', body: { new_alert: true, chatty_mult: 1 } }); name = ''; await reload() } catch (e) { toast(tr(e.message)) }
  }
  async function presets() { try { const r = await api('/groups/presets', { method: 'POST', body: {} }); toast(t('{n} groups added', { n: r.added })); await reload() } catch (e) { toast(tr(e.message)) } }
  async function remove(g) {
    if (!confirm(t('Delete the group {g}? Its clients stay, they just lose the group.', { g: g.name }))) return
    try { await api('/groups/' + encodeURIComponent(g.name), { method: 'DELETE' }); await reload() } catch (e) { toast(tr(e.message)) }
  }
</script>

<div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Client groups')}</h2>
  <span class="muted" style="font-size:12px">{t('One threshold for phones, servers and smart plugs gives false alarms and blind spots. Put clients into groups (Clients page) and tune what each group triggers.')}</span></div>
  {#if groups.length}
    <div class="scroll"><table>
      <thead><tr><th>{t('Group')}</th><th class="r">{t('Clients')}</th><th title={t('A new client of this group raises an alert')}>{t('New-client alert')}</th><th title={t('Multiplies the limit for “unusually many connections”')}>{t('Connections limit ×')}</th>
        <th title={t('Traffic spikes caused by its clients are recorded as info')}>{t('Spikes as info')}</th><th title={t('A weak Wi-Fi signal is normal for these devices')}>{t('Ignore weak signal')}</th><th title={t('Clients put into the group are watched (alert when offline)')}>{t('Watch members')}</th><th></th></tr></thead>
      <tbody>{#each groups as g (g.name)}
        <tr><td><b>{g.name}</b></td><td class="r num">{g.online}/{g.clients}</td>
          <td><input type="checkbox" checked={g.new_alert} onchange={e => save(g, { new_alert: e.currentTarget.checked })} aria-label={t('New-client alert')} /></td>
          <td><input class="input" type="number" min="0.1" step="0.5" style="width:70px" value={g.chatty_mult} onchange={e => save(g, { chatty_mult: +e.currentTarget.value })} aria-label={t('Connections limit ×')} /></td>
          <td><input type="checkbox" checked={g.spike_info} onchange={e => save(g, { spike_info: e.currentTarget.checked })} aria-label={t('Spikes as info')} /></td>
          <td><input type="checkbox" checked={g.ignore_weak} onchange={e => save(g, { ignore_weak: e.currentTarget.checked })} aria-label={t('Ignore weak signal')} /></td>
          <td><input type="checkbox" checked={g.watch_default} onchange={e => save(g, { watch_default: e.currentTarget.checked })} aria-label={t('Watch members')} /></td>
          <td class="r"><button class="btn sm" onclick={() => remove(g)}>{t('Delete')}</button></td></tr>{/each}</tbody></table></div>
  {/if}
  <div class="card-b" style="display:flex;gap:8px;flex-wrap:wrap;align-items:center;border-top:1px solid var(--border)">
    <input class="input" style="width:200px" placeholder={t('New group name')} bind:value={name} maxlength="32" aria-label={t('New group name')} onkeydown={e => e.key === 'Enter' && add()} />
    <button class="btn" onclick={add} disabled={!name.trim()}>{t('Add group')}</button>
    <button class="btn" onclick={presets}>{t('Add standard groups (IoT, Family, Servers, Guests, Lab)')}</button></div>
</div>
