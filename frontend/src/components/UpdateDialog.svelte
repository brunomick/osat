<script lang="ts">
  import { onDestroy } from 'svelte'
  import { api, fmtSize } from '../lib/api'
  import { focusTrap } from '../lib/focustrap'
  import { t } from '../lib/i18n'
  import { renderMarkdown } from '../lib/markdown'
  import { holdOverlay } from '../lib/overlay'
  import { installUpdate, updateUi } from '../lib/updates'

  // Aviso de versión nueva, progreso de la instalación y error: ocupan toda la ventana, debajo de la barra de título.
  export let top = 30

  onDestroy(holdOverlay()) // la vista nativa de un sitio se dibuja encima del HTML: mientras se ve esto, se oculta

  $: u = $updateUi
  // Mientras se instala no se puede cerrar.
  const close = () => {
    if (u?.kind !== 'progress') updateUi.set(null)
  }
  const openPage = () => (api.updateOpenPage(), updateUi.set(null))
  $: pct = u?.kind === 'progress' && u.total > 0 ? Math.min(100, Math.round((u.done / u.total) * 100)) : 0
</script>

<svelte:window on:keydown={(e) => e.key === 'Escape' && close()} />

{#if u}
  <div class="screen" style:top={`${top}px`} role="dialog" aria-modal="true" use:focusTrap aria-label={$t('update.label')}>
    <div class="content">
      {#if u.kind === 'available'}
        <h1>{$t('update.availableTitle')}</h1>
        <p class="lead">{$t('update.availableText', { latest: u.info.latest, current: u.info.current })}</p>
        {#if u.info.beta}
          <p class="beta">{$t('update.betaNote')}</p>
        {/if}
        {#if u.info.notes}
          <div class="notes">{@html renderMarkdown(u.info.notes)}</div>
        {/if}
        <p class="muted small">{u.info.canApply ? $t('update.restartNote') : u.info.manual}</p>
        <div class="actions">
          {#if u.info.canApply}
            <button class="primary" on:click={() => u?.kind === 'available' && installUpdate(u.info)}>{$t('update.installNow')}</button>
          {:else}
            <button class="primary" on:click={openPage}>{$t('update.goToDownload')}</button>
          {/if}
          <button on:click={close}>{$t('update.later')}</button>
        </div>
      {:else if u.kind === 'progress'}
        <h1>{$t('update.progressTitle', { version: u.info.latest })}</h1>
        <p class="lead">
          {#if u.restarting}{$t('update.restarting')}
          {:else if u.total > 0}{$t('update.downloading', { done: fmtSize(u.done), total: fmtSize(u.total) })}
          {:else}{$t('update.preparing')}{/if}
        </p>
        <div class="bar" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow={u.restarting ? 100 : pct}>
          <div class:indeterminate={!u.restarting && u.total === 0} style:width={u.restarting ? '100%' : `${pct}%`} />
        </div>
        <p class="muted small">{$t('update.progressNote')}</p>
      {:else if u.kind === 'error'}
        <h1>{$t('update.errorTitle')}</h1>
        <p class="lead err">{u.error}</p>
        <p class="muted small">{$t('update.errorNote')}</p>
        <div class="actions">
          <button class="primary" on:click={openPage}>{$t('update.goToDownload')}</button>
          <button on:click={close}>{$t('common.close')}</button>
        </div>
      {/if}
    </div>
  </div>
{/if}

<style>
  .screen {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 450;
    overflow: auto;
    background: var(--bg-2);
  }
  .content {
    max-width: 760px;
    margin: 0 auto;
    padding: 64px 32px 48px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  h1 {
    margin: 0;
    font-size: 22px;
    font-weight: 600;
  }
  p {
    margin: 0;
    line-height: 1.55;
  }
  .lead {
    font-size: 14px;
  }
  .small {
    font-size: 12px;
  }
  .beta {
    font-size: 12px;
    color: var(--warning);
  }
  .err {
    color: var(--danger);
    word-break: break-word;
  }
  .notes {
    font-size: 13px;
    line-height: 1.6;
    user-select: text;
  }
  .notes :global(h4) {
    margin: 14px 0 4px;
    font-size: 12px;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-2);
  }
  .notes :global(ul) {
    margin: 4px 0 8px;
    padding-left: 20px;
  }
  .notes :global(li) {
    margin: 6px 0;
  }
  .notes :global(p) {
    margin: 8px 0;
  }
  .notes :global(code) {
    font-family: var(--mono);
    font-size: 0.95em;
  }
  .bar {
    height: 6px;
    margin-top: 8px;
    background: var(--bg-4);
    overflow: hidden;
  }
  .bar > div {
    height: 100%;
    background: var(--accent);
    transition: width 0.2s;
  }
  .bar > div.indeterminate {
    width: 30% !important;
    animation: slide 1.2s ease-in-out infinite;
  }
  @keyframes slide {
    from {
      transform: translateX(-100%);
    }
    to {
      transform: translateX(340%);
    }
  }
  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    margin-top: 8px;
  }
</style>
