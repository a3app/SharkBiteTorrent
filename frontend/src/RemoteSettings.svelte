<script lang="ts">
  import { GetRemoteStatus, SetRemote, StartRemotePairing, RemoveRemoteDevice } from '../wailsjs/go/main/App.js'
  import { EventsOn } from '../wailsjs/runtime/runtime.js'
  import type { main } from '../wailsjs/go/models'

  let { onError }: { onError: (e: string) => void } = $props()

  let status = $state<main.RemoteStatus | null>(null)
  let port = $state(8732)
  let pairing = $state<main.RemotePairing | null>(null)
  let busy = $state(false)

  async function load() {
    status = await GetRemoteStatus()
    port = status.port
  }
  load()

  $effect(() => {
    // A phone just paired: hide the QR code and show it in the list.
    const off = EventsOn('remote-paired', () => {
      pairing = null
      load()
    })
    return off
  })

  async function apply(enabled: boolean) {
    busy = true
    onError('')
    try {
      status = await SetRemote(enabled, Number(port))
      if (!enabled) pairing = null
    } catch (e) {
      onError(String(e))
      await load()
    }
    busy = false
  }

  async function pair() {
    onError('')
    try {
      pairing = await StartRemotePairing()
    } catch (e) {
      onError(String(e))
    }
  }

  async function remove(id: string) {
    await RemoveRemoteDevice(id)
    await load()
  }

  const ago = (unix: number) => {
    const s = Date.now() / 1000 - unix
    if (s < 120) return 'just now'
    if (s < 7200) return `${Math.floor(s / 60)} min ago`
    if (s < 172800) return `${Math.floor(s / 3600)} h ago`
    return `${Math.floor(s / 86400)} days ago`
  }
</script>

{#if status}
  <label class="row">
    <input type="checkbox" checked={status.enabled} disabled={busy} onchange={e => apply(e.currentTarget.checked)} />
    Let phones on my home network control Shark BiteTorrent
  </label>
  {#if status.enabled}
    {#if status.error}
      <p class="warn small">{status.error}</p>
    {/if}
    <label class="row">
      <span class="lbl">Port</span>
      <input type="number" min="1024" max="65535" bind:value={port} class="short" />
      {#if Number(port) !== status.port}
        <button onclick={() => apply(true)} disabled={busy}>Apply</button>
      {/if}
    </label>
    {#if status.running}
      <p class="muted small">
        Open on a phone on the same Wi-Fi: {status.urls.length ? status.urls.join(' or ') : '(no home network address found)'}
      </p>
      <div class="row">
        <button class="primary" onclick={pair}>Pair a phone</button>
      </div>
      {#if pairing}
        <div class="pair">
          <img src={pairing.qr} alt="QR code to pair a phone" width="200" height="200" />
          <div>
            <p class="muted small">
              Scan with the phone's camera, or open the address below on the phone and type this code:
            </p>
            <p class="code">{pairing.code}</p>
            <p class="muted small">
              It works once and expires in 5 minutes. Windows may ask to allow Shark BiteTorrent on
              <b>private</b> networks: allow it. On the phone, “Add to Home Screen” makes it easy to come back
              without pairing again.
            </p>
          </div>
        </div>
      {/if}
    {/if}
    {#if status.devices.length > 0}
      <div class="devices">
        {#each status.devices as d (d.id)}
          <div class="row">
            <span class="grow">{d.name} <span class="muted">· last used {ago(d.lastSeen)}</span></span>
            <button onclick={() => remove(d.id)}>Remove</button>
          </div>
        {/each}
      </div>
    {/if}
    <p class="muted small">
      Paired phones can do what this window can: add, pause and remove torrents (including deleting their files), and
      search. It only answers devices on your home network, over plain HTTP. Remove a phone here if you lose it.
      Tip: reserve this computer's address in your router ("DHCP reservation") so phone shortcuts keep working.
    </p>
  {/if}
{/if}

<style>
  .row { display: flex; align-items: center; gap: 8px; margin-top: 8px; flex-wrap: wrap; }
  .lbl { width: 72px; }
  .short { width: 110px; }
  .grow { flex: 1; min-width: 0; }
  .muted { color: var(--muted); }
  .small { font-size: 12px; margin: 6px 0 0; }
  .warn { color: var(--warn); }
  .pair { display: flex; gap: 12px; align-items: center; margin-top: 8px; }
  .pair img { background: #fff; padding: 6px; border-radius: 6px; image-rendering: pixelated; }
  .devices { margin-top: 6px; }
  .code { font-size: 22px; font-weight: 600; letter-spacing: 0.12em; margin: 4px 0; font-variant-numeric: tabular-nums; }
</style>
