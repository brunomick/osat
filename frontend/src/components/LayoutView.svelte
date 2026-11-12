<script lang="ts">
  import { t } from '../lib/i18n'
  import Pane from './Pane.svelte'
  import { focusedPane, setRatio, type LayoutNode } from '../lib/layout'

  // Dibuja recursivamente el árbol de paneles con divisores arrastrables.
  export let node: LayoutNode

  let el: HTMLDivElement

  // Flechas (izquierda/derecha o arriba/abajo según la división): mueven el divisor de a 5 %.
  function splitKey(e: KeyboardEvent) {
    if (node.kind !== 'split') return
    const keys = node.dir === 'row' ? ['ArrowLeft', 'ArrowRight'] : ['ArrowUp', 'ArrowDown']
    const i = keys.indexOf(e.key)
    if (i < 0) return
    e.preventDefault()
    setRatio(node.id, node.ratio + (i ? 0.05 : -0.05))
  }

  function startDrag(e: MouseEvent) {
    if (node.kind !== 'split') return
    e.preventDefault()
    const id = node.id
    const row = node.dir === 'row'
    const rect = el.getBoundingClientRect()
    const move = (ev: MouseEvent) =>
      setRatio(id, row ? (ev.clientX - rect.left) / rect.width : (ev.clientY - rect.top) / rect.height)
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }
</script>

{#if node.kind === 'pane'}
  <!-- Una instancia por panel: si en este lugar aparece otro panel (al cerrar
       la última pestaña, o al colapsar una división) se monta de nuevo y
       publica su rectángulo con el id nuevo. Reutilizada, seguiría con el
       rectángulo del panel anterior y sus pestañas quedarían ocultas. -->
  {#key node.id}
    <Pane pane={node} focused={node.id === $focusedPane} />
  {/key}
{:else}
  <div class="split {node.dir}" bind:this={el}>
    <div class="child" style="flex: {node.ratio} 1 0">
      <svelte:self node={node.a} />
    </div>
    <div
      class="splitter"
      role="slider"
      aria-orientation={node.dir === 'row' ? 'vertical' : 'horizontal'}
      aria-label={$t('sftp.panelsSize')}
      aria-valuenow={Math.round(node.ratio * 100)}
      aria-valuemin={0}
      aria-valuemax={100}
      tabindex="0"
      on:mousedown={startDrag}
      on:keydown={splitKey}
    />
    <div class="child" style="flex: {1 - node.ratio} 1 0">
      <svelte:self node={node.b} />
    </div>
  </div>
{/if}

<style>
  .split {
    display: flex;
    width: 100%;
    height: 100%;
    min-width: 0;
    min-height: 0;
  }
  .split.col {
    flex-direction: column;
  }
  .child {
    display: flex;
    min-width: 0;
    min-height: 0;
  }
  .splitter {
    flex: none;
    background: var(--border);
  }
  .row > .splitter {
    width: 4px;
    cursor: col-resize;
  }
  .col > .splitter {
    height: 4px;
    cursor: row-resize;
  }
  .splitter:hover {
    background: var(--text-3);
  }
</style>
