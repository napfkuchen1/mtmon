<script>
  // Who uses it? Lists the clients behind one row of the Top lists or one app.
  import Modal from './Modal.svelte'
  import { api } from './api.js'
  import { app, go } from './state.svelte.js'
  import { poll } from './Poll.svelte.js'
  import { bytes, num, ago } from './format.js'
  import { t } from './i18n.svelte.js'
  let { what, keyOf, title, range, onclose } = $props()
  const q = poll(() => api(`/usage/${what}?key=${encodeURIComponent(keyOf)}&range=${range}`), 15000)
  const open = c => { onclose?.(); go('client/' + encodeURIComponent(c.mac)) }
</script>
<Modal title={t('Who uses {name}', { name: title })} wide {onclose}>
  {#if q.error}<div class="empty">{t('Could not load the client list')}</div>
  {:else if !q.data}<div class="empty">{t('Loading…')}</div>
  {:else if !q.data.clients.length}<div class="empty">{t('No client traffic recorded in this range')}</div>
  {:else}
    <div class="scroll"><table>
      <thead><tr><th>{t('Client')}</th><th>{t('IP')}</th><th class="r">{t('↓ Down')}</th><th class="r">{t('↑ Up')}</th><th class="r">{t('Flows')}</th></tr></thead>
      <tbody>
        {#each q.data.clients as c (c.mac)}
          <tr class="click" onclick={() => open(c)}>
            <td><b>{c.label}</b>{#if c.vendor}<div class="muted" style="font-size:11.5px">{c.vendor}</div>{/if}</td>
            <td class="mono muted">{c.ip || ''}</td>
            <td class="r num">{bytes(c.down)}</td><td class="r num">{bytes(c.up)}</td>
            <td class="r num muted">{num(c.flows)}</td>
          </tr>
        {/each}
      </tbody>
    </table></div>
    <div class="muted" style="font-size:12.5px;margin-top:8px">
      {t('{n} clients · total ↓ {down} ↑ {up}', { n: q.data.clients.length, down: bytes(q.data.down), up: bytes(q.data.up) })}
      {#if q.data.truncated} · {t('Only the busiest destinations are analysed.')}{/if}
    </div>
  {/if}
</Modal>
