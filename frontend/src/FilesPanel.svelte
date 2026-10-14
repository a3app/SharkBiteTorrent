<script lang="ts">
  import { GetFiles, SetFilesWanted } from '../wailsjs/go/main/App.js'
  import type { engine } from '../wailsjs/go/models'
  import { bytes } from './format'
  import PieceMap from './PieceMap.svelte'

  let { torrent, onError }: { torrent: engine.Status; onError: (e: string) => void } = $props()

  let files = $state<engine.FileStatus[]>([])
  const allWanted = $derived(files.length > 0 && files.every(f => f.wanted))
  const noneWanted = $derived(files.every(f => !f.wanted))
  const warnings = $derived(torrent.warnings ?? [])
  const flagged = $derived(new Set(warnings.map(w => w.file)))

  // Set when the panel closes or switches torrent, so a request still in
  // flight (e.g. for a torrent just removed) doesn't report an error.
  let stale = false

  async function load() {
    const h = hash
    try {
      const f = (await GetFiles(h)) ?? []
      if (!stale && h === hash) files = f
    } catch (e) {
      if (!stale && h === hash) onError(String(e))
    }
  }

  // Only restart when a different torrent is selected: the torrent object
  // itself is re-created on every 1s list refresh.
  const hash = $derived(torrent.hash)

  $effect(() => {
    void hash
    stale = false
    load()
    const id = setInterval(load, 1500)
    return () => {
      stale = true
      clearInterval(id)
    }
  })

  async function setWanted(indexes: number[], wanted: boolean) {
    try {
      await SetFilesWanted(hash, indexes, wanted)
      await load()
    } catch (e) {
      onError(String(e))
    }
  }
</script>

<section class="panel">
  <div class="head">
    <b title={torrent.name}>{torrent.name || torrent.hash}</b>
    <span class="muted">{files.length} file{files.length === 1 ? '' : 's'}</span>
  </div>
  {#if warnings.length > 0 || torrent.scanState}
    <div class="safety">
      {#each warnings as w (w.file)}
        <div class="caution">⚠ <b>{w.file}</b>: {w.reason}</div>
      {/each}
      {#if torrent.scanState === 'scanning'}
        <div class="muted">Scanning the finished files for malware…</div>
      {:else if torrent.scanState === 'clean'}
        <div class="ok">✓ {torrent.scanEngine} found no threats.</div>
      {:else if torrent.scanState === 'threat'}
        <div class="threat">
          ⚠ {torrent.scanEngine} found: {(torrent.threats ?? []).join(', ')}. Don't open these files.
          Removing the torrent with its files is the safest option.
        </div>
      {:else if torrent.scanState === 'error'}
        <div class="muted">Not scanned for malware: {torrent.scanError}.</div>
      {/if}
    </div>
  {/if}
  <PieceMap hash={torrent.hash} />
  {#if files.length === 0}
    <div class="empty">Waiting for metadata from peers…</div>
  {:else}
    <div class="list">
      <table>
        <thead>
          <tr>
            <th class="check">
              <input
                type="checkbox"
                checked={allWanted}
                indeterminate={!allWanted && !noneWanted}
                onchange={() => setWanted(files.map(f => f.index), !allWanted)}
                title="Select all"
              />
            </th>
            <th>File</th><th class="num">Size</th><th class="num">Done</th>
          </tr>
        </thead>
        <tbody>
          {#each files as f (f.index)}
            <tr class:skipped={!f.wanted}>
              <td class="check">
                <input type="checkbox" checked={f.wanted} onchange={() => setWanted([f.index], !f.wanted)} />
              </td>
              <td class="path" title={f.path}>
                {#if flagged.has(f.path)}<span class="flag">⚠</span>{/if}{f.path}
              </td>
              <td class="num">{bytes(f.size)}</td>
              <td class="num">{f.size ? ((f.done / f.size) * 100).toFixed(0) : 100}%</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

<style>
  .panel { display: flex; flex-direction: column; min-height: 0; height: 100%; }
  .head {
    display: flex; align-items: center; gap: 10px; padding: 8px 14px;
    border-bottom: 1px solid var(--border); background: var(--panel);
  }
  .head b { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
  .muted { color: var(--muted); white-space: nowrap; }
  .empty { padding: 16px; color: var(--muted); }
  .safety { padding: 8px 14px; border-bottom: 1px solid var(--border); display: grid; gap: 4px; font-size: 13px; user-select: text; }
  .safety .caution { color: var(--warn); }
  .safety .threat { color: var(--err); font-weight: 600; }
  .safety .ok { color: var(--ok); }
  .flag { color: var(--warn); font-weight: 700; margin-right: 5px; }
  .list { overflow: auto; flex: 1; }
  table { width: 100%; border-collapse: collapse; font-variant-numeric: tabular-nums; }
  th {
    position: sticky; top: 0; background: var(--bg); color: var(--muted); font-weight: 500;
    text-align: left; padding: 5px 10px; border-bottom: 1px solid var(--border);
  }
  td { padding: 4px 10px; border-bottom: 1px solid var(--border); white-space: nowrap; }
  .check { width: 32px; }
  .path { max-width: 0; width: 100%; overflow: hidden; text-overflow: ellipsis; }
  .num { text-align: right; }
  tr.skipped td:not(.check) { color: var(--muted); text-decoration: line-through; }
</style>
