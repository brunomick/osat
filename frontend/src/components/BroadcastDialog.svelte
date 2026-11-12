<script lang="ts">
  import { t as tl } from '../lib/i18n'
  import { tabTitle } from '../lib/tabtitle'
  import { focusTrap } from '../lib/focustrap'
  import Icon from '../lib/Icon.svelte'
  import { broadcast, candidates, defaultSelection, start } from '../lib/broadcast'
  import { panes, layout } from '../lib/layout'
  import { broadcastDialogOpen, kindIcon, tabs } from '../lib/stores'
  import { holdOverlay } from '../lib/overlay'
  import { onDestroy } from 'svelte'

  onDestroy(holdOverlay())

  // Elegir las terminales abiertas y conectadas a las que va lo que se escribe o se pega en cualquiera de ellas.
  const close = () => broadcastDialogOpen.set(false)

  // Una lista fija al abrir (si una terminal se cae con el diálogo abierto, el broadcast la saca solo).
  const cands = candidates($tabs)
  const active = $broadcast.targets.length > 0
  // Con un broadcast en marcha vienen marcadas las que participan; si no, las que se ven en el mosaico.
  let selected = new Set(
    active
      ? $broadcast.targets.filter((id) => cands.some((t) => t.id === id))
      : defaultSelection(
          cands,
          panes($layout).flatMap((p) => (p.active ? [p.active] : [])),
        ),
  )

  function toggle(id: string) {
    selected.has(id) ? selected.delete(id) : selected.add(id)
    selected = selected
  }

  function go() {
    // En el orden en que están abiertas.
    start(cands.filter((t) => selected.has(t.id)).map((t) => t.id))
    close()
  }
</script>

<div class="backdrop" role="presentation" on:mousedown|self={close} on:keydown={(e) => e.key === 'Escape' && close()}>
  <form class="modal" on:submit|preventDefault={go} role="dialog" aria-modal="true" use:focusTrap aria-labelledby="broadcast-title">
    <h2 id="broadcast-title">{$tl('broadcast.title')}</h2>
    <p class="muted">{$tl('broadcast.intro')}</p>

    <div class="list">
      {#each cands as t (t.id)}
        <label class="row">
          <input type="checkbox" checked={selected.has(t.id)} on:change={() => toggle(t.id)} />
          <span class="dot" style="background:{t.color || 'transparent'}" />
          <Icon name={kindIcon[t.kind]} size={13} />
          <span class="title" title={tabTitle($tl, t)}>{tabTitle($tl, t)}</span>
        </label>
      {:else}
        <div class="empty">{$tl('broadcast.none')}</div>
      {/each}
    </div>

    <footer>
      <span class="muted">{$tl(selected.size === 1 ? 'broadcast.chosen.one' : 'broadcast.chosen.other', { count: selected.size })}</span>
      <span class="spacer" />
      <button type="button" on:click={close}>{$tl('common.cancel')}</button>
      <button class="primary" type="submit" disabled={selected.size < 2}>{active ? $tl('broadcast.apply') : $tl('broadcast.start')}</button>
    </footer>
  </form>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    z-index: 400;
  }
  .modal {
    width: 460px;
    max-width: 94vw;
    max-height: 92vh;
    background: var(--bg-2);
    border: 1px solid var(--border);
    padding: 20px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  h2 {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
  }
  .muted {
    margin: 0;
    color: var(--text-3);
    font-size: 12px;
  }
  .list {
    max-height: 320px;
    overflow: auto;
    border: 1px solid var(--border);
    background: var(--bg);
  }
  .row {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 10px;
    color: var(--text-2);
  }
  .row:hover {
    background: var(--bg-3);
  }
  .dot {
    flex: none;
    width: 8px;
    height: 8px;
  }
  .title {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text);
    font-size: 13px;
  }
  .empty {
    padding: 14px;
    text-align: center;
    color: var(--text-3);
    font-size: 12px;
  }
  footer {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .spacer {
    flex: 1;
  }
</style>
