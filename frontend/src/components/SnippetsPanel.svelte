<script lang="ts">
  import { createEventDispatcher, onMount, tick } from 'svelte'
  import { t, tr } from '../lib/i18n'
  import Icon from '../lib/Icon.svelte'
  import { api, errorText, type Snippet } from '../lib/api'
  import { broadcast } from '../lib/broadcast'
  import { confirmDialog, questionsDialog } from '../lib/dialog'
  import { fill, search, toSend, variables, visibleFor } from '../lib/snippets'
  import { keys } from '../lib/platform'
  import { openAppTab, toast } from '../lib/stores'
  import SnippetDialog from './SnippetDialog.svelte'

  // Panel de snippets superpuesto en una terminal: buscar, usar, crear, editar, duplicar y borrar.
  /** Grupo de la conexión de la terminal; null = terminal de esta PC (solo ve los globales). */
  export let groupPath: string | null

  const dispatch = createEventDispatcher<{ send: { text: string; enter: boolean; all?: boolean }; close: void }>()

  let all: Snippet[] = []
  let loaded = false
  let query = ''
  let sel = 0
  let input: HTMLInputElement
  let listEl: HTMLDivElement
  let editing: Partial<Snippet> | null = null

  $: broadcasting = $broadcast.targets.length

  $: list = search(visibleFor(all, groupPath), query)
  $: if (sel >= list.length) sel = Math.max(0, list.length - 1)
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

  onMount(() => {
    load()
    input?.focus()
  })

  /** Pide las variables (si tiene) y manda el comando a la terminal; cancelar el diálogo no manda nada. */
  async function use(s: Snippet, mode: Snippet['mode'] = s.mode, all = false) {
    let command = s.command
    const names = variables(command)
    if (names.length) {
      const answers = await questionsDialog({
        title: s.name,
        message: tr('snippets.fillVars'),
        questions: names.map((text) => ({ text, echo: true })),
      })
      if (!answers) return
      command = fill(command, Object.fromEntries(names.map((n, i) => [n, answers[i] ?? ''])))
    }
    dispatch('send', { ...toSend(command, mode), all })
    dispatch('close')
  }

  async function duplicate(s: Snippet) {
    try {
      await api.snippetSave({ ...s, id: undefined, name: tr('snippets.copyName', { name: s.name }) })
      await load()
      toast(tr('snippets.duplicated'), 'success')
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function remove(s: Snippet) {
    const ok = await confirmDialog({
      title: tr('snippets.deleteTitle'),
      message: tr('snippets.deleteMsg', { name: s.name }),
      okText: tr('snippets.delete'),
      danger: true,
    })
    if (!ok) return
    try {
      await api.snippetDelete(s.id)
      await load()
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  // La pestaña Snippets, para editarlos con más lugar (y todos, no solo los de esta conexión).
  function manage() {
    dispatch('close')
    openAppTab('snippets')
  }

  async function closeEditor() {
    editing = null
    await tick()
    input?.focus()
  }

  function move(d: number) {
    if (!list.length) return
    sel = (sel + d + list.length) % list.length
    tick().then(() => listEl?.querySelector('.row.sel')?.scrollIntoView({ block: 'nearest' }))
  }

  function onInputKey(e: KeyboardEvent) {
    if (e.key === 'ArrowDown') move(1)
    else if (e.key === 'ArrowUp') move(-1)
    else if (e.key === 'Enter' && list[sel]) use(list[sel])
    else return
    e.preventDefault()
  }

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape' || ((e.ctrlKey || e.metaKey) && e.shiftKey && e.code === 'KeyS')) {
      e.preventDefault()
      e.stopPropagation()
      dispatch('close')
    }
  }
</script>

<div class="panel" role="presentation" on:keydown={onKey} on:contextmenu|stopPropagation>
  <div class="head">
    <input bind:this={input} bind:value={query} on:input={() => (sel = 0)} on:keydown={onInputKey} placeholder={$t('snippets.search')} spellcheck="false" autocomplete="off" />
    <button class="icon" title={$t('snippets.new')} aria-label={$t('snippets.new')} on:click={() => (editing = {})}><Icon name="plus" size={14} /></button>
    <button class="icon" title={$t('snippets.manage')} aria-label={$t('snippets.manage')} on:click={manage}><Icon name="edit" size={14} /></button>
    <button class="icon" title={$t('snippets.closeTip')} aria-label={$t('common.close')} on:click={() => dispatch('close')}><Icon name="x" size={14} /></button>
  </div>

  <div class="list" role="listbox" aria-label={$t('snippets.list')} bind:this={listEl}>
    {#each sections as sec (sec.folder)}
      {#if sec.folder || sections.length > 1}
        <div class="folder">{sec.folder || $t('snippets.noFolder')}</div>
      {/if}
      {#each sec.items as s (s.id)}
        {@const i = list.indexOf(s)}
        <div
          class="row"
          role="option"
          aria-selected={i === sel}
          tabindex="-1"
          class:sel={i === sel}
          class:bc={broadcasting > 0}
          on:keydown={(e) => e.target === e.currentTarget && e.key === 'Enter' && use(s)}
          on:click={() => (sel = i)} on:dblclick={() => use(s)} on:mouseenter={() => (sel = i)}>
          <div class="info">
            <span class="name" title={s.name}>{s.name}</span>
            <span class="cmd mono" title={s.command}>{s.command.split('\n')[0]}{s.command.includes('\n') ? ' …' : ''}</span>
          </div>
          <span class="tag" title={s.mode === 'run' ? $t('snippets.runTip') : $t('snippets.pasteTip')}>{s.mode === 'run' ? $t('snippets.run') : $t('snippets.paste')}</span>
          {#if s.group}<span class="tag grp" title={$t('snippets.groupTip', { group: s.group })}>{s.group}</span>{/if}
          <div class="acts">
            <button class="act" on:click|stopPropagation={() => use(s, 'paste')}>{$t('snippets.pasteBtn')}</button>
            <button class="act" on:click|stopPropagation={() => use(s, 'run')}>{$t('snippets.runBtn')}</button>
            {#if broadcasting}
              <button class="act bcast" on:click|stopPropagation={() => use(s, 'run', true)}>{$t('snippets.runAll', { count: broadcasting })}</button>
            {/if}
            <button class="icon" title={$t('snippets.edit')} aria-label={$t('snippets.edit')} on:click|stopPropagation={() => (editing = s)}><Icon name="edit" size={13} /></button>
            <button class="icon" title={$t('snippets.duplicate')} aria-label={$t('snippets.duplicate')} on:click|stopPropagation={() => duplicate(s)}><Icon name="copy" size={13} /></button>
            <button class="icon" title={$t('snippets.delete')} aria-label={$t('snippets.delete')} on:click|stopPropagation={() => remove(s)}><Icon name="trash" size={13} /></button>
          </div>
        </div>
      {/each}
    {:else}
      <div class="empty">
        {#if !loaded}
          {$t('snippets.loading')}
        {:else if all.length && !visibleFor(all, groupPath).length}
          {$t('snippets.noneForConn')}
        {:else if query.trim()}
          {$t('snippets.noMatch')}
        {:else}
          {$t('snippets.noneYet')}
        {/if}
      </div>
    {/each}
  </div>
  <div class="foot">{$t('snippets.foot', { keys: keys('Ctrl+Shift+S') })}</div>
</div>

{#if editing}
  <SnippetDialog initial={editing} {folders} on:saved={load} on:close={closeEditor} />
{/if}

<style>
  /* Superpuesto arriba a la derecha, debajo de la barra de búsqueda de la terminal (deja libre la barra de scroll). */
  .panel {
    position: absolute;
    top: 46px;
    right: 18px;
    z-index: 7;
    width: min(440px, calc(100% - 36px));
    max-height: calc(100% - 58px);
    display: flex;
    flex-direction: column;
    background: var(--rail);
    border: 1px solid var(--border);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 4px;
    border-bottom: 1px solid var(--border);
  }
  .head input {
    flex: 1;
    min-width: 0;
    padding: 2px 6px;
    background: var(--bg);
  }
  .list {
    overflow: auto;
    min-height: 0;
  }
  .folder {
    padding: 6px 10px 2px;
    font-size: 10px;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .row {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 5px 8px 5px 10px;
    cursor: default;
  }
  .row.sel {
    background: var(--bg-4);
  }
  .info {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .name,
  .cmd {
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
  .tag {
    flex: none;
    max-width: 90px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    padding: 0 5px;
    border: 1px solid var(--border);
    color: var(--text-3);
    font-size: 10px;
  }
  .tag.grp {
    color: var(--text-2);
  }
  /* Las acciones solo se muestran en la fila seleccionada. */
  .acts {
    display: none;
    flex: none;
    align-items: center;
    gap: 2px;
  }
  .row.sel .acts {
    display: flex;
  }
  /* Con un broadcast en marcha hay un botón más largo: las acciones pasan a su propia línea. */
  .row.sel.bc {
    flex-wrap: wrap;
  }
  .row.sel.bc .acts {
    flex-basis: 100%;
    justify-content: flex-end;
  }
  .act.bcast {
    border-color: var(--warning);
    color: var(--warning);
  }
  .row.sel .tag {
    display: none;
  }
  .act {
    padding: 1px 6px;
    font-size: 11px;
  }
  .empty {
    padding: 14px 12px;
    color: var(--text-3);
    font-size: 12px;
    text-align: center;
  }
  .foot {
    padding: 3px 10px;
    border-top: 1px solid var(--border);
    color: var(--text-3);
    font-size: 10px;
  }
</style>
