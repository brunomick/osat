<script lang="ts">
  import { focusTrap } from '../lib/focustrap'
  import { t } from '../lib/i18n'
  import { createEventDispatcher, onDestroy } from 'svelte'
  import type { Snippet } from '../lib/api'
  import SnippetForm from './SnippetForm.svelte'
  import { holdOverlay } from '../lib/overlay'

  onDestroy(holdOverlay())

  // Crear o editar un snippet desde el panel de la terminal (los campos están en SnippetForm).
  export let initial: Partial<Snippet> = {}
  /** Carpetas que ya existen, para sugerirlas. */
  export let folders: string[] = []

  const dispatch = createEventDispatcher<{ close: void; saved: Snippet }>()

  const isNew = !initial.id

  function saved(e: CustomEvent<Snippet>) {
    dispatch('saved', e.detail)
    dispatch('close')
  }
</script>

<div class="backdrop" role="presentation" on:mousedown|self={() => dispatch('close')} on:keydown={(e) => e.key === 'Escape' && dispatch('close')}>
  <div class="modal" role="dialog" aria-modal="true" use:focusTrap aria-labelledby="snippet-title">
    <h2 id="snippet-title">{isNew ? $t('snippets.new') : $t('snippets.editTitle')}</h2>
    <SnippetForm {initial} {folders} on:saved={saved} on:cancel={() => dispatch('close')} />
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
    width: 520px;
    max-width: 94vw;
    max-height: 92vh;
    overflow: auto;
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
</style>
