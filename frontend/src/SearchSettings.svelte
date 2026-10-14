<script lang="ts">
  import { GetSearchStatus, AutoDetectSearch, ConnectSearch, DisconnectSearch } from '../wailsjs/go/main/App.js'
  import type { main } from '../wailsjs/go/models'

  let { onError }: { onError: (e: string) => void } = $props()

  let status = $state<main.SearchStatus | null>(null)
  let url = $state('http://localhost:9696')
  let apiKey = $state('')
  let busy = $state(false)

  GetSearchStatus().then(s => (status = s))

  async function attempt(fn: () => Promise<main.SearchStatus | void>) {
    busy = true
    onError('')
    try {
      const s = await fn()
      status = s ?? (await GetSearchStatus())
      apiKey = ''
    } catch (e) {
      onError(String(e))
    }
    busy = false
  }

  const name = (k: string) => (k === 'jackett' ? 'Jackett' : 'Prowlarr')
</script>

{#if status?.configured}
  <div class="row">
    <span class="ok">Connected to {name(status.kind)}</span>
    <span class="muted">· {status.url}</span>
    <button onclick={() => attempt(() => DisconnectSearch())} disabled={busy}>Disconnect</button>
  </div>
{:else}
  <div class="row">
    <button onclick={() => attempt(AutoDetectSearch)} disabled={busy}>Find Prowlarr or Jackett on this computer</button>
  </div>
  <p class="muted small">Or enter it yourself (Prowlarr: Settings → General → API Key; Jackett: top of its page).</p>
  <label class="row">
    <span class="lbl">Address</span>
    <input class="grow" bind:value={url} placeholder="http://localhost:9696" />
  </label>
  <label class="row">
    <span class="lbl">API key</span>
    <input class="grow" type="password" autocomplete="off" bind:value={apiKey} />
    <button onclick={() => attempt(() => ConnectSearch(url, apiKey))} disabled={busy || !apiKey.trim()}>Connect</button>
  </label>
{/if}
<p class="muted small">
  Searches go to your own Prowlarr or Jackett, which searches the sites you added there; no sites are built in.
  The API key is stored encrypted.
</p>

<style>
  .row { display: flex; align-items: center; gap: 8px; margin-top: 8px; flex-wrap: wrap; }
  .lbl { width: 72px; }
  .grow { flex: 1; min-width: 0; }
  .muted { color: var(--muted); }
  .small { font-size: 12px; margin: 6px 0 0; }
  .ok { color: var(--ok); }
</style>
