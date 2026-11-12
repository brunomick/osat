<script lang="ts">
  import { onMount } from 'svelte'
  import { t, tr } from '../lib/i18n'
  import Icon from '../lib/Icon.svelte'
  import { api, errorText, type Snippet } from '../lib/api'
  import { confirmDialog } from '../lib/dialog'
  import { search } from '../lib/snippets'
  import { patchTab, toast, type Tab } from '../lib/stores'
  import SnippetForm from './SnippetForm.svelte'

  // Pestaña para administrar los snippets: todos (de cualquier grupo) a la izquierda y el elegido a la derecha,
  // para editarlo, duplicarlo o borrarlo. Usarlos sigue siendo desde el panel de cada terminal.
  export let tab: Tab

  let all: Snippet[] = []
  let loaded = false
  let query = ''
  /** El que se edita: uno existente, {} para uno nuevo o null (ninguno). */
  let editing: Partial<Snippet> | null = null
  let formKey = 0 // cambia para rearmar el formulario (otro snippet, o descartar cambios)

  $: list = search(all, query)
  // Las carpetas ya vienen ordenadas del backend: los de una misma carpeta quedan juntos.
  $: sections = list.reduce<{ folder: string; items: Snippet[] }[]>((acc, s) => {
    const last = acc[acc.length - 1]
    if (last && last.folder === s.folder) last.items.push(s)
    else acc.push({ folder: s.folder, items: [s] })
    return acc
  }, [])
  $: folders = [...new Set(all.map((s) => s.folder).filter(Boolean))]

  async function load() {
    try {
      all = (await api.snippetsList()) ?? []
    } catch (e) {
      toast(tr('snippets.loadError', { error: errorText(e) }), 'error')
    }
    loaded = true
  }

  function pick(s: Partial<Snippet> | null) {
    editing = s
    formKey++
  }

  async function saved(e: CustomEvent<Snippet>) {
    await load()
    pick(e.detail)
    toast(tr('snippets.saved'), 'success', 1500)
  }

  async function duplicate(s: Partial<Snippet>) {
    try {
      const copy = await api.snippetSave({ ...(s as Snippet), id: undefined, name: tr('snippets.copyName', { name: s.name ?? '' }) })
      await load()
      pick(copy)
      toast(tr('snippets.duplicated'), 'success')
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function remove(s: Partial<Snippet>) {
    const ok = await confirmDialog({
      title: tr('snippets.deleteTitle'),
      message: tr('snippets.deleteMsg', { name: s.name ?? '' }),
      okText: tr('snippets.delete'),
      danger: true,
    })
    if (!ok || !s.id) return
    try {
      await api.snippetDelete(s.id)
      await load()
      pick(null)
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  // Descartar: uno nuevo se cierra; uno existente vuelve a lo guardado.
  const discard = () => pick(editing?.id ? editing : null)

  onMount(() => {
    patchTab(tab.id, { status: 'ready' })
    load()
  })
</script>

<div class="page">
  <aside class="side">
    <div class="head">
      <input bind:value={query} placeholder={$t('snippets.search')} spellcheck="false" autocomplete="off" />
      <button class="icon" title={$t('snippets.new')} aria-label={$t('snippets.new')} on:click={() => pick({})}><Icon name="plus" size={14} /></button>
    </div>
    <div class="list" role="listbox" aria-label={$t('snippets.list')}>
      {#each sections as sec (sec.folder)}
        {#if sec.folder || sections.length > 1}
          <div class="folder">{sec.folder || $t('snippets.noFolder')}</div>
        {/if}
        {#each sec.items as s (s.id)}
          <div
            class="row"
            role="option"
            aria-selected={editing?.id === s.id}
            tabindex="0"
            class:sel={editing?.id === s.id}
            on:click={() => pick(s)}
            on:keydown={(e) => e.key === 'Enter' && pick(s)}
          >
            <span class="name" title={s.name}>{s.name}</span>
            <span class="cmd mono" title={s.command}>{s.command.split('\n')[0]}{s.command.includes('\n') ? ' …' : ''}</span>
            {#if s.group}<span class="grp" title={$t('snippets.groupTip', { group: s.group })}>{s.group}</span>{/if}
          </div>
        {/each}
      {:else}
        <div class="empty">
          {#if !loaded}
            {$t('snippets.loading')}
          {:else if query.trim()}
            {$t('snippets.noMatch')}
          {:else}
            {$t('snippets.noneYet')}
          {/if}
        </div>
      {/each}
    </div>
  </aside>

  <section class="edit">
    {#if editing}
      <header>
        <h2>{editing.id ? $t('snippets.editTitle') : $t('snippets.new')}</h2>
        {#if editing.id}
          <button on:click={() => editing && duplicate(editing)}><Icon name="copy" size={13} /> {$t('snippets.duplicate')}</button>
          <button class="danger" on:click={() => editing && remove(editing)}><Icon name="trash" size={13} /> {$t('snippets.delete')}</button>
        {/if}
      </header>
      {#key formKey}
        <SnippetForm initial={editing} {folders} cancelText={editing.id ? $t('snippets.discard') : ''} on:saved={saved} on:cancel={discard} />
      {/key}
    {:else}
      <p class="hint">{$t('snippets.manageHint')}</p>
    {/if}
  </section>
</div>

<style>
  .page {
    height: 100%;
    display: grid;
    grid-template-columns: minmax(240px, 340px) minmax(0, 1fr);
    background: var(--bg-2);
  }
  .side {
    display: flex;
    flex-direction: column;
    min-height: 0;
    border-right: 1px solid var(--border);
    background: var(--bg);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 8px;
    border-bottom: 1px solid var(--border);
  }
  .head input {
    flex: 1;
    min-width: 0;
  }
  .list {
    flex: 1;
    overflow: auto;
    min-height: 0;
  }
  .folder {
    padding: 8px 12px 2px;
    font-size: 10px;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .row {
    display: flex;
    flex-direction: column;
    padding: 6px 12px;
    cursor: default;
    border-left: 2px solid transparent;
  }
  .row:hover {
    background: var(--bg-3);
  }
  .row.sel {
    background: var(--bg-4);
    border-left-color: var(--accent);
  }
  .name,
  .cmd,
  .grp {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .name {
    color: var(--text);
    font-size: 13px;
  }
  .cmd {
    color: var(--text-3);
    font-size: 11px;
  }
  .grp {
    color: var(--text-2);
    font-size: 10px;
  }
  .empty {
    padding: 14px 12px;
    color: var(--text-3);
    font-size: 12px;
    text-align: center;
  }
  .edit {
    overflow: auto;
    padding: 24px 28px;
  }
  .edit :global(form) {
    max-width: 720px;
  }
  header {
    display: flex;
    align-items: center;
    gap: 8px;
    max-width: 720px;
    margin-bottom: 14px;
  }
  h2 {
    flex: 1;
    margin: 0;
    font-size: 16px;
    font-weight: 600;
  }
  header button {
    display: inline-flex;
    align-items: center;
    gap: 5px;
  }
  .hint {
    color: var(--text-3);
    font-size: 13px;
  }
</style>
