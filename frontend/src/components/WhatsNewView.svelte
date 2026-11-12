<script lang="ts">
  import { onMount } from 'svelte'
  import { t } from '../lib/i18n'
  import { renderMarkdown } from '../lib/markdown'
  import { closeTab, patchTab, type Tab } from '../lib/stores'
  import { whatsNewNotes } from '../lib/updates'

  // Pestaña "Novedades de osat X.Y.Z": lo que trae la versión que se acaba de instalar (del CHANGELOG).
  export let tab: Tab

  onMount(() => patchTab(tab.id, { status: 'ready' }))
</script>

<div class="page">
  <div class="body">
    <h2>{$t('update.newsTitle', { version: tab.title })}</h2>
    <div class="notes">{@html renderMarkdown($whatsNewNotes)}</div>
    <div class="actions">
      <button class="primary" on:click={() => closeTab(tab.id)}>{$t('update.gotIt')}</button>
    </div>
  </div>
</div>

<style>
  .page {
    height: 100%;
    overflow: auto;
    background: var(--bg-2);
  }
  .body {
    max-width: 820px;
    padding: 24px 28px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  h2 {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
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
  .actions {
    display: flex;
  }
</style>
