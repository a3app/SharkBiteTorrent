<script lang="ts">
  import {
    GetSettings, SaveSettings, ChooseDirectory,
    RegisterAssociations, OpenDefaultAppsSettings, GetAbout, OpenURL,
  } from '../wailsjs/go/main/App.js'
  import { main, type platform } from '../wailsjs/go/models'
  import VpnSettings from './VpnSettings.svelte'

  let { onClose }: { onClose: () => void } = $props()

  let s = $state<main.Settings | null>(null)
  let original = $state<main.Settings | null>(null)
  let endpoint = $state('')
  let accounts = $state<Record<string, string>>({})
  let assoc = $state<platform.AssocStatus>({ supported: false, magnet: false, torrent: false, canOpenSettings: false })
  let error = $state('')
  let saving = $state(false)
  let about = $state<main.About | null>(null)
  const hasReferrals = $derived(!!about && Object.values(about.links.providers ?? {}).some(p => p.referral))

  GetAbout().then(a => (about = a))

  GetSettings().then(v => {
    s = { ...v.settings } as main.Settings
    original = { ...v.settings } as main.Settings
    endpoint = v.customEndpoint
    accounts = v.accounts ?? {}
    assoc = v.assoc
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
            {#if about.links.sponsor}
              <div class="row">
                <button class="sponsor" onclick={() => OpenURL(about!.links.sponsor)}>♥ Support development</button>
                <span class="muted small">No ads, no tracking. Donations keep it that way.</span>
              </div>
            {/if}
            {#if hasReferrals}
              <p class="muted small">
                Some “Get a VPN” links are referral links: the provider pays a commission that supports
                development, at no extra cost to you. Free WARP and config import are always available.
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
  .link { background: none; border: none; padding: 0; color: var(--accent); text-decoration: underline; }
  .sponsor { border-color: #db61a2; color: #f0a6cf; }
  .sponsor:hover { background: rgba(219, 97, 162, 0.15) !important; }
  .error {
    margin: 8px 20px 0; padding: 8px 12px; border-radius: 6px; user-select: text;
    background: rgba(248, 81, 73, 0.12); border: 1px solid rgba(248, 81, 73, 0.4);
  }
  .actions { display: flex; justify-content: flex-end; gap: 8px; padding: 14px 20px; border-top: 1px solid var(--border); }
</style>
