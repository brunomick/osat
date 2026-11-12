<script lang="ts">
  // Panel de búsqueda de archivos del explorador SFTP: busca en la carpeta actual y sus subcarpetas, por
  // nombre (con comodines) y opcionalmente por el texto que contienen. Los resultados llegan en tandas por
  // eventos mientras el servidor busca (ver internal/remote/search.go).
  import { createEventDispatcher, onDestroy, onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { t, tr } from '../lib/i18n'
  import ContextMenu, { type MenuItem } from './ContextMenu.svelte'
  import { api, clipboardSet, errorText, fmtSize, on, type FileEntry, type SearchCaps, type SearchDone } from '../lib/api'

  export let sessionId: string
  /** Carpeta donde se busca (la que muestra el explorador al apretar Buscar). */
  export let cwd: string

  const dispatch = createEventDispatcher<{ open: FileEntry; reveal: FileEntry; close: void }>()

  let name = ''
  let contains = ''
  let caps: SearchCaps = { content: true }
  let results: FileEntry[] = []
  let running = false
  let done: SearchDone | null = null
  let error = ''
  let root = '' // carpeta real donde busca (cwd puede ser un enlace); las rutas relativas salen de acá
  let searchId = ''
  let selected = ''
  let menu: { x: number; y: number; items: MenuItem[] } | null = null
  let nameEl: HTMLInputElement
  let offs: (() => void)[] = []

  /** Lleva el foco a la caja del nombre (Ctrl+F con el panel ya abierto). */
  export function focus() {
    nameEl?.focus()
    nameEl?.select()
  }

  // Qué puede hacer el servidor (si no hay find o exec, el contenido se muestra deshabilitado).
  function loadCaps(sid: string) {
    api
      .searchCaps(sid)
      .then((c) => (caps = c))
      .catch(() => {})
  }
  $: loadCaps(sessionId)
  $: if (!caps.content) contains = ''

  const newId = () => globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`

  function unlisten() {
    offs.forEach((f) => f())
    offs = []
  }

  async function start() {
    if (running) return
    unlisten()
    const id = newId()
    searchId = id
    results = []
    done = null
    error = ''
    selected = ''
    root = ''
    running = true
    // Antes de pedir la búsqueda, para no perder los primeros resultados.
    offs = [
      on('search:results:' + id, (batch: FileEntry[]) => (results = results.concat(batch))),
      on('search:done:' + id, (d: SearchDone) => {
        done = d
        running = false
        if (d.error) error = d.error
      }),
    ]
    try {
      root = await api.searchStart(id, sessionId, cwd, name.trim(), caps.content ? contains : '')
    } catch (e) {
      error = errorText(e)
      running = false
      unlisten()
    }
  }

  function stop() {
    if (running) api.searchStop(searchId).catch(() => {})
  }

  function close() {
    stop()
    unlisten()
    dispatch('close')
  }

  // Si el explorador cierra el panel o la conexión, la búsqueda se corta en el servidor.
  onMount(focus)
  onDestroy(() => {
    stop()
    unlisten()
  })

  // Ruta relativa a la carpeta de la búsqueda: la carpeta de adentro y el nombre.
  const prefix = () => (root.endsWith('/') ? root : root + '/')
  const rel = (e: FileEntry) => (root && e.path.startsWith(prefix()) ? e.path.slice(prefix().length) : e.path)
  const relDir = (e: FileEntry) => rel(e).slice(0, rel(e).length - e.name.length)

  function rowMenu(ev: MouseEvent, e: FileEntry) {
    ev.preventDefault()
    selected = e.path
    menu = {
      x: ev.clientX,
      y: ev.clientY,
      items: [
        { label: tr('search.reveal'), icon: 'folder', action: () => dispatch('reveal', e) },
        { label: tr('search.copyPath'), icon: 'copy', action: () => clipboardSet(e.path) },
      ],
    }
  }

  function onInputKey(ev: KeyboardEvent) {
    if (ev.key === 'Enter') {
      ev.preventDefault()
      start()
    } else if (ev.key === 'Escape') {
      ev.preventDefault()
      close()
    } else if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === 'f') {
      ev.preventDefault()
      focus()
    }
  }

  const fmtCount = (n: number) => n.toLocaleString(tr('locale.tag'))
</script>

<section>
  <div class="head">
    <Icon name="search" size={14} />
    <span class="muted">{$t('search.in')}</span>
    <span class="mono where" title={cwd}>{cwd}</span>
    <span class="spacer" />
    <button class="icon" title={$t('term.closeSearch')} on:click={close} aria-label={$t('common.close')}><Icon name="x" size={14} /></button>
  </div>

  <div class="form">
    <input
      class="mono"
      bind:this={nameEl}
      bind:value={name}
      placeholder={$t('search.namePh')}
      spellcheck="false"
      title={$t('search.nameTip')}
      on:keydown={onInputKey}
    />
    <input
      class="mono"
      bind:value={contains}
      disabled={!caps.content}
      placeholder={$t('search.containsPh')}
      spellcheck="false"
      title={caps.content ? $t('search.containsTip') : caps.note}
      on:keydown={onInputKey}
    />
    {#if running}
      <button on:click={stop}>{$t('search.stop')}</button>
    {:else}
      <button class="primary" on:click={start}>{$t('search.start')}</button>
    {/if}
  </div>
  {#if caps.note}<div class="note">{caps.note}</div>{/if}

  {#if results.length || running || done || error}
    <div class="results">
      {#each results as e (e.path)}
        <button
          type="button"
          class="row"
          class:sel={selected === e.path}
          title={e.path}
          on:click={() => (selected = e.path)}
          on:dblclick={() => dispatch('open', e)}
          on:contextmenu={(ev) => rowMenu(ev, e)}
        >
          <Icon name={e.isDir ? 'folder' : 'file'} size={13} />
          <span class="p"><span class="dir">{relDir(e)}</span>{e.name}</span>
          <span class="size">{e.isDir || e.size < 0 ? '' : fmtSize(e.size)}</span>
        </button>
      {/each}
      {#if !results.length && !running && !error}
        <div class="empty">{$t('search.nothing')}</div>
      {/if}
    </div>
    <div class="status">
      {#if running}
        {$t('search.searching', { count: fmtCount(results.length), word: $t(results.length === 1 ? 'search.result.one' : 'search.result.other') })}
      {:else if error}
        <span class="err">{error}</span>
      {:else if done?.truncated}
        {$t('search.truncated', { count: fmtCount(done.count) })}
      {:else if done}
        {$t('search.countWord', { count: fmtCount(done.count), word: $t(done.count === 1 ? 'search.result.one' : 'search.result.other') })}
      {/if}
      {#if root && root !== cwd}<span class="muted">{$t('search.inRoot', { root })}</span>{/if}
      {#if done?.contentUnavailable}
        <span class="warn">{$t('search.noContent', { reason: caps.note || $t('search.serverCant') })}</span>
      {/if}
    </div>
  {/if}
</section>

{#if menu}
  <ContextMenu {...menu} on:close={() => (menu = null)} />
{/if}

<style>
  section {
    flex: none;
    display: flex;
    flex-direction: column;
    border-bottom: 1px solid var(--border);
    background: var(--bg-2);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 4px 8px 0;
    min-width: 0;
  }
  .where {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .spacer {
    flex: 1;
  }
  .form {
    display: flex;
    gap: 6px;
    padding: 6px 8px;
  }
  .form input {
    flex: 1;
    min-width: 0;
  }
  .note {
    padding: 0 8px 6px;
    font-size: 12px;
    color: var(--text-2);
  }
  .results {
    height: min(240px, 35vh);
    overflow: auto;
    border-top: 1px solid var(--border);
    background: var(--bg);
  }
  .row {
    display: grid;
    grid-template-columns: 16px minmax(0, 1fr) auto;
    align-items: center;
    gap: 6px;
    width: 100%;
    height: 24px;
    padding: 0 10px;
    border: 0;
    background: transparent;
    text-align: left;
    user-select: none;
  }
  .row:hover {
    background: var(--bg-3);
  }
  .row.sel {
    background: var(--bg-4);
  }
  .p {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .dir,
  .size {
    color: var(--text-3);
  }
  .size {
    font-size: 12px;
  }
  .empty {
    padding: 12px;
    color: var(--text-3);
  }
  .status {
    padding: 3px 10px;
    font-size: 12px;
    color: var(--text-2);
    border-top: 1px solid var(--border);
  }
  .err {
    color: var(--danger);
  }
  .warn {
    color: var(--warning);
  }
</style>
