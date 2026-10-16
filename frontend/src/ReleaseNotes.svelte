<script lang="ts">
  import type { main } from '../wailsjs/go/models'

  // Items are already HTML-safe: the Go side escapes them and only adds
  // <b> and <code> (see releasenotes.go).
  let { notes }: { notes: main.ReleaseNote[] } = $props()
</script>

<div class="notes">
  <!-- Keyed by position: a duplicated heading in CHANGELOG.md must not break rendering. -->
  {#each notes as n, i (i)}
    <section>
      <h4>{n.version}{#if n.date}<span class="muted"> · {n.date}</span>{/if}</h4>
      {#each n.text ?? [] as p}
        <p>{@html p}</p>
      {/each}
      {#each n.groups ?? [] as g, j (j)}
        <div class="group">{g.title}</div>
        <ul>
          {#each g.items ?? [] as item}
            <li>{@html item}</li>
          {/each}
        </ul>
      {/each}
    </section>
  {/each}
</div>

<style>
  .notes { max-height: 260px; overflow: auto; font-size: 13px; line-height: 1.45; }
  section + section { margin-top: 10px; border-top: 1px solid var(--border); padding-top: 8px; }
  h4 { margin: 0 0 4px; font-size: 14px; }
  .muted { color: var(--muted); font-weight: normal; }
  .group { color: var(--muted); font-size: 12px; text-transform: uppercase; letter-spacing: 0.03em; margin-top: 6px; }
  ul { margin: 2px 0 0; padding-left: 18px; }
  li { margin: 2px 0; }
  p { margin: 4px 0; }
  :global(.notes code) { font-size: 12px; background: var(--row-hover); padding: 0 4px; border-radius: 4px; }
</style>
