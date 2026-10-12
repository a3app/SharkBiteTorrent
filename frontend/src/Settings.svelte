<script lang="ts">
  import {
    GetSettings, SaveSettings, ChooseDirectory,
    RegisterAssociations, OpenDefaultAppsSettings, GetAbout, OpenURL,
    CheckForUpdates, InstallUpdate,
  } from '../wailsjs/go/main/App.js'
  import { main, type platform } from '../wailsjs/go/models'
  import VpnSettings from './VpnSettings.svelte'

  let { onClose }: { onClose: () => void } = $props()

  let s = $state<main.Settings | null>(null)
  let original = $state<main.Settings | null>(null)
  let endpoint = $state('')
  let accounts = $state<Record<string, string>>({})
  let assoc = $state<platform.AssocStatus>({ supported: false, magnet: false, torrent: false, canOpenSettings: false })
  let scanner = $state("")
  let scannerAvailable = $state(false)
  let error = $state('')
  let saving = $state(false)
  let about = $state<main.About | null>(null)
  const providerNames: Record<string, string> = {
    ivpn: 'IVPN', mullvad: 'Mullvad', nordvpn: 'NordVPN', pia: 'Private Internet Access',
  }
  const referralIds = $derived(
    Object.entries(about?.links.providers ?? {}).filter(([, p]) => p.referral).map(([id]) => id).sort(),
  )
  const hasReferrals = $derived(referralIds.length > 0)
  const referralNames = $derived(referralIds.map(id => providerNames[id] ?? id).join(' and '))

  GetAbout().then(a => (about = a))

  let checking = $state(false)
  let installing = $state(false)
  let updateResult = $state<main.UpdateInfo | null>(null)

  async function checkUpdates() {
    checking = true
    updateResult = null
    await attempt(async () => (updateResult = await CheckForUpdates()))
    checking = false
  }

  async function installUpdate() {
    installing = true
    await attempt(() => InstallUpdate(updateResult!.latest)) // the app quits and relaunches
    installing = false
  }

  GetSettings().then(v => {
    s = { ...v.settings } as main.Settings
    original = { ...v.settings } as main.Settings
    endpoint = v.customEndpoint
    accounts = v.accounts ?? {}
    assoc = v.assoc
    scanner = v.scanner
    scannerAvailable = v.scannerAvailable
  })

  const vpnChanged = $derived(
    !!s && !!original &&
      (s.vpnProvider !== original.vpnProvider ||
        (s.vpnProvider === 'custom' && s.forwardedPort !== original.forwardedPort) ||
        (s.vpnProvider === 'account' &&
          (s.accountProvider !== original.accountProvider || s.accountRegion !== original.accountRegion))),
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
          <VpnSettings {s} bind:endpoint bind:accounts onError={e => (error = e)} />
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
          <legend>Desktop</legend>
          <label class="check">
            <input type="checkbox" bind:checked={s.closeToTray} />
            <span>Closing the window keeps Shark BiteTorrent running in the tray</span>
          </label>
          <label class="check">
            <input type="checkbox" bind:checked={s.scanDownloads} disabled={!scannerAvailable} />
            <span>
              Scan finished downloads for malware with {scanner}
              {#if !scannerAvailable}<span class="muted">(not installed)</span>{/if}
            </span>
          </label>
          {#if !scannerAvailable && scanner === 'ClamAV'}
            <p class="muted small">
              Install ClamAV to enable scanning: <code>sudo pacman -S clamav</code> on Arch/CachyOS,
              <code>brew install clamav</code> on macOS. Torrents are still checked for disguised programs either way.
            </p>
          {:else}
            <p class="muted small">
              Every torrent is also checked for disguised programs as soon as its file list arrives, and finished
              files are marked as downloaded from the internet so the system checks them when opened.
            </p>
          {/if}
          {#if assoc.supported}
            <div class="row">
              <span class="status">
                Magnet links: <b class:ok={assoc.magnet}>{assoc.magnet ? 'open here' : 'not set'}</b> ·
                .torrent files: <b class:ok={assoc.torrent}>{assoc.torrent ? 'open here' : 'not set'}</b>
              </span>
            </div>
            <div class="row">
              <button onclick={register}>Open magnet links &amp; .torrent files with Shark BiteTorrent</button>
            </div>
            {#if !(assoc.magnet && assoc.torrent) && assoc.canOpenSettings}
              <p class="muted small">
                If you picked another app before, your system keeps using it.
                <button class="link" onclick={() => OpenDefaultAppsSettings()}>Open Default Apps</button>
                and pick Shark BiteTorrent there.
              </p>
            {/if}
          {/if}
        </fieldset>

        {#if about}
          <fieldset>
            <legend>About</legend>
            <div class="row">
              <span>Shark BiteTorrent {about.version}</span>
              <span class="muted">· free software ({about.license})</span>
              <button class="link" onclick={() => OpenURL(about!.links.repo)}>Source code ↗</button>
            </div>
            <div class="row">
              <button onclick={checkUpdates} disabled={checking || installing}>
                {checking ? 'Checking…' : 'Check for updates'}
              </button>
              {#if updateResult}
                {#if updateResult.available}
                  <span>Version <b>{updateResult.latest}</b> is available.</span>
                  {#if updateResult.canInstall}
                    <button class="primary" onclick={installUpdate} disabled={installing}>
                      {installing ? 'Downloading…' : 'Update now'}
                    </button>
                  {:else}
                    <button class="primary" onclick={() => OpenURL(updateResult!.notesURL)}>Download</button>
                  {/if}
                  <button class="link" onclick={() => OpenURL(updateResult!.notesURL)}>What's new ↗</button>
                {:else}
                  <span class="ok">You're up to date.</span>
                {/if}
              {/if}
            </div>
            {#if updateResult?.direct}
              <p class="muted small">The VPN was off, so this check used your normal connection.</p>
            {/if}
            <label class="check">
              <input type="checkbox" bind:checked={s.autoUpdateCheck} />
              <span>Check for updates automatically <span class="muted">(about once a day, through the VPN)</span></span>
            </label>
            {#if about.links.sponsor}
              <div class="row">
                <button class="sponsor" onclick={() => OpenURL(about!.links.sponsor)}>♥ Support development</button>
                <span class="muted small">No ads, no tracking. Donations keep it that way.</span>
              </div>
            {/if}
            {#if !about.funding}
            <p class="muted small">
              <b>This build has funding links turned off</b> (built with <code>-tags nofunding</code>): no sponsor
              button and no referral links. No ads, no telemetry: the app never sends anything to the developers.
              Updates are offered as a download rather than installed, because official releases include the links.
            </p>
            {:else}
            <p class="muted small">
              <b>How this app is funded:</b> optional donations{#if hasReferrals}, and referral links for
                {referralNames}: if you subscribe through one, the provider pays this project a commission
                (same price for you; the provider's site will know you came from this app){/if}.
              Providers are listed alphabetically, never by commission. Free WARP and WireGuard import are always available.
              No ads, no telemetry: the app never sends anything to us.
              <button class="link" onclick={() => OpenURL(about!.links.repo + '#how-this-project-is-funded')}>Details ↗</button>
            </p>
            {/if}
          </fieldset>
        {/if}
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
  .check { display: flex; align-items: flex-start; gap: 10px; margin-top: 8px; cursor: pointer; }
  .check input { margin-top: 3px; }
  .lbl { width: 72px; }
  .short { width: 100px; }
  .grow { flex: 1; min-width: 0; }
  .muted { color: var(--muted); }
  .small { font-size: 12px; margin: 6px 0 0; }
  .note { color: var(--warn); margin: 10px 0 0; font-size: 13px; }
  .status b { font-weight: 600; color: var(--warn); }
  .status b.ok { color: var(--ok); }
  .ok { color: var(--ok); }
  .link { background: none; border: none; padding: 0; color: var(--accent); text-decoration: underline; }
  .sponsor { border-color: #db61a2; color: #f0a6cf; }
  .sponsor:hover { background: rgba(219, 97, 162, 0.15) !important; }
  .error {
    margin: 8px 20px 0; padding: 8px 12px; border-radius: 6px; user-select: text;
    background: rgba(248, 81, 73, 0.12); border: 1px solid rgba(248, 81, 73, 0.4);
  }
  .actions { display: flex; justify-content: flex-end; gap: 8px; padding: 14px 20px; border-top: 1px solid var(--border); }
</style>
