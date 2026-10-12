<script lang="ts">
  import { onMount } from 'svelte'
  import {
    GetVpnStatus, ListTorrents, AddMagnet, PickTorrentFile,
    Pause, Resume, Remove, Retry, OpenDownloadDir, GetSettings, ChooseDownloadDir,
  } from '../wailsjs/go/main/App.js'
  import type { engine, main } from '../wailsjs/go/models'

  let vpn = $state<main.VpnStatus>({ state: 'connecting', exitIP: '', location: '', colo: '', error: '' } as main.VpnStatus)
  let torrents = $state<engine.Status[]>([])
  let selected = $state<string | null>(null)
  let magnet = $state('')
  let error = $state('')
  let downloadDir = $state('')
  let dirChanged = $state(false)

  const connected = $derived(vpn.state === 'connected')
  const sel = $derived(torrents.find(t => t.hash === selected))
  const totalDown = $derived(torrents.reduce((s, t) => s + t.downRate, 0))
  const totalUp = $derived(torrents.reduce((s, t) => s + t.upRate, 0))

  async function refresh() {
    try {
      vpn = await GetVpnStatus()
      torrents = await ListTorrents()
      if (selected && !torrents.some(t => t.hash === selected)) selected = null
    } catch (e) {
      error = String(e)
    }
  }

  onMount(() => {
    GetSettings().then(s => (downloadDir = s.downloadDir))
    refresh()
    const id = setInterval(refresh, 1000)
    return () => clearInterval(id)
  })

  async function run(fn: () => Promise<unknown>) {
    error = ''
    try {
      await fn()
      await refresh()
    } catch (e) {
      error = String(e)
    }
  }

  function addMagnet(ev: SubmitEvent) {
    ev.preventDefault()
    if (!magnet.trim()) return
    run(async () => {
      await AddMagnet(magnet)
      magnet = ''
    })
  }

  async function chooseDir() {
    const before = downloadDir
    await run(async () => (downloadDir = await ChooseDownloadDir()))
    if (downloadDir !== before) dirChanged = true
  }

  function bytes(n: number): string {
    if (n < 1024) return `${n} B`
    const u = ['KB', 'MB', 'GB', 'TB']
    let i = -1
    do { n /= 1024; i++ } while (n >= 1024 && i < u.length - 1)
    return `${n.toFixed(n < 10 ? 2 : n < 100 ? 1 : 0)} ${u[i]}`
  }
  const rate = (n: number) => (n > 0 ? `${bytes(n)}/s` : '–')

  function eta(t: engine.Status): string {
    if (t.state !== 'downloading' || t.downRate <= 0) return '–'
    const s = Math.round((t.size - t.done) / t.downRate)
    if (s < 60) return `${s}s`
    if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`
    return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`
  }

  const stateLabel: Record<string, string> = {
    metadata: 'Fetching metadata', downloading: 'Downloading', seeding: 'Seeding', paused: 'Paused',
  }
</script>

<div class="shell">
  <header>
    <div class="vpn {vpn.state}">
      <span class="dot"></span>
      {#if vpn.state === 'connected'}
        <span><b>VPN on</b> · WARP exit {vpn.exitIP} · {vpn.location} ({vpn.colo})</span>
        {#if vpn.error}<span class="warn" title={vpn.error}>⚠ check failed</span>{/if}
      {:else if vpn.state === 'connecting'}
        <span><b>Connecting to WARP…</b> Torrents start once the tunnel is verified.</span>
      {:else}
        <span><b>VPN error</b> · {vpn.error}</span>
        <button onclick={() => run(Retry)}>Retry</button>
      {/if}
    </div>
    <div class="totals">↓ {rate(totalDown)} &nbsp; ↑ {rate(totalUp)}</div>
  </header>

  <div class="toolbar">
    <form onsubmit={addMagnet}>
      <input placeholder="Paste a magnet link…" bind:value={magnet} disabled={!connected} />
      <button class="primary" type="submit" disabled={!connected || !magnet.trim()}>Add</button>
    </form>
    <button onclick={() => run(PickTorrentFile)} disabled={!connected}>Open .torrent…</button>
    <span class="sep"></span>
    <button onclick={() => sel && run(() => Resume(sel.hash))} disabled={!sel || sel.state !== 'paused'}>Resume</button>
    <button onclick={() => sel && run(() => Pause(sel.hash))} disabled={!sel || sel.state === 'paused'}>Pause</button>
    <button onclick={() => sel && run(() => Remove(sel.hash))} disabled={!sel} title="Removes from the list; downloaded files are kept">Remove</button>
  </div>

  {#if error}<div class="error" role="alert">{error} <button onclick={() => (error = '')}>✕</button></div>{/if}

  <main>
    {#if torrents.length === 0}
      <div class="empty">
        {#if connected}
          No torrents yet. Paste a magnet link or open a .torrent file.
        {:else}
          Waiting for the VPN. Nothing will download until the tunnel is up.
        {/if}
      </div>
    {:else}
      <table>
        <thead>
          <tr>
            <th class="name">Name</th><th>Size</th><th class="prog">Progress</th><th>Status</th>
            <th>Down</th><th>Up</th><th>ETA</th><th>Peers</th>
          </tr>
        </thead>
        <tbody>
          {#each torrents as t (t.hash)}
            <tr class:selected={t.hash === selected} onclick={() => (selected = t.hash)}>
              <td class="name" title={t.name}>{t.name || t.hash}</td>
              <td>{t.size ? bytes(t.size) : '–'}</td>
              <td class="prog">
                <div class="bar {t.state}"><div style="width:{(t.progress * 100).toFixed(1)}%"></div></div>
                <span>{(t.progress * 100).toFixed(1)}%</span>
              </td>
              <td>{stateLabel[t.state] ?? t.state}</td>
              <td>{rate(t.downRate)}</td>
              <td>{rate(t.upRate)}</td>
              <td>{eta(t)}</td>
              <td>{t.seeds}/{t.peers}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </main>

  <footer>
    <span class="path" title={downloadDir}>Saving to {downloadDir}</span>
    {#if dirChanged}<span class="warn">Restart to apply new folder</span>{/if}
    <button onclick={chooseDir}>Change…</button>
    <button onclick={() => run(OpenDownloadDir)}>Open folder</button>
  </footer>
</div>

<style>
  .shell { display: flex; flex-direction: column; height: 100vh; }

  header {
    display: flex; align-items: center; justify-content: space-between; gap: 12px;
    padding: 10px 14px; border-bottom: 1px solid var(--border); background: var(--panel);
  }
  .vpn { display: flex; align-items: center; gap: 10px; min-width: 0; }
  .vpn span { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .dot { width: 10px; height: 10px; border-radius: 50%; flex: none; background: var(--warn); }
  .vpn.connected .dot { background: var(--ok); box-shadow: 0 0 6px var(--ok); }
  .vpn.error .dot { background: var(--err); }
  .vpn.connecting .dot { animation: pulse 1s infinite alternate; }
  @keyframes pulse { to { opacity: 0.3; } }
  .totals { color: var(--muted); white-space: nowrap; font-variant-numeric: tabular-nums; }
  .warn { color: var(--warn); }

  .toolbar { display: flex; align-items: center; gap: 8px; padding: 10px 14px; }
  .toolbar form { display: flex; gap: 8px; flex: 1; min-width: 0; }
  .toolbar input { flex: 1; min-width: 0; }
  .sep { width: 1px; align-self: stretch; background: var(--border); margin: 0 4px; }

  .error {
    margin: 0 14px 8px; padding: 8px 12px; border-radius: 6px;
    background: rgba(248, 81, 73, 0.12); border: 1px solid rgba(248, 81, 73, 0.4);
    display: flex; justify-content: space-between; align-items: center; gap: 8px; user-select: text;
  }
  .error button { padding: 2px 8px; background: none; border: none; }

  main { flex: 1; overflow: auto; border-top: 1px solid var(--border); }
  .empty { display: grid; place-items: center; height: 100%; color: var(--muted); }

  table { width: 100%; border-collapse: collapse; font-variant-numeric: tabular-nums; }
  th {
    position: sticky; top: 0; background: var(--bg); color: var(--muted); font-weight: 500;
    text-align: left; padding: 8px 10px; border-bottom: 1px solid var(--border); white-space: nowrap;
  }
  td { padding: 7px 10px; border-bottom: 1px solid var(--border); white-space: nowrap; }
  td.name, th.name { width: 40%; max-width: 0; overflow: hidden; text-overflow: ellipsis; }
  tbody tr { cursor: default; }
  tbody tr:hover { background: var(--row-hover); }
  tbody tr.selected { background: rgba(79, 140, 255, 0.18); }

  .prog { width: 160px; }
  td.prog { display: flex; align-items: center; gap: 8px; }
  .bar { flex: 1; height: 6px; border-radius: 3px; background: var(--border); overflow: hidden; }
  .bar div { height: 100%; background: var(--accent); transition: width 0.4s; }
  .bar.seeding div { background: var(--ok); }
  .bar.paused div { background: var(--muted); }
  td.prog span { width: 44px; text-align: right; color: var(--muted); font-size: 12px; }

  footer {
    display: flex; align-items: center; gap: 8px; padding: 8px 14px;
    border-top: 1px solid var(--border); background: var(--panel); color: var(--muted);
  }
  footer .path { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  footer button { padding: 4px 10px; }
</style>
