<script>
  import DeviceIcon from './DeviceIcon.svelte'
  // Round device icon with a small presence dot. client: {label, hostname, vendor, wifi}, online: bool
  let { client, online = false, size = 32 } = $props()
</script>
<span class="av" style="width:{size}px;height:{size}px"><DeviceIcon {client} size={Math.round(size * 0.56)} color="var(--muted)" />
  <i class="st" class:on={online}></i></span>
<style>
  .av { position: relative; flex: none; display: inline-grid; place-items: center; border-radius: 50%; background: var(--card-2); border: 1px solid var(--border); }
  .st { position: absolute; right: -1px; bottom: -1px; width: 9px; height: 9px; border-radius: 50%; background: var(--faint); border: 2px solid var(--card); box-sizing: content-box; }
  .st.on { background: var(--ok); }
</style>
