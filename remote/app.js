// Shark BiteTorrent remote control. All text from torrents and search
// results is set with textContent, never as HTML: torrent names come from
// strangers.
'use strict'

const TOKEN_KEY = 'sharkbite-remote-token'
const $ = id => document.getElementById(id)
let token = localStorage.getItem(TOKEN_KEY) || ''

async function api(method, path, body) {
  const opts = { method, headers: { Authorization: 'Bearer ' + token } }
  if (body instanceof FormData) opts.body = body
  else if (body !== undefined) {
    opts.body = JSON.stringify(body)
    opts.headers['Content-Type'] = 'application/json'
  }
  const res = await fetch(path, opts)
  if (res.status === 401) {
    showUnpaired()
    throw new Error('not paired')
  }
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
  const type = res.headers.get('Content-Type') || ''
  return type.includes('json') ? res.json() : null
}

function el(tag, cls, text) {
  const e = document.createElement(tag)
  if (cls) e.className = cls
  if (text !== undefined) e.textContent = text
  return e
}

function message(text, ok) {
  const m = $('message')
  m.textContent = text
  m.className = 'message' + (ok ? ' ok' : '')
  m.hidden = !text
  if (text) setTimeout(() => { if (m.textContent === text) m.hidden = true }, 6000)
}

async function act(fn, okText) {
  try {
    await fn()
    if (okText) message(okText, true)
    refresh()
  } catch (e) {
    if (e.message !== 'not paired') message(e.message)
  }
}

function bytes(n) {
  if (!n) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(u.length - 1, Math.floor(Math.log(n) / Math.log(1024)))
  return (n / 1024 ** i).toFixed(i > 1 ? 1 : 0) + ' ' + u[i]
}
const rate = n => (n > 0 ? bytes(n) + '/s' : '')

const stateLabel = {
  metadata: 'Fetching metadata', 'needs-torrent-file': 'Needs .torrent file', checking: 'Checking',
  downloading: 'Downloading', seeding: 'Seeding', paused: 'Paused',
}

function showUnpaired() {
  $('app').hidden = true
  $('unpaired').hidden = false
}

function renderVPN(v) {
  const box = $('vpn')
  box.replaceChildren()
  let cls = 'bad', label = 'VPN ' + (v.state || '…'), detail = v.error || ''
  if (v.state === 'connected') { cls = 'on'; label = 'VPN on'; detail = [v.name, v.location].filter(Boolean).join(' · ') }
  else if (v.state === 'off') { cls = 'off'; label = 'VPN off'; detail = 'normal connection' }
  else if (v.state === 'connecting') { label = 'Connecting VPN…'; detail = '' }
  box.className = 'vpn ' + cls
  box.append(el('b', '', label))
  if (detail) box.append(document.createTextNode(' · ' + detail))
}

function renderTorrents(list) {
  const ul = $('torrents')
  ul.replaceChildren()
  $('empty').hidden = list.length > 0 || !$('searchBox').hidden // not on the search screen
  for (const t of list) {
    const li = el('li', 'item')
    li.append(el('div', 'name', t.name || t.hash))
    const parts = [stateLabel[t.state] || t.state, Math.floor(t.progress * 100) + '%', bytes(t.size)]
    if (t.downRate > 0) parts.push('↓ ' + rate(t.downRate))
    if (t.upRate > 0) parts.push('↑ ' + rate(t.upRate))
    li.append(el('div', 'meta', parts.join(' · ')))
    const bar = el('div', 'bar' + (t.progress >= 1 ? ' done' : ''))
    const fill = el('div')
    fill.style.width = Math.max(0, Math.min(100, t.progress * 100)) + '%'
    bar.append(fill)
    li.append(bar)
    const actions = el('div', 'actions')
    const paused = t.state === 'paused'
    const pr = el('button', '', paused ? 'Resume' : 'Pause')
    pr.onclick = () => act(() => api('POST', `/api/torrents/${t.hash}/${paused ? 'resume' : 'pause'}`))
    const rm = el('button', 'danger', 'Remove…')
    rm.onclick = () => askRemove(t)
    actions.append(pr, rm)
    li.append(actions)
    ul.append(li)
  }
}

// One dialog, like the desktop app: the torrent's name, a "delete files"
// checkbox (off by default), Cancel and Remove.
function askRemove(t) {
  const d = $('removeDialog')
  $('removeName').textContent = t.name || t.hash
  $('removeFiles').checked = false
  d.returnValue = ''
  d.onclose = () => {
    if (d.returnValue !== 'remove') return
    const files = $('removeFiles').checked
    act(() => api('POST', `/api/torrents/${t.hash}/remove?files=${files ? 1 : 0}`), files ? 'Removed, with its files.' : 'Removed.')
  }
  d.showModal()
}

async function refresh() {
  try {
    const s = await api('GET', '/api/state')
    renderVPN(s.vpn || {})
    renderTorrents(s.torrents || [])
  } catch (e) {
    if (e.message !== 'not paired') $('vpn').textContent = 'Can’t reach the computer: ' + e.message
  }
}

const isIOS = /iPhone|iPad|iPod/.test(navigator.userAgent)
const isAndroid = /Android/.test(navigator.userAgent)
const standalone = () => window.matchMedia('(display-mode: standalone)').matches || navigator.standalone === true

// pair exchanges a one-time code (from the QR code, or typed) for this
// phone's own access key.
async function pair(code) {
  const ua = navigator.userAgent
  let name = /iPhone/.test(ua) ? 'iPhone' : /iPad/.test(ua) ? 'iPad' : isAndroid ? 'Android phone' : 'Browser'
  if (standalone()) name += ' (home screen)'
  const res = await fetch('/api/pair', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code, name }),
  })
  if (!res.ok) {
    message((await res.text()).trim())
    return false
  }
  token = (await res.json()).token
  localStorage.setItem(TOKEN_KEY, token)
  message('Paired. This phone can now control Shark BiteTorrent.', true)
  return true
}

async function pairIfNeeded() {
  const m = location.hash.match(/^#pair=([A-Za-z0-9_-]+)$/)
  if (!m) return
  history.replaceState(null, '', location.pathname) // drop the code from the address bar
  await pair(m[1])
}

function showApp() {
  $('unpaired').hidden = true
  $('app').hidden = false
  $('installBtn').hidden = standalone() // already opened from the home screen
}

let refreshing = false
function startRefreshing() {
  if (refreshing) return
  refreshing = true
  refresh()
  setInterval(refresh, 2000)
}

function wire() {
  // Typed pairing code: for when scanning isn't handy, and for an iPhone
  // home-screen app, which may not share Safari's storage.
  $('codeForm').onsubmit = async e => {
    e.preventDefault()
    const code = $('code').value.toUpperCase().replace(/[^A-Z0-9]/g, '')
    if (!code) return
    if (await pair(code)) {
      $('code').value = ''
      showApp()
      startRefreshing()
    }
  }
  // Browsers only let a page add itself to the home screen on HTTPS
  // (Android) or not at all (iPhone), so this explains the two taps.
  $('installBtn').onclick = () => {
    const steps = isIOS
      ? 'In Safari, tap the Share button (the square with an arrow), then “Add to Home Screen”.'
      : isAndroid
        ? 'In Chrome, tap the ⋮ menu at the top right, then “Add to Home screen”.'
        : 'Use your browser’s menu to add this page to your home screen, or bookmark it.'
    $('installSteps').textContent = steps
    $('installDialog').showModal()
  }
  $('addForm').onsubmit = e => {
    e.preventDefault()
    const link = $('magnet').value.trim()
    if (!link) return
    act(async () => { await api('POST', '/api/magnet', { link }); $('magnet').value = '' }, 'Added.')
  }
  $('file').onchange = () => {
    const f = $('file').files[0]
    if (!f) return
    const fd = new FormData()
    fd.append('file', f)
    act(() => api('POST', '/api/torrent', fd), 'Added.')
    $('file').value = ''
  }
  // Search is its own screen, like the desktop app: it replaces the torrent
  // list, and the button becomes the way back.
  $('searchToggle').onclick = () => {
    const searching = $('searchBox').hidden
    $('searchBox').hidden = !searching
    for (const id of ['addForm', 'uploadBtn', 'torrents']) $(id).hidden = searching
    $('empty').hidden = searching || $('torrents').children.length > 0
    $('searchToggle').textContent = searching ? '← Torrents' : 'Search'
    if (searching) $('query').focus()
  }
  $('searchForm').onsubmit = async e => {
    e.preventDefault()
    const q = $('query').value.trim()
    if (!q) return
    const box = $('results')
    box.replaceChildren(el('p', 'muted', 'Searching…'))
    try {
      const rs = await api('GET', `/api/search?q=${encodeURIComponent(q)}&cat=${encodeURIComponent($('cat').value)}`)
      box.replaceChildren()
      if (!rs || rs.length === 0) box.append(el('p', 'muted', 'No results.'))
      for (const r of (rs || []).slice(0, 50)) {
        const item = el('div', 'item')
        const info = el('div')
        info.append(el('div', 'name', r.title))
        info.append(el('div', 'meta', [bytes(r.size), `${r.seeders} seeds`, r.indexer].join(' · ')))
        const btn = el('button', 'primary', 'Download')
        btn.onclick = () => act(async () => { btn.disabled = true; await api('POST', '/api/search/add', r) }, 'Added.')
        item.append(info, btn)
        box.append(item)
      }
    } catch (err) {
      box.replaceChildren(el('p', 'muted', err.message))
    }
  }
}

async function start() {
  wire()
  await pairIfNeeded()
  if (!token) {
    showUnpaired()
    return
  }
  showApp()
  startRefreshing()
}

start()
