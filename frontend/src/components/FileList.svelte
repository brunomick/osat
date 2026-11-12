<script lang="ts">
  // Lista de archivos de un explorador (SFTP o "Este equipo"): columnas
  // ordenables, selección (Ctrl/Shift), zoom y arrastre hacia otras carpetas
  // y exploradores (ver lib/filedrag).
  import { createEventDispatcher, onDestroy } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { t } from '../lib/i18n'
  import { fmtDate, fmtSize, type FileEntry } from '../lib/api'
  import { beginFileDrag, fileDrag, type FsSource } from '../lib/filedrag'

  export let entries: FileEntry[]
  export let source: FsSource
  /** Carpeta que se muestra (soltar en el fondo de la lista = soltar acá). */
  export let cwd: string
  export let filter = ''
  export let loading = false
  export let selected = new Set<string>()
  /** Lo que se muestra, en orden (para Ctrl+A, Enter, etc.). */
  export let visible: FileEntry[] = []
  export let zoom = 1
  /** Título de la columna de modo; vacío = "Permisos". */
  export let modeLabel = ''
  /** El modo se muestra en monoespaciada (permisos Unix). */
  export let mono = true
  /** Vacío = "Carpeta vacía.". */
  export let emptyText = ''
  /** Texto al arrastrar archivos desde el Explorador; vacío = no los acepta. */
  export let osDropHint = ''
  /** Sacar el cursor de la ventana mientras se arrastra (arrastre nativo al sistema). */
  export let dragOut: (paths: string[]) => void

  const dispatch = createEventDispatcher<{
    open: FileEntry
    menu: { ev: MouseEvent; entry: FileEntry | null }
    /** El mouse pasó sobre una carpeta (para precargarla). */
    hover: FileEntry
  }>()

  let sortKey: 'name' | 'size' | 'modTime' = 'name'
  let sortAsc = true
  let lastClicked: string | null = null

  $: visible = entries
    .filter((e) => !filter || e.name.toLowerCase().includes(filter.toLowerCase()))
    .sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
      let r = 0
      if (sortKey === 'name') r = a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' })
      else r = (a[sortKey] as number) - (b[sortKey] as number)
      return sortAsc ? r : -r
    })

  $: dropDir = $fileDrag?.target?.tabId === source.tabId ? $fileDrag.target.dir : null

  function sortBy(k: typeof sortKey) {
    if (sortKey === k) sortAsc = !sortAsc
    else {
      sortKey = k
      sortAsc = true
    }
  }

  function onWheel(e: WheelEvent) {
    if (!(e.ctrlKey || e.metaKey) || !e.deltaY) return
    e.preventDefault()
    zoom = Math.round(Math.min(2, Math.max(0.6, zoom + (e.deltaY < 0 ? 0.1 : -0.1))) * 10) / 10
  }

  // Enter abre; Espacio selecciona (con Ctrl o Shift, como el clic).
  function rowKey(ev: KeyboardEvent, e: FileEntry) {
    if (ev.target !== ev.currentTarget) return
    if (ev.key === 'Enter') dispatch('open', e)
    else if (ev.key === ' ') clickRow(ev as unknown as MouseEvent, e)
    else return
    ev.preventDefault()
  }

  function clickRow(ev: MouseEvent, e: FileEntry) {
    if (suppressClick) return
    const s = new Set(ev.ctrlKey || ev.metaKey ? selected : [])
    if (ev.shiftKey && lastClicked) {
      const a = visible.findIndex((x) => x.path === lastClicked)
      const b = visible.findIndex((x) => x.path === e.path)
      const [from, to] = a < b ? [a, b] : [b, a]
      visible.slice(from, to + 1).forEach((x) => s.add(x.path))
    } else if ((ev.ctrlKey || ev.metaKey) && s.has(e.path)) {
      s.delete(e.path)
    } else {
      s.add(e.path)
      lastClicked = e.path
    }
    selected = s
  }

  // ---------- arrastrar ----------
  let pressed: { e: FileEntry; x: number; y: number } | null = null
  let suppressClick = false

  function rowDown(ev: MouseEvent, e: FileEntry) {
    if (ev.button !== 0 || ev.shiftKey || ev.ctrlKey || ev.metaKey) return
    if (blankSpace(ev.target)) return // el hueco al lado del nombre arranca el rectángulo de selección
    pressed = { e, x: ev.clientX, y: ev.clientY }
    window.addEventListener('mousemove', pressMove)
    window.addEventListener('mouseup', release)
  }

  function pressMove(ev: MouseEvent) {
    if (!pressed || Math.hypot(ev.clientX - pressed.x, ev.clientY - pressed.y) < 6) return
    if (!selected.has(pressed.e.path)) selected = new Set([pressed.e.path])
    release()
    beginFileDrag(source, [...selected], dragOut, () => {
      suppressClick = true // el click que sigue al mouseup no debe cambiar la selección
      setTimeout(() => (suppressClick = false))
    })
  }

  function release() {
    pressed = null
    window.removeEventListener('mousemove', pressMove)
    window.removeEventListener('mouseup', release)
  }

  // ---------- seleccionar con un rectángulo ----------
  // Apretar en un lugar vacío (debajo de la lista, o el hueco a la derecha del nombre de un archivo que no
  // está seleccionado) y arrastrar dibuja un rectángulo que selecciona lo que toca, como en el escritorio.
  // Con Ctrl o Shift se suma a lo que ya estaba seleccionado. Cerca del borde, la lista se desplaza sola.
  // Todo se calcula en coordenadas de la ventana (getBoundingClientRect + clientX/Y): el zoom de la lista
  // (CSS zoom) las deja consistentes, y el rectángulo se dibuja fuera de ella para que el zoom no lo escale.
  let listEl: HTMLDivElement
  let box: { x: number; y: number; w: number; h: number } | null = null
  let mq: { ax: number; ay: number; scroll0: number; cx: number; cy: number; base: Set<string>; moved: boolean } | null = null
  let edgeTimer: ReturnType<typeof setInterval> | undefined

  function blankSpace(t: EventTarget | null) {
    const el = t as HTMLElement | null
    if (!el || el.closest('.head')) return false
    if (el === listEl || el.classList.contains('empty')) return true
    // El hueco de la fila: la celda del nombre (donde no hay texto) o el relleno de la fila misma.
    const row = el.closest('.row')
    return (el.classList.contains('name') || el === row) && !!row && !row.classList.contains('sel')
  }

  /** Píxeles de la ventana por píxel de la lista (el zoom). */
  const scale = () => (listEl.offsetHeight ? listEl.getBoundingClientRect().height / listEl.offsetHeight : 1)

  function mqDown(ev: MouseEvent) {
    if (ev.button !== 0 || !blankSpace(ev.target)) return
    ev.preventDefault() // sin selección de texto mientras se arrastra
    mq = {
      ax: ev.clientX,
      ay: ev.clientY,
      scroll0: listEl.scrollTop,
      cx: ev.clientX,
      cy: ev.clientY,
      base: ev.ctrlKey || ev.metaKey || ev.shiftKey ? new Set(selected) : new Set(),
      moved: false,
    }
    window.addEventListener('mousemove', mqMove)
    window.addEventListener('mouseup', mqUp)
  }

  function mqMove(ev: MouseEvent) {
    if (!mq) return
    mq.cx = ev.clientX
    mq.cy = ev.clientY
    if (!mq.moved) {
      if (Math.hypot(mq.cx - mq.ax, mq.cy - mq.ay) < 4) return
      mq.moved = true
      edgeTimer = setInterval(edgeScroll, 30)
    }
    mqUpdate()
  }

  function edgeScroll() {
    if (!mq) return
    const r = listEl.getBoundingClientRect()
    const near = 36 // la cabecera fija ocupa el borde de arriba
    if (mq.cy < r.top + near) listEl.scrollTop -= 14
    else if (mq.cy > r.bottom - 16) listEl.scrollTop += 14
    else return
    mqUpdate()
  }

  function mqUpdate() {
    if (!mq) return
    const ay = mq.ay - (listEl.scrollTop - mq.scroll0) * scale() // el punto donde se empezó acompaña al contenido
    const x1 = Math.min(mq.ax, mq.cx)
    const x2 = Math.max(mq.ax, mq.cx)
    const y1 = Math.min(ay, mq.cy)
    const y2 = Math.max(ay, mq.cy)
    const lr = listEl.getBoundingClientRect()
    const cx1 = Math.max(x1, lr.left)
    const cy1 = Math.max(y1, lr.top)
    box = { x: cx1, y: cy1, w: Math.max(0, Math.min(x2, lr.right) - cx1), h: Math.max(0, Math.min(y2, lr.bottom) - cy1) }
    const s = new Set(mq.base)
    listEl.querySelectorAll<HTMLElement>('.row:not(.head)').forEach((row, i) => {
      const b = row.getBoundingClientRect()
      if (visible[i] && b.bottom > y1 && b.top < y2 && b.right > x1 && b.left < x2) s.add(visible[i].path)
    })
    selected = s
  }

  function mqUp() {
    window.removeEventListener('mousemove', mqMove)
    window.removeEventListener('mouseup', mqUp)
    clearInterval(edgeTimer)
    if (mq?.moved) {
      suppressClick = true // el click que sigue al soltar no debe cambiar la selección
      setTimeout(() => (suppressClick = false))
    }
    mq = null
    box = null
  }

  onDestroy(mqUp)

  function menu(ev: MouseEvent, entry: FileEntry | null) {
    ev.preventDefault()
    ev.stopPropagation()
    if (entry && !selected.has(entry.path)) selected = new Set([entry.path])
    if (!entry) selected = new Set()
    dispatch('menu', { ev, entry })
  }
</script>

<div
  class="list"
  role="listbox"
  aria-label={$t('files.list')}
  aria-multiselectable="true"
  tabindex="-1"
  bind:this={listEl}
  data-fs-tab={source.tabId}
  class:droptarget={dropDir === cwd}
  style="{osDropHint ? '--wails-drop-target: drop;' : ''} zoom: {zoom}"
  on:wheel={onWheel}
  on:mousedown={mqDown}
  on:contextmenu={(ev) => menu(ev, null)}
  on:click|self={() => !suppressClick && (selected = new Set())}
  on:keydown|self={(ev) => ev.key === 'Escape' && (selected = new Set())}
>
  <div class="row head" role="presentation">
    <button class="c name" on:click={() => sortBy('name')}>{$t('files.name')} {sortKey === 'name' ? (sortAsc ? '▲' : '▼') : ''}</button>
    <button class="c size" on:click={() => sortBy('size')}>{$t('files.size')} {sortKey === 'size' ? (sortAsc ? '▲' : '▼') : ''}</button>
    <button class="c date" on:click={() => sortBy('modTime')}>{$t('files.modified')} {sortKey === 'modTime' ? (sortAsc ? '▲' : '▼') : ''}</button>
    <span class="c perm">{modeLabel || $t('files.permissions')}</span>
  </div>
  {#each visible as e (e.path)}
    <div
      class="row"
      role="option"
      aria-selected={selected.has(e.path)}
      tabindex="0"
      class:sel={selected.has(e.path)}
      class:droptarget={dropDir === e.path}
      data-drop={e.isDir ? e.path : undefined}
      on:mousedown={(ev) => rowDown(ev, e)}
      on:click={(ev) => clickRow(ev, e)}
      on:keydown={(ev) => rowKey(ev, e)}
      on:dblclick={() => dispatch('open', e)}
      on:mouseenter={() => e.isDir && dispatch('hover', e)}
      on:contextmenu={(ev) => menu(ev, e)}
    >
      <span class="c name">
        <span class="ficon" class:dir={e.isDir}><Icon name={e.isDir ? 'folder' : 'file'} size={15} /></span>
        <span class="fname">{e.name}</span>
        {#if e.isLink}<span class="link" title={$t('files.symlink')}><Icon name="link" size={11} /></span>{/if}
      </span>
      <span class="c size">{e.isDir && !/^[A-Za-z]:[\\/]?$/.test(e.path) ? '' : fmtSize(e.size)}</span>
      <span class="c date">{fmtDate(e.modTime)}</span>
      <span class="c perm" class:mono={mono}>{e.mode}</span>
    </div>
  {:else}
    {#if !loading}
      <div class="empty">{filter ? $t('files.noMatches') : emptyText || $t('files.emptyFolder')}</div>
    {/if}
  {/each}
  {#if osDropHint}<div class="drop-hint">{osDropHint}</div>{/if}
</div>
{#if box}
  <div class="marquee" style="left:{box.x}px;top:{box.y}px;width:{box.w}px;height:{box.h}px" />
{/if}

<style>
  .list {
    container-type: inline-size;
    flex: 1;
    overflow: auto;
    position: relative;
  }
  .row {
    display: grid;
    grid-template-columns: minmax(200px, 1fr) 90px 140px 110px;
    align-items: center;
    padding: 0 12px;
    height: 28px;
    border-bottom: 1px solid transparent;
  }
  /* Panel angosto: primero se van los permisos/tipo, después la fecha. */
  @container (max-width: 560px) {
    .row {
      grid-template-columns: minmax(120px, 1fr) 80px 120px;
    }
    .c.perm {
      display: none;
    }
  }
  @container (max-width: 400px) {
    .row {
      grid-template-columns: minmax(100px, 1fr) 72px;
    }
    .c.date {
      display: none;
    }
  }
  .row:not(.head):hover {
    background: var(--bg-3);
  }
  .droptarget {
    outline: 1px solid var(--text-2);
    outline-offset: -1px;
  }
  .row.sel {
    background: var(--bg-4) !important;
  }
  .row.head {
    position: sticky;
    top: 0;
    background: var(--bg);
    border-bottom: 1px solid var(--border);
    z-index: 1;
    color: var(--text-3);
    font-size: 11px;
  }
  .head button {
    background: transparent;
    border: 0;
    padding: 0;
    text-align: left;
    color: inherit;
  }
  .head button:hover {
    color: var(--text);
    background: transparent !important;
  }
  .c {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .c.size,
  .c.date,
  .c.perm {
    color: var(--text-2);
    font-size: 12px;
  }
  .c.size {
    text-align: right;
    padding-right: 16px;
  }
  .head .c.size {
    text-align: right;
    padding-right: 16px;
  }
  .name {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .ficon {
    display: inline-flex;
    color: var(--text-3);
  }
  .ficon.dir {
    color: var(--text-2);
  }
  .fname {
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .link {
    color: var(--text-3);
    display: inline-flex;
  }
  .empty {
    padding: 40px;
    text-align: center;
    color: var(--text-3);
  }
  /* El rectángulo de selección: fuera de .list (y con position fixed) para que el zoom de la lista no lo escale. */
  .marquee {
    position: fixed;
    z-index: 50;
    pointer-events: none;
    border: 1px solid var(--text-2);
    background: var(--drop-tint);
  }
  .drop-hint {
    display: none;
  }
  :global(.wails-drop-target-active).list {
    outline: 2px dashed var(--accent);
    outline-offset: -6px;
  }
  :global(.wails-drop-target-active) .drop-hint {
    display: block;
    position: sticky;
    bottom: 12px;
    margin: 0 auto;
    width: fit-content;
    background: var(--accent);
    color: var(--on-accent);
    padding: 6px 12px;
  }
</style>
