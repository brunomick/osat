<script lang="ts">
  import { focusTrap } from '../lib/focustrap'
  import { t } from '../lib/i18n'
  import LogoMark from '../lib/LogoMark.svelte'
  import Wordmark from '../lib/Wordmark.svelte'
  import { aboutOpen } from '../lib/stores'
  import { version } from '../../package.json'
  import { holdOverlay } from '../lib/overlay'
  import { onDestroy } from 'svelte'

  onDestroy(holdOverlay()) // la vista nativa de un sitio se dibuja encima del HTML: mientras haya un diálogo, se oculta

  const close = () => aboutOpen.set(false)
</script>

<svelte:window on:keydown={(e) => e.key === 'Escape' && close()} />

<div class="backdrop" role="presentation" on:mousedown|self={close}>
  <div class="modal" role="dialog" aria-modal="true" use:focusTrap aria-label={$t('about.label')}>
    <div class="head">
      <LogoMark size={72} tile />
      <div class="name">
        <div class="title">
          <Wordmark height={26} />
          <span class="muted ver">v {version}</span>
        </div>
        <span class="full">OPEN SERVER ADMINISTRATION TOOL</span>
      </div>
    </div>
    <p>All your connections in one app.</p>
    <p class="small muted">Copyright (C) 2026 Bruno Mick<br />Open source software under GPL-3.0.</p>
    <div class="actions">
      <button class="primary" on:click={close}>{$t('common.close')}</button>
    </div>
  </div>
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
    width: 560px;
    max-width: 94vw;
    background: var(--bg-2);
    border: 1px solid var(--border);
    padding: 20px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 16px;
    margin-bottom: 4px;
  }
  /* OSAT (con la versión) y el nombre completo, juntos y centrados en el alto del ícono (.head). */
  .name {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .name :global(svg) {
    display: block; /* sin el espacio de la línea de texto debajo */
  }
  .full,
  .ver {
    line-height: 1;
  }
  .full {
    font-size: 10px;
    letter-spacing: 0.08em;
    color: var(--text-2);
  }
  /* La versión chiquita, abajo a la derecha de OSAT. */
  .title {
    display: flex;
    align-items: flex-end;
    gap: 6px;
  }
  .ver {
    font-size: 10px;
  }
  p {
    margin: 0;
    line-height: 1.5;
  }
  .small {
    font-size: 12px;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 4px;
  }
</style>
