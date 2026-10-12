<script lang="ts">
  import {
    GetSettings, SaveSettings, ChooseDirectory, ImportWireGuardConfig,
    RegisterAssociations, OpenDefaultAppsSettings,
  } from '../wailsjs/go/main/App.js'
  import { main, type winassoc } from '../wailsjs/go/models'

  let { onClose }: { onClose: () => void } = $props()

  let s = $state<main.Settings | null>(null)
  let original = $state<main.Settings | null>(null)
  let endpoint = $state('')
  let assoc = $state<winassoc.AssocStatus>({ magnet: false, torrent: false })
  let error = $state('')
  let saving = $state(false)

  GetSettings().then(v => {
    s = { ...v.settings } as main.Settings
    original = { ...v.settings } as main.Settings
    endpoint = v.customEndpoint
    assoc = v.assoc
  })

  const vpnChanged = $derived(
    !!s && !!original &&
      (s.vpnProvider !== original.vpnProvider ||
        (s.vpnProvider === 'custom' && s.forwardedPort !== original.forwardedPort)),
  )

  async function attempt(fn: () => Promise<unknown>) {
    error = ''
    try {
      await fn()
    } catch (e) {
      error = String(e)
    }
  }

  const pickDir = () =>
    attempt(async () => {
      const d = await ChooseDirectory(s!.downloadDir)
      if (d) s!.downloadDir = d
    })

  const importConf = () =>
    attempt(async () => {
      const ep = await ImportWireGuardConfig()
      if (ep) {
        endpoint = ep
        s!.vpnProvider = 'custom'
      }
    })

  const register = () => attempt(async () => (assoc = await RegisterAssociations()))

  async function save() {
    saving = true
    await attempt(async () => {
      await SaveSettings(main.Settings.createFrom({
        ...s!,
        forwardedPort: Number(s!.forwardedPort) || 0,
        downLimitKB: Number(s!.downLimitKB) || 0,
        upLimitKB: Number(s!.upLimitKB) || 0,
      }))
      onClose()
    })
    saving = false
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') onClose()
  }
</script>

<svelte:window onkeydown={onKey} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="backdrop" onclick={onClose}>
  <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="settings-title" tabindex="-1" onclick={e => e.stopPropagation()}>
    <h2 id="settings-title">Settings</h2>

    {#if !s}
      <p class="muted">Loading…</p>
    {:else}
      <div class="body">
        <fieldset>
          <legend>VPN</legend>
          <label class="radio">
            <input type="radio" bind:group={s.vpnProvider} value="warp" />
            <span><b>Cloudflare WARP</b> <span class="muted">free, no sign-up. Downloads only; peers can't connect to you.</span></span>
          </label>
          <label class="radio">
            <input type="radio" bind:group={s.vpnProvider} value="custom" disabled={!endpoint} />
            <span>
              <b>Custom WireGuard</b>
              {#if endpoint}
                <span class="muted">· {endpoint}</span>
              {:else}
                <span class="muted">import a .conf from your VPN provider first</span>
              {/if}
            </span>
          </label>
          <div class="row indent">
            <button onclick={importConf}>{endpoint ? 'Replace' : 'Import'} WireGuard config…</button>
          </div>
          <label class="row indent">
            <span>Forwarded port</span>
            <input type="number" min="0" max="65535" bind:value={s.forwardedPort} disabled={s.vpnProvider !== 'custom'} class="short" />
            <span class="muted">If your provider forwards a port to you, enter it to accept incoming peers. 0 = off.</span>
          </label>
          {#if vpnChanged}<p class="note">Saving will reconnect the VPN. Torrents pick up where they left off.</p>{/if}
        </fieldset>

        <fieldset>
          <legend>Speed limits</legend>
          <label class="row">
            <span class="lbl">Download</span>
            <input type="number" min="0" bind:value={s.downLimitKB} class="short" /> <span class="muted">KB/s</span>
          </label>
          <label class="row">
            <span class="lbl">Upload</span>
            <input type="number" min="0" bind:value={s.upLimitKB} class="short" /> <span class="muted">KB/s</span>
          </label>
          <p class="muted small">0 = unlimited. Changes apply immediately.</p>
        </fieldset>

        <fieldset>
          <legend>Downloads</legend>
          <div class="row">
            <input class="grow" readonly value={s.downloadDir} title={s.downloadDir} />
            <button onclick={pickDir}>Change…</button>
          </div>
          <p class="muted small">Used for torrents you add from now on. Existing torrents stay where they are.</p>
        </fieldset>

        <fieldset>
          <legend>Windows</legend>
          <label class="check">
            <input type="checkbox" bind:checked={s.closeToTray} />
            <span>Closing the window keeps Shark BiteTorrent running in the tray</span>
          </label>
          <div class="row">
            <span class="status">
              Magnet links: <b class:ok={assoc.magnet}>{assoc.magnet ? 'open here' : 'not set'}</b> ·
              .torrent files: <b class:ok={assoc.torrent}>{assoc.torrent ? 'open here' : 'not set'}</b>
            </span>
          </div>
          <div class="row">
            <button onclick={register}>Open magnet links &amp; .torrent files with Shark BiteTorrent</button>
          </div>
          {#if !(assoc.magnet && assoc.torrent)}
            <p class="muted small">
              If another app was chosen in Windows before, Windows keeps using it.
              <button class="link" onclick={() => OpenDefaultAppsSettings()}>Open Default Apps</button>
              and pick Shark BiteTorrent there.
            </p>
          {/if}
        </fieldset>
      </div>
    {/if}

    {#if error}<div class="error" role="alert">{error}</div>{/if}

    <div class="actions">
      <button onclick={onClose}>Cancel</button>
      <button class="primary" onclick={save} disabled={!s || saving}>Save</button>
    </div>
  </div>
</div>

<style>
  .backdrop {
    position: fixed; inset: 0; background: rgba(0, 0, 0, 0.55);
    display: grid; place-items: center; z-index: 10;
  }
  .dialog {
    width: min(640px, calc(100vw - 32px)); max-height: calc(100vh - 32px);
    display: flex; flex-direction: column;
    background: var(--panel); border: 1px solid var(--border); border-radius: 10px;
    box-shadow: 0 20px 50px rgba(0, 0, 0, 0.5);
  }
  h2 { margin: 0; padding: 16px 20px 8px; font-size: 18px; font-weight: 600; }
  .body { overflow: auto; padding: 0 20px; }
  fieldset { border: none; border-top: 1px solid var(--border); margin: 0; padding: 12px 0 14px; }
  legend { color: var(--muted); font-size: 12px; text-transform: uppercase; letter-spacing: 0.06em; padding-right: 8px; }
  .row { display: flex; align-items: center; gap: 8px; margin-top: 8px; flex-wrap: wrap; }
  .indent { margin-left: 26px; }
  .radio, .check { display: flex; align-items: flex-start; gap: 10px; margin-top: 8px; cursor: pointer; }
  .radio input, .check input { margin-top: 3px; }
  .lbl { width: 72px; }
  .short { width: 100px; }
  .grow { flex: 1; min-width: 0; }
  .muted { color: var(--muted); }
  .small { font-size: 12px; margin: 6px 0 0; }
  .note { color: var(--warn); margin: 10px 0 0; font-size: 13px; }
  .status b { font-weight: 600; color: var(--warn); }
  .status b.ok { color: var(--ok); }
  .link { background: none; border: none; padding: 0; color: var(--accent); text-decoration: underline; }
  .error {
    margin: 8px 20px 0; padding: 8px 12px; border-radius: 6px; user-select: text;
    background: rgba(248, 81, 73, 0.12); border: 1px solid rgba(248, 81, 73, 0.4);
  }
  .actions { display: flex; justify-content: flex-end; gap: 8px; padding: 14px 20px; border-top: 1px solid var(--border); }
</style>
