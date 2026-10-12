<script lang="ts">
  import { GetPieceMap } from '../wailsjs/go/main/App.js'
  import type { engine } from '../wailsjs/go/models'

  let { hash }: { hash: string } = $props()

  // One code per piece: 0 missing, 1 done, 2 downloading, 3 checking, 4 skipped.
  const CODE: Record<string, number> = { missing: 0, done: 1, partial: 2, checking: 3, skipped: 4 }
  let pieces = $state<Uint8Array>(new Uint8Array(0))
  let counts = $state([0, 0, 0, 0, 0])
  let canvas = $state<HTMLCanvasElement | null>(null)
  let width = $state(0)

  async function load() {
    try {
      const runs: engine.PieceRun[] = (await GetPieceMap(hash)) ?? []
      const total = runs.reduce((s, r) => s + r.n, 0)
      const out = new Uint8Array(total)
      const c = [0, 0, 0, 0, 0]
      let i = 0
      for (const r of runs) {
        const code = CODE[r.s] ?? 0
        out.fill(code, i, i + r.n)
        c[code] += r.n
        i += r.n
      }
      pieces = out
      counts = c
    } catch {
      // Torrent removed or VPN reconnecting; keep the last map.
    }
  }

  $effect(() => {
    hash
    pieces = new Uint8Array(0)
    load()
    const id = setInterval(load, 1000)
    return () => clearInterval(id)
  })

  function color(name: string) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  }

  // Each pixel column covers a slice of pieces. A column is drawn in the
  // "most interesting" state present (checking > downloading), otherwise
  // shaded by the share of its pieces that are done.
  $effect(() => {
    const cv = canvas
    const p = pieces
    const w = width
    if (!cv || w === 0) return
    const dpr = window.devicePixelRatio || 1
    const h = 18
    cv.width = Math.round(w * dpr)
    cv.height = Math.round(h * dpr)
    const ctx = cv.getContext('2d')!
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    ctx.clearRect(0, 0, w, h)
    const missing = color('--border')
    const done = color('--accent')
    const partial = color('--piece-partial')
    const checking = color('--warn')
    const skipped = color('--bg')
    ctx.fillStyle = missing
    ctx.fillRect(0, 0, w, h)
    const n = p.length
    if (n === 0) return

    const cols = Math.min(Math.floor(w), n)
    const colW = w / cols
    for (let x = 0; x < cols; x++) {
      const a = Math.floor((x * n) / cols)
      const b = Math.max(a + 1, Math.floor(((x + 1) * n) / cols))
      let d = 0, dl = 0, ck = 0, sk = 0
      for (let i = a; i < b; i++) {
        const s = p[i]
        if (s === 1) d++
        else if (s === 2) dl++
        else if (s === 3) ck++
        else if (s === 4) sk++
      }
      const span = b - a
      let fill = ''
      let alpha = 1
      if (ck > 0) fill = checking
      else if (dl > 0) fill = partial
      else if (d > 0) {
        fill = done
        alpha = 0.35 + 0.65 * (d / (span - sk || 1))
      } else if (sk === span) fill = skipped
      if (!fill) continue
      ctx.globalAlpha = Math.min(1, alpha)
      ctx.fillStyle = fill
      ctx.fillRect(x * colW, 0, Math.ceil(colW), h)
    }
    ctx.globalAlpha = 1
  })

  const legend = $derived([
    { label: 'Done', n: counts[1], cls: 'done' },
    { label: 'Downloading', n: counts[2], cls: 'partial' },
    { label: 'Checking', n: counts[3], cls: 'checking' },
    { label: 'Missing', n: counts[0], cls: 'missing' },
    { label: 'Skipped', n: counts[4], cls: 'skipped' },
  ].filter(l => l.n > 0 || l.cls === 'done' || l.cls === 'missing'))
</script>

{#if pieces.length > 0}
  <div class="piecemap">
    <div class="bar" bind:clientWidth={width}>
      <canvas bind:this={canvas} style="width:100%;height:18px" aria-label="Piece map"></canvas>
    </div>
    <div class="legend">
      {#each legend as l (l.cls)}
        <span><i class={l.cls}></i>{l.label} <b>{l.n.toLocaleString()}</b></span>
      {/each}
      <span class="total">{pieces.length.toLocaleString()} pieces</span>
    </div>
  </div>
{/if}

<style>
  .piecemap { padding: 8px 14px 10px; border-bottom: 1px solid var(--border); display: grid; gap: 6px; }
  .bar { border: 1px solid var(--border); border-radius: 3px; overflow: hidden; line-height: 0; }
  canvas { display: block; }
  .legend { display: flex; flex-wrap: wrap; gap: 4px 14px; font-size: 12px; color: var(--muted); font-variant-numeric: tabular-nums; }
  .legend b { color: var(--text); font-weight: 500; }
  .legend i { display: inline-block; width: 9px; height: 9px; border-radius: 2px; margin-right: 5px; vertical-align: -1px; }
  .legend .total { margin-left: auto; }
  i.done { background: var(--accent); }
  i.partial { background: var(--piece-partial); }
  i.checking { background: var(--warn); }
  i.missing { background: var(--border); }
  i.skipped { background: var(--bg); border: 1px solid var(--border); }
</style>
