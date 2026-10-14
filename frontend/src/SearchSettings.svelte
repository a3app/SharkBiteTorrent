<script lang="ts">
  import { GetSearchStatus, AutoDetectSearch, ConnectSearch, DisconnectSearch, GetProxyStatus, SetSearchProxy } from '../wailsjs/go/main/App.js'
  import { ClipboardSetText } from '../wailsjs/runtime/runtime.js'
  import type { main } from '../wailsjs/go/models'

  let { onError }: { onError: (e: string) => void } = $props()

  let status = $state<main.SearchStatus | null>(null)
  let url = $state('http://localhost:9696')
  let apiKey = $state('')
  let busy = $state(false)

  let proxy = $state<main.ProxyStatus | null>(null)
  let proxyBusy = $state(false)
  let showPass = $state(false)
  let detailsOpen = $state(false)

  GetSearchStatus().then(s => (status = s))
  $effect(() => {
    if (status?.configured)
      GetProxyStatus().then(p => {
        proxy = p
        detailsOpen = p.manual // Jackett: the values are needed right away
      })
    else proxy = null
  })

  async function setProxy(on: boolean) {
    proxyBusy = true
    onError('')
    try {
      proxy = await SetSearchProxy(on)
      detailsOpen = proxy.manual
    } catch (e) {
      onError(String(e))
      proxy = await GetProxyStatus()
    }
    proxyBusy = false
  }

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
  {#if proxy}
    <label class="row">
      <input type="checkbox" checked={proxy.enabled} disabled={proxyBusy} onchange={e => setProxy(e.currentTarget.checked)} />
      Send {name(status.kind)}'s traffic through the VPN too
    </label>
    {#if proxy.enabled}
      {#if proxy.error}
        <p class="warn small">{proxy.error}</p>
      {:else if proxy.applied}
        <p class="muted small">
          <span class="ok">{name(status.kind)} is using it.</span> Its searches only work while Shark BiteTorrent is running;
          they fail rather than go out on your real IP. Sonarr, Radarr and FlareSolverr on your own network are reached directly.
        </p>
      {:else if proxy.manual}
        <p class="muted small">
          Jackett's proxy has to be set on its own page: open Jackett, set these under <b>Proxy</b> at the bottom,
          then click <b>Apply server settings</b>.
        </p>
      {:else}
        <p class="warn small">
          {name(status.kind)} isn't set to use it anymore.
          <button type="button" class="link" onclick={() => setProxy(true)} disabled={proxyBusy}>Set it again</button>
        </p>
      {/if}
      <details class="small" bind:open={detailsOpen}>
        <summary class="muted">Proxy details</summary>
        <div class="grid">
          <span class="muted">Type</span><span>SOCKS5</span>
          <span class="muted">Address</span><span>{proxy.host}</span>
          <span class="muted">Port</span><span>{proxy.port}</span>
          <span class="muted">Username</span><span>{proxy.username}</span>
          <span class="muted">Password</span>
          <span>
            <code>{showPass ? proxy.password : '••••••••••'}</code>
            <button type="button" class="link" onclick={() => (showPass = !showPass)}>{showPass ? 'Hide' : 'Show'}</button>
            <button type="button" class="link" onclick={() => ClipboardSetText(proxy!.password)}>Copy</button>
          </span>
        </div>
      </details>
    {/if}
  {/if}
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
  .warn { color: var(--warn, #e0a030); }
  .link { background: none; border: none; padding: 0 0 0 6px; color: var(--accent); text-decoration: underline; font: inherit; }
  details { margin-top: 6px; }
  summary { cursor: pointer; }
  .grid { display: grid; grid-template-columns: auto 1fr; gap: 4px 12px; margin-top: 6px; }
</style>
