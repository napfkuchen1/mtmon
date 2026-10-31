<!-- SPDX-License-Identifier: AGPL-3.0-only -->
<script>
  // Settings -> Internet: own probe targets (DNS names and HTTP(S) URLs) next to the built-in ones (#194).
  import { api } from './api.js'
  import { toast } from './state.svelte.js'
  import { poll } from './Poll.svelte.js'
  import { t, tr } from './i18n.svelte.js'
  const d = poll(() => api('/internet/targets'), 60000)
  let kind = $state('http')
  let target = $state('')
  let insecure = $state(false)
  const list = $derived(d.data?.targets || [])
  async function save(next) {
    try { d.data = await api('/internet/targets', { method: 'PUT', body: { targets: next } }); toast(t('Saved')); return true } catch (e) { toast(tr(e.message)); return false }
  }
  async function add() {
    const v = target.trim()
    if (!v) return
    if (await save([...list, { kind, target: v, insecure: kind === 'http' && insecure }])) { target = ''; insecure = false }
  }
  const https = $derived(kind === 'http' && target.trim().startsWith('https://'))
</script>
{#if d.data}
  <div class="card" style="margin-top:16px"><div class="card-h"><h2>{t('Own internet targets')}</h2>
    <span class="muted">{t('{n} of {max}', { n: list.length, max: d.data.max })}</span></div>
    <div class="card-b muted" style="font-size:13px">{t('mtmon checks these names and addresses every 30 s on top of the built-in ones. A target that fails 3 checks in a row raises one warning; it is not counted as a WAN outage. Takes effect at once.')}</div>
    <div class="card-b">
      {#if list.length}
        <div class="scroll"><table><tbody>
          {#each list as c, i (c.kind + c.target)}
            <tr><td class="muted">{c.kind === 'dns' ? 'DNS' : 'HTTP(S)'}</td><td class="mono">{c.target}</td>
              <td class="muted">{#if c.insecure}{t('certificate not checked')}{/if}</td>
              <td class="r"><button class="btn sm" onclick={() => save(list.filter((_, j) => j !== i))}>{t('Remove')}</button></td></tr>
          {/each}
        </tbody></table></div>
      {:else}<div class="muted">{t('No own targets yet.')}</div>{/if}
      {#if list.length < d.data.max}
        <div class="row" style="gap:8px;flex-wrap:wrap;margin-top:12px;align-items:center">
          <select bind:value={kind} aria-label={t('Type')}><option value="http">{t('HTTP(S) URL')}</option><option value="dns">{t('DNS name')}</option></select>
          <input bind:value={target} placeholder={kind === 'dns' ? 'example.com' : 'https://example.com/'} aria-label={t('Target')} style="flex:1;min-width:200px" onkeydown={e => e.key === 'Enter' && add()} />
          {#if https}<label class="chk"><input type="checkbox" bind:checked={insecure} /> {t('do not check the certificate')}</label>{/if}
          <button class="btn primary" disabled={!target.trim()} onclick={add}>{t('Add')}</button>
        </div>
      {/if}
    </div>
  </div>
{/if}
