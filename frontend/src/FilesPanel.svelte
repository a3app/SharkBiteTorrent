<script lang="ts">
  import { GetFiles, SetFilesWanted } from '../wailsjs/go/main/App.js'
  import type { engine } from '../wailsjs/go/models'
  import { bytes } from './format'

  let { torrent, onError }: { torrent: engine.Status; onError: (e: string) => void } = $props()

  let files = $state<engine.FileStatus[]>([])
  const allWanted = $derived(files.length > 0 && files.every(f => f.wanted))
  const noneWanted = $derived(files.every(f => !f.wanted))

  async function load() {
    try {
      files = (await GetFiles(torrent.hash)) ?? []
    } catch (e) {
      onError(String(e))
    }
  }

  // Reload when the selection changes, and poll for progress.
  $effect(() => {
    torrent.hash
    load()
    const id = setInterval(load, 1500)
    return () => clearInterval(id)
  })

  async function setWanted(indexes: number[], wanted: boolean) {
    try {
      await SetFilesWanted(torrent.hash, indexes, wanted)
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
              <td class="path" title={f.path}>{f.path}</td>
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
