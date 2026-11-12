<script lang="ts">
  import { t } from '../lib/i18n'
  import { createEventDispatcher, onMount } from 'svelte'
  import { api, errorText, type Snippet } from '../lib/api'
  import { connections, groupPaths, toast } from '../lib/stores'

  // Campos de un snippet: un comando guardado con su carpeta, su modo (pegar o ejecutar) y dónde aparece
  // (en todas las terminales o solo en las de un grupo de conexiones). Lo usan el diálogo del panel
  // (SnippetDialog) y la pestaña Snippets (SnippetsView).
  export let initial: Partial<Snippet> = {}
  /** Carpetas que ya existen, para sugerirlas. */
  export let folders: string[] = []
  /** Texto del botón que no guarda (en el diálogo, Cancelar; en la pestaña, Descartar cambios). */
  export let cancelText = ''

  const dispatch = createEventDispatcher<{ cancel: void; saved: Snippet }>()

  let name = initial.name ?? ''
  let folder = initial.folder ?? ''
  let command = initial.command ?? ''
  let mode: Snippet['mode'] = initial.mode ?? 'paste'
  let group = initial.group ?? ''
  let saving = false
  let nameInput: HTMLInputElement

  // Los grupos que hay, con sus ancestros (un snippet de "A" vale para "A/B" aunque "A" no tenga conexiones propias).
  $: groups = (() => {
    const all = new Set<string>()
    for (const g of [...$groupPaths, ...$connections.map((c) => c.group), group]) {
      const parts = g.split('/').filter(Boolean)
      for (let i = 1; i <= parts.length; i++) all.add(parts.slice(0, i).join('/'))
    }
    return [...all].sort((a, b) => a.localeCompare(b))
  })()

  $: canSave = !!name.trim() && !!command.trim()

  async function save() {
    saving = true
    try {
      const saved = await api.snippetSave({ id: initial.id, name: name.trim(), folder: folder.trim(), command, mode, group })
      dispatch('saved', saved)
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      saving = false
    }
  }

  onMount(() => nameInput?.focus())
</script>

<form on:submit|preventDefault={save}>
  <label>
    {$t('snippets.name')}
    <input bind:this={nameInput} bind:value={name} placeholder={$t('snippets.namePlaceholder')} spellcheck="false" required />
  </label>

  <label>
    {$t('snippets.command')}
    <textarea class="mono" rows="6" bind:value={command} placeholder={'sudo systemctl restart {{' + $t('snippets.varExample') + '}}'} spellcheck="false" required />
    <span class="dim">{$t('snippets.helpA')} <span class="mono">{'{{' + $t('snippets.varExample2') + '}}'}</span> {$t('snippets.helpB')}</span>
  </label>

  <div class="field">
    <span class="lbl">{$t('snippets.mode')}</span>
    <div class="seg">
      <button type="button" class:on={mode === 'paste'} on:click={() => (mode = 'paste')}>{$t('snippets.pasteBtn')}</button>
      <button type="button" class:on={mode === 'run'} on:click={() => (mode = 'run')}>{$t('snippets.runBtn')}</button>
    </div>
    <span class="dim">{mode === 'paste' ? $t('snippets.modePasteHelp') : $t('snippets.modeRunHelp')}</span>
  </div>

  <div class="row">
    <label>
      {$t('snippets.folder')}
      <input bind:value={folder} list="snippet-folders" placeholder="Web/Nginx" spellcheck="false" />
      <datalist id="snippet-folders">
        {#each folders as f}<option value={f} />{/each}
      </datalist>
    </label>
    <label>
      {$t('snippets.appearsIn')}
      <select bind:value={group}>
        <option value="">{$t('snippets.allTerminals')}</option>
        {#each groups as g}<option value={g}>{$t('snippets.groupOption', { group: g })}</option>{/each}
      </select>
    </label>
  </div>
  {#if group}<span class="dim">{$t('snippets.groupHelp', { group })}</span>{/if}

  <footer>
    <span class="spacer" />
    <button type="button" on:click={() => dispatch('cancel')}>{cancelText || $t('common.cancel')}</button>
    <button class="primary" type="submit" disabled={saving || !canSave}>{saving ? $t('form.saving') : $t('common.save')}</button>
  </footer>
</form>

<style>
  form {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  label,
  .field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    color: var(--text-2);
    font-size: 12px;
  }
  label input,
  label select,
  label textarea {
    color: var(--text);
    font-size: 13px;
  }
  textarea {
    resize: vertical;
    white-space: pre;
  }
  .row {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 12px;
  }
  /* Sin esto, el ancho propio del desplegable (el nombre de grupo más largo) lo saca de su columna. */
  .row label {
    min-width: 0;
  }
  .row select {
    width: 100%;
  }
  .seg {
    display: flex;
    border: 1px solid var(--border);
    overflow: hidden;
  }
  .seg button {
    flex: 1;
    border: 0;
    background: var(--bg);
  }
  .seg button + button {
    border-left: 1px solid var(--border);
  }
  .seg button.on {
    background: var(--accent);
    color: var(--on-accent);
  }
  .dim {
    color: var(--text-3);
    font-size: 11px;
  }
  footer {
    display: flex;
    gap: 8px;
    margin-top: 4px;
  }
  .spacer {
    flex: 1;
  }
</style>
