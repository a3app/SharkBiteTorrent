<script lang="ts">
  import {
    ListVpnProviders, GetVpnRegions, LoginVpnAccount, LogoutVpnAccount, ImportWireGuardConfig,
    GetAbout, OpenURL,
  } from '../wailsjs/go/main/App.js'
  import type { main, vpnprov } from '../wailsjs/go/models'

  // s is the settings object being edited; changes are saved by the parent.
  let {
    s,
    endpoint = $bindable(),
    accounts = $bindable(),
    onError,
  }: {
    s: main.Settings
    endpoint: string
    accounts: Record<string, string>
    onError: (e: string) => void
  } = $props()

  let providers = $state<vpnprov.Info[]>([])
  let regions = $state<vpnprov.Region[]>([])
  let regionsFor = ''
  let creds = $state<Record<string, string>>({})
  let extra = $state<vpnprov.Field | null>(null) // e.g. 2FA code requested by the provider
  let extraMsg = $state('')
  let busy = $state(false)

  ListVpnProviders().then(p => {
    providers = p
    if (!s.accountProvider && p.length) s.accountProvider = p[0].id
  })

  const provider = $derived(providers.find(p => p.id === s.accountProvider))
  let providerLinks = $state<Record<string, main.ProviderLink>>({})
  GetAbout().then(a => (providerLinks = a.links.providers ?? {}))
  const signup = $derived(providerLinks[s.accountProvider])
  const signedIn = $derived(!!accounts[s.accountProvider])

  // Load server locations for the selected provider.
  $effect(() => {
    const id = s.accountProvider
    if (!id || id === regionsFor) return
    regionsFor = id
    regions = []
    GetVpnRegions(id)
      .then(r => { if (regionsFor === id) regions = (r ?? []).sort((a, b) => a.name.localeCompare(b.name)) })
      .catch(e => onError(String(e)))
  })

  function switchProvider() {
    creds = {}
    extra = null
    extraMsg = ''
    s.accountRegion = ''
  }

  async function signIn(ev: SubmitEvent) {
    ev.preventDefault()
    busy = true
    onError('')
    try {
      const r = await LoginVpnAccount(s.accountProvider, creds)
      if (r.needField) {
        extra = r.needField
        extraMsg = r.message
      } else {
        accounts = { ...accounts, [s.accountProvider]: r.display }
        creds = {}
        extra = null
        extraMsg = ''
        s.vpnProvider = 'account'
      }
    } catch (e) {
      onError(String(e))
    }
    busy = false
  }

  async function signOut() {
    busy = true
    try {
      await LogoutVpnAccount(s.accountProvider)
      const { [s.accountProvider]: _, ...rest } = accounts
      accounts = rest
      if (s.vpnProvider === 'account') s.vpnProvider = 'warp'
    } catch (e) {
      onError(String(e))
    }
    busy = false
  }

  async function importConf() {
    try {
      const ep = await ImportWireGuardConfig()
      if (ep) {
        endpoint = ep
        s.vpnProvider = 'custom'
      }
    } catch (e) {
      onError(String(e))
    }
  }
</script>

<label class="radio">
  <input type="radio" bind:group={s.vpnProvider} value="warp" />
  <span><b>Cloudflare WARP</b> <span class="muted">free, no sign-up, the default. Hides your IP from peers, but peers can't connect to you.</span></span>
</label>

<label class="radio">
  <input type="radio" bind:group={s.vpnProvider} value="account" />
  <span><b>VPN account</b> <span class="muted">sign in to your VPN provider and pick a location.</span></span>
</label>
{#if s.vpnProvider === 'account'}
  <div class="sub">
    <label class="row">
      <span class="lbl">Provider</span>
      <select bind:value={s.accountProvider} onchange={switchProvider}>
        {#each providers as p (p.id)}
          <option value={p.id}>{p.name}{accounts[p.id] ? ' ✓' : ''}</option>
        {/each}
      </select>
    </label>

    {#if provider}
      {#if signedIn}
        <div class="row">
          <span class="lbl"></span>
          <span class="ok">Signed in · {accounts[s.accountProvider]}</span>
          <button onclick={signOut} disabled={busy}>Sign out</button>
        </div>
        <label class="row">
          <span class="lbl">Location</span>
          <select bind:value={s.accountRegion}>
            <option value="">Automatic (your country)</option>
            {#each regions as r (r.id)}
              <option value={r.id}>{r.name}{r.portForward ? ' · port forwarding' : ''}</option>
            {/each}
          </select>
        </label>
        {#if provider.portForwarding}
          <p class="muted small indent">Locations marked “port forwarding” let other peers connect to you, which helps seeding.</p>
        {/if}
      {:else}
        <form onsubmit={signIn}>
          {#each provider.fields as f (f.key)}
            <label class="row">
              <span class="lbl">{f.label}</span>
              <input
                class="grow"
                type={f.kind === 'password' ? 'password' : 'text'}
                autocomplete="off"
                bind:value={creds[f.key]}
              />
            </label>
            {#if f.help}<p class="muted small indent">{f.help}</p>{/if}
          {/each}
          {#if extra}
            <p class="note">{extraMsg}</p>
            {#if extra.kind === 'checkbox'}
              <label class="check indent">
                <input type="checkbox" checked={creds[extra.key] === 'true'}
                  onchange={e => (creds[extra!.key] = e.currentTarget.checked ? 'true' : '')} />
                <span>{extra.label}</span>
              </label>
            {:else}
              <label class="row">
                <span class="lbl">{extra.label}</span>
                <input class="short" autocomplete="one-time-code" bind:value={creds[extra.key]} />
              </label>
            {/if}
          {/if}
          <div class="row">
            <span class="lbl"></span>
            <button class="primary" type="submit" disabled={busy}>{busy ? 'Signing in…' : 'Sign in'}</button>
          </div>
          {#if provider.note}<p class="muted small indent">{provider.note}</p>{/if}
          {#if signup}
            <p class="small indent">
              <span class="muted">Don't have an account?</span>
              <button type="button" class="link" onclick={() => OpenURL(signup.url)}>Get {provider.name} ↗</button>
              {#if signup.referral}<span class="muted">· referral link, supports development</span>{/if}
            </p>
          {/if}
        </form>
      {/if}
    {/if}
  </div>
{/if}

<label class="radio">
  <input type="radio" bind:group={s.vpnProvider} value="custom" disabled={!endpoint} />
  <span>
    <b>WireGuard config file</b>
    {#if endpoint}
      <span class="muted">· {endpoint}</span>
    {:else}
      <span class="muted">works with any provider; import the .conf they give you.</span>
    {/if}
  </span>
</label>
<div class="sub">
  <div class="row">
    <button onclick={importConf}>{endpoint ? 'Replace' : 'Import'} WireGuard config…</button>
  </div>
  {#if s.vpnProvider === 'custom'}
    <label class="row">
      <span class="lbl">Forwarded port</span>
      <input type="number" min="0" max="65535" bind:value={s.forwardedPort} class="short" />
      <span class="muted">If your provider forwards a port to you. 0 = off.</span>
    </label>
  {/if}
</div>

<p class="muted small">🔒 Sign-ins, keys and imported configs are encrypted on this computer and readable only by your user account.</p>

<style>
  .radio { display: flex; align-items: flex-start; gap: 10px; margin-top: 10px; cursor: pointer; }
  .radio input { margin-top: 3px; }
  .sub { margin-left: 26px; padding: 2px 0 6px; }
  .row { display: flex; align-items: center; gap: 8px; margin-top: 8px; flex-wrap: wrap; }
  .lbl { width: 96px; flex: none; }
  .indent { margin-left: 104px; }
  .grow { flex: 1; min-width: 0; }
  .short { width: 110px; }
  .muted { color: var(--muted); }
  .small { font-size: 12px; margin: 4px 0 0; }
  .note { color: var(--warn); margin: 10px 0 0; font-size: 13px; }
  .ok { color: var(--ok); }
  .link { background: none; border: none; padding: 0; color: var(--accent); text-decoration: underline; }
  .check { display: flex; align-items: center; gap: 8px; margin-top: 8px; cursor: pointer; }
  select {
    font: inherit; color: var(--text); background: var(--bg);
    border: 1px solid var(--border); border-radius: 6px; padding: 5px 8px; min-width: 220px;
  }
  form { margin: 0; }
</style>
