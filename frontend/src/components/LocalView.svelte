<script lang="ts">
  // "Este equipo": explorador de los archivos de esta PC, para tenerlo al lado
  // de un SFTP (estilo WinSCP) y arrastrar entre los dos.
  import { onMount, onDestroy, tick } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { t, tr } from '../lib/i18n'
  import ContextMenu, { type MenuItem } from './ContextMenu.svelte'
  import FileList from './FileList.svelte'
  import { api, errorText, fmtSize, parseError, type FileEntry, type LocalListing } from '../lib/api'
  import { choiceDialog, confirmDialog, promptDialog, renameDialog } from '../lib/dialog'
  import { allIn, describe, fileDrag, fsViews, parentOf, type FsSource } from '../lib/filedrag'
  import { fileManagerText, isMac, keys, platform } from '../lib/platform'
  import { moveErrorText, transfer } from '../lib/transfer'
  import { fileClip } from '../lib/fileclip'
  import { get } from 'svelte/store'
  import { defaultShell, downloadFinished, localShells, openEditor, openLocalTerminal, patchTab, toast, type Tab } from '../lib/stores'

  export let tab: Tab
  /** Visible y en el panel enfocado: solo entonces responde a los atajos. */
  export let focused = false

  const DRIVES = '::drives'

  let home = ''
  let listing: LocalListing | null = null
  let pathInput = ''
  let selected = new Set<string>()
  let visible: FileEntry[] = []
  let history: string[] = []
  let forward: string[] = [] // lo que se dejó con Atrás, para volver con Adelante
  let loading = false
  let filter = ''
  let zoom = 1
  let menu: { x: number; y: number; items: MenuItem[] } | null = null

  $: cwd = listing?.path ?? ''
  $: entries = listing?.entries ?? []
  $: atDrives = cwd === DRIVES
  $: selEntries = entries.filter((e) => selected.has(e.path))
  $: source = { tabId: tab.id, side: 'local' } as FsSource
  $: dropDir = $fileDrag?.target?.tabId === tab.id ? $fileDrag.target.dir : null

  async function navigate(dir: string, pushHistory = true) {
    loading = true
    try {
      const l = await api.localList(dir)
      if (pushHistory && cwd && cwd !== l.path) {
        history = [...history.slice(-49), cwd]
        forward = []
      }
      // Al salir hacia arriba (Subir, Atrás, la barra de ruta) queda seleccionada la carpeta de la que se venía.
      const prev = cwd
      const s = prev.includes('\\') ? '\\' : '/'
      const from = prev && prev !== l.path ? l.entries.find((e) => e.isDir && (prev === e.path || prev.startsWith(e.path.endsWith(s) ? e.path : e.path + s))) : undefined
      listing = l
      patchTab(tab.id, { cwd: l.path, status: 'ready' })
      pathInput = l.path === DRIVES ? '' : l.path
      selected = new Set(from ? [from.path] : [])
      if (from) tick().then(() => document.querySelector(`[data-fs-tab="${tab.id}"] [data-drop="${CSS.escape(from.path)}"]`)?.scrollIntoView({ block: 'nearest' }))
    } catch (e) {
      toast(errorText(e), 'error')
      pathInput = atDrives ? '' : cwd
    } finally {
      loading = false
    }
  }

  const refresh = () => navigate(cwd, false)
  const goUp = () => listing?.parent && navigate(listing.parent)
  const goBack = () => {
    const prev = history.pop()
    if (!prev) return
    history = history
    forward = [...forward, cwd]
    navigate(prev, false)
  }
  const goForward = () => {
    const next = forward.pop()
    if (!next) return
    forward = forward
    history = [...history.slice(-49), cwd]
    navigate(next, false)
  }

  // Botones laterales del mouse (atrás / adelante), como en el Explorador.
  function onMouse(e: MouseEvent) {
    if (!focused || menu) return
    if (e.button === 3) goBack()
    else if (e.button === 4) goForward()
    else return
    e.preventDefault()
  }
  const sep = () => (cwd.includes('\\') ? '\\' : '/')
  const join = (name: string) => (cwd.endsWith(sep()) ? cwd + name : cwd + sep() + name)

  // Doble clic / Enter: carpetas se navegan; archivos de texto se abren en el
  // editor de osat y el resto (binarios, > 2 MB) con la aplicación de Windows.
  async function open(e: FileEntry) {
    if (e.isDir) return navigate(e.path)
    let text: string
    try {
      text = await api.localReadText(e.path)
    } catch {
      return openExternal(e)
    }
    openEditor(tab, undefined, e.path, text)
  }

  async function openExternal(e: FileEntry) {
    try {
      await api.localOpen(e.path)
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  // ---------- Arrastrar ----------
  // Soltar acá elementos de otro "Este equipo" (o de esta misma lista sobre
  // una carpeta) los mueve; soltar elementos de un SFTP los descarga.
  const fsView = {
    cwd: () => cwd,
    verb(src: FsSource, paths: string[], dir: string) {
      if (dir === DRIVES) return null
      if (src.side === 'remote') return src.sessionId ? tr('local.download', { what: describe(paths), dir }) : null
      return allIn(paths, dir) ? null : tr('local.move', { what: describe(paths), dir })
    },
    drop(src: FsSource, paths: string[], dir: string) {
      if (src.side === 'remote') {
        const sid = src.sessionId!
        transfer((policy) => api.sftpDownload(sid, paths, dir, policy))
      } else moveTo(paths, dir, src.tabId)
    },
    refresh: () => refresh(),
  }

  async function moveTo(paths: string[], dir: string, fromTab: string) {
    try {
      await api.localMove(paths, dir)
      toast(`${describe(paths)} → ${dir}`, 'success', 2500)
    } catch (e) {
      toast(moveErrorText(e), 'error', 6000)
    }
    refresh()
    if (fromTab !== tab.id) fsViews.get(fromTab)?.refresh()
  }

  async function dragOut(paths: string[]) {
    try {
      if (await api.localDragOut(paths)) toast(tr('local.copiedShort', { what: describe(paths) }), 'success', 2500)
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  // ---------- acciones ----------

  async function mkdir() {
    if (atDrives) return
    const name = await promptDialog({ title: tr('local.newFolder'), message: tr('local.insideOf', { dir: cwd }), okText: tr('common.create') })
    if (!name) return
    try {
      await api.localMkdir(join(name))
      refresh()
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function rename(e: FileEntry) {
    const name = await renameDialog(e)
    if (!name) return
    try {
      await api.localRename(e.path, join(name))
      refresh()
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  async function remove(list: FileEntry[]) {
    if (!list.length || atDrives) return
    // En macOS todavía no hay papelera: se elimina definitivamente, avisando.
    if (isMac()) {
      const ok = await confirmDialog({
        title: tr('local.deleteForeverTitle'),
        message:
          list.length === 1
            ? tr('local.deleteForever.one', { name: list[0].name })
            : tr('local.deleteForever.other', { count: list.length }),
        okText: tr('common.delete'),
        danger: true,
      })
      if (!ok) return
      try {
        await api.localDeleteForever(list.map((e) => e.path))
      } catch (err) {
        toast(errorText(err), 'error')
      }
      refresh()
      return
    }
    // Windows y Linux: a la papelera. "Papelera de reciclaje" solo en Windows.
    const trashKey = platform === 'windows' ? 'local.trashWin' : 'local.trash'
    const ok = await confirmDialog({
      title: tr('local.trashTitle'),
      message:
        list.length === 1
          ? tr(trashKey + '.one', { name: list[0].name })
          : tr(trashKey + '.other', { count: list.length }),
      okText: tr('local.trashTitle'),
      danger: true,
    })
    if (!ok) return
    try {
      await api.localRemove(list.map((e) => e.path))
    } catch (err) {
      const k = parseError(err)
      if (k?.code === 'NO_TRASH') {
        // Los que no entraron a la papelera (otro disco): ofrecer eliminarlos definitivamente.
        const idx = (k.data?.failed ?? '').split(',').filter((s) => s !== '').map(Number)
        const failed = idx.map((i) => list[i]).filter(Boolean)
        if (failed.length) {
          const choice = await choiceDialog({
            title: tr('local.noTrashTitle'),
            message: tr('local.noTrashMsg'),
            list: failed.map((e) => e.name),
            choices: [{ value: 'delete', label: tr('local.deleteForeverTitle'), kind: 'danger' }],
          })
          if (choice === 'delete') {
            try {
              await api.localDeleteForever(failed.map((e) => e.path))
            } catch (e2) {
              toast(errorText(e2), 'error')
            }
          }
        }
      } else {
        toast(errorText(err), 'error')
      }
    }
    refresh()
  }

  // ---------- Copiar / Pegar ----------
  // "Copiar" guarda las rutas en el portapapeles de archivos de OSAT; "Pegar" las
  // copia en la carpeta de esta lista (o dentro de una carpeta elegida). En la misma
  // carpeta crea "x - copia.ext" para renombrarla después; nunca pisa nada. Si lo
  // copiado viene de un SFTP, pegar lo descarga.
  let copying = false

  function copySel(list: FileEntry[]) {
    if (!list.length || atDrives) return
    const paths = list.map((e) => e.path)
    fileClip.set({ source, paths })
    toast(tr('local.copiedHint', { what: describe(paths), keys: keys('Ctrl+V') }), 'info', 2500)
  }

  async function paste(dir = cwd) {
    const clip = get(fileClip)
    if (!clip || atDrives || dir === DRIVES || copying) return
    if (clip.source.side === 'remote') {
      if (clip.source.sessionId) fsView.drop(clip.source, clip.paths, dir)
      else toast(tr('local.sftpGone'), 'error')
      return
    }
    copying = true
    try {
      const made = await api.localCopy(clip.paths, dir)
      toast(tr('local.copiedShort', { what: describe(made) }), 'success', 2500)
      if (dir === cwd) {
        await refresh()
        selected = new Set(made) // queda seleccionada la copia: F2 para renombrarla
      } else refresh()
      if (clip.source.tabId !== tab.id) fsViews.get(clip.source.tabId)?.refresh()
    } catch (e) {
      toast(errorText(e), 'error', 6000)
      refresh()
    } finally {
      copying = false
    }
  }

  const reveal = (p: string) => api.localReveal(p).catch((e) => toast(errorText(e), 'error'))
  const openInExplorer = () => !atDrives && api.localOpen(cwd).catch((e) => toast(errorText(e), 'error'))

  // Terminal de este equipo en esa carpeta: con la terminal por defecto de los ajustes, o eligiendo una.
  const terminalHere = (dir: string) => openLocalTerminal('', dir)

  async function terminalHereWith(dir: string) {
    const shells = get(localShells)
    if (!shells.length) return toast(tr('local.noShell'), 'error')
    const last = get(defaultShell)
    const id = await choiceDialog({
      title: tr('local.terminalHere'),
      message: dir,
      choices: shells.map((s) => ({ value: s.id, label: s.name, kind: s.id === last ? ('primary' as const) : undefined })),
    })
    if (id) openLocalTerminal(id, dir)
  }

  const terminalItems = (dir: string): MenuItem[] => [
    { label: tr('local.terminalHere'), icon: 'terminal', action: () => terminalHere(dir) },
    { label: tr('local.terminalHereWith'), icon: 'terminal', action: () => terminalHereWith(dir) },
  ]

  function rowMenu(ev: MouseEvent, e: FileEntry) {
    const many = selected.size > 1
    const sel = entries.filter((x) => selected.has(x.path))
    menu = {
      x: ev.clientX,
      y: ev.clientY,
      items: [
        ...(!many ? [{ label: tr('common.open'), icon: e.isDir ? 'folder' : 'file', action: () => open(e) }] : []),
        ...(!many && !e.isDir
          ? [{ label: tr('local.openDefault'), icon: 'external', action: () => openExternal(e) }]
          : []),
        ...(!many && !atDrives ? [{ label: fileManagerText(tr, 'showIn'), icon: 'external', action: () => reveal(e.path) }] : []),
        ...(e.isDir && !many && !atDrives ? terminalItems(e.path) : []),
        { separator: true, label: '' },
        { label: tr('term.copy'), icon: 'copy', hint: keys('Ctrl+C'), disabled: atDrives, action: () => copySel(sel) },
        ...(e.isDir && !many && !atDrives
          ? [{ label: tr('local.pasteInto'), icon: 'paste', disabled: !get(fileClip), action: () => paste(e.path) }]
          : []),
        { label: tr('local.rename'), hint: 'F2', disabled: many || atDrives, action: () => rename(e) },
        { label: tr('local.copyPath'), icon: 'copy', action: () => window.runtime?.ClipboardSetText?.(sel.map((x) => x.path).join('\n')) },
        ...(atDrives
          ? []
          : [
              { separator: true, label: '' },
              { label: isMac() ? tr('local.deleteMenu') : tr('local.toTrash'), icon: 'trash', danger: true, action: () => remove(sel) },
            ]),
      ],
    }
  }

  function bgMenu(ev: MouseEvent) {
    menu = {
      x: ev.clientX,
      y: ev.clientY,
      items: [
        { label: tr('term.paste'), icon: 'paste', hint: keys('Ctrl+V'), disabled: atDrives || !get(fileClip), action: () => paste() },
        { separator: true, label: '' },
        { label: tr('local.newFolder'), icon: 'folderPlus', disabled: atDrives, action: mkdir },
        { label: fileManagerText(tr, 'openIn'), icon: 'external', disabled: atDrives, action: openInExplorer },
        ...(atDrives ? [] : terminalItems(cwd)),
        { separator: true, label: '' },
        { label: tr('local.refresh'), icon: 'refresh', action: refresh },
      ],
    }
  }

  function onKey(e: KeyboardEvent) {
    if (!focused || menu) return
    const tag = (e.target as HTMLElement)?.tagName
    if (tag === 'INPUT' || tag === 'TEXTAREA') return
    const mod = e.ctrlKey || e.metaKey
    if (e.altKey && e.key === 'ArrowLeft') goBack()
    else if (e.altKey && e.key === 'ArrowRight') goForward()
    else if (isMac() && e.metaKey && e.key === 'Backspace') remove(selEntries) // como Supr, al estilo del Finder
    else if (e.key === 'Backspace') goUp()
    else if (e.key === 'F5') refresh()
    else if (e.key === 'Delete') remove(selEntries)
    else if (e.key === 'F2' && selEntries.length === 1 && !atDrives) rename(selEntries[0])
    else if (e.key === 'Enter' && selEntries.length === 1) open(selEntries[0])
    else if (mod && e.key.toLowerCase() === 'a') selected = new Set(visible.map((x) => x.path))
    else if (mod && e.key.toLowerCase() === 'c' && selEntries.length) copySel(selEntries)
    else if (mod && e.key.toLowerCase() === 'v') paste()
    else if (mod && (e.key === '=' || e.key === '+')) zoom = Math.min(2, Math.round((zoom + 0.1) * 10) / 10)
    else if (mod && e.key === '-') zoom = Math.max(0.6, Math.round((zoom - 0.1) * 10) / 10)
    else if (mod && e.key === '0') zoom = 1
    else return
    e.preventDefault()
  }

  // Refresca cuando termina una descarga a esta carpeta.
  let refreshTimer: ReturnType<typeof setTimeout>
  const unsubDownload = downloadFinished.subscribe((d) => {
    if (d && cwd && parentOf(d.target) === cwd) {
      clearTimeout(refreshTimer)
      refreshTimer = setTimeout(refresh, 300)
    }
  })

  onMount(async () => {
    fsViews.set(tab.id, fsView)
    window.addEventListener('keydown', onKey)
    window.addEventListener('mouseup', onMouse)
    home = await api.localHome()
    await navigate(tab.initialDir || home, false)
    if (tab.initialDir && !listing) navigate(home, false) // esa carpeta ya no existe (p. ej. al restaurar un espacio de trabajo)
  })

  onDestroy(() => {
    fsViews.delete(tab.id)
    window.removeEventListener('keydown', onKey)
    window.removeEventListener('mouseup', onMouse)
    unsubDownload()
    clearTimeout(refreshTimer)
  })
</script>

<div class="local">
  <div class="toolbar">
    <button class="icon" title={$t('local.backTip')} disabled={!history.length} on:click={goBack} aria-label={$t('local.back')}><Icon name="back" /></button>
    <button class="icon" title={$t('local.forwardTip')} disabled={!forward.length} on:click={goForward} aria-label={$t('local.forward')}><Icon name="forward" /></button>
    <button class="icon" title={$t('local.upTip')} disabled={!listing?.parent} on:click={goUp} aria-label={$t('local.up')}><Icon name="up" /></button>
    <button class="icon" title={$t('local.home')} on:click={() => navigate(home)} aria-label={$t('local.home')}><Icon name="home" /></button>
    <button class="icon" title={$t('local.refreshTip')} on:click={refresh} aria-label={$t('local.refresh')}><Icon name="refresh" /></button>

    <form class="path" on:submit|preventDefault={() => pathInput && navigate(pathInput)}>
      <div class="crumbs">
        {#each listing?.crumbs ?? [] as c, i}
          <button type="button" class="crumb" class:droptarget={dropDir === c.path} data-drop={c.path} on:click={() => navigate(c.path)}>{c.name}</button>
          {#if i < (listing?.crumbs.length ?? 0) - 1}<span class="slash">›</span>{/if}
        {/each}
      </div>
      <input class="mono" bind:value={pathInput} spellcheck="false" title={$t('local.pathTip')} />
    </form>

    <div class="filter">
      <Icon name="search" size={13} />
      <input placeholder={$t('local.filter')} bind:value={filter} />
    </div>

    <div class="sep" />
    <button class="icon" title={$t('local.newFolder')} disabled={atDrives} on:click={mkdir} aria-label={$t('local.newFolder')}><Icon name="folderPlus" /></button>
    <button class="icon" title={fileManagerText($t, 'openIn')} aria-label={fileManagerText($t, 'openIn')} disabled={atDrives} on:click={openInExplorer}><Icon name="external" /></button>
  </div>

  <FileList
    {entries}
    {source}
    {cwd}
    {filter}
    {loading}
    bind:selected
    bind:visible
    bind:zoom
    modeLabel={$t('local.typeCol')}
    mono={false}
    {dragOut}
    on:open={(e) => open(e.detail)}
    on:menu={(e) => (e.detail.entry ? rowMenu(e.detail.ev, e.detail.entry) : bgMenu(e.detail.ev))}
  />

  <div class="status">
    {#if loading}{$t('snippets.loading')}{:else if copying}{$t('local.copying')}{:else}
      {$t('local.items', { count: visible.length })}{selected.size ? $t('local.selected', { count: selected.size, size: fmtSize(selEntries.reduce((a, e) => a + (e.isDir ? 0 : e.size), 0)) }) : ''}
    {/if}
    <span class="hint">{$t('local.dragHint')}</span>
  </div>
</div>

{#if menu}
  <ContextMenu {...menu} on:close={() => (menu = null)} />
{/if}

<style>
  .local {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  .toolbar {
    container-type: inline-size;
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 6px 8px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-2);
  }
  .path {
    flex: 1;
    min-width: 80px;
    position: relative;
  }
  .path input {
    width: 100%;
    color: transparent;
  }
  .path input:focus {
    color: var(--text);
  }
  .crumbs {
    position: absolute;
    inset: 1px 8px;
    display: flex;
    align-items: center;
    overflow: hidden;
    pointer-events: none;
    white-space: nowrap;
  }
  .path:focus-within .crumbs {
    display: none;
  }
  .crumb {
    pointer-events: auto;
    background: transparent;
    border: 0;
    padding: 2px 4px;
    font-family: var(--mono);
    font-size: 12px;
  }
  .slash {
    color: var(--text-3);
  }
  .droptarget {
    outline: 1px solid var(--text-2);
    outline-offset: -1px;
  }
  .filter {
    display: flex;
    align-items: center;
    gap: 4px;
    border: 1px solid var(--border);
    padding: 0 6px;
    background: var(--bg);
    color: var(--text-3);
    flex: 0 1 140px;
    min-width: 70px;
  }
  .filter input {
    border: 0;
    padding: 6px 0;
    width: 100%;
    background: transparent;
  }
  .sep {
    width: 1px;
    height: 20px;
    background: var(--border);
    margin: 0 4px;
  }
  .status {
    display: flex;
    white-space: nowrap;
    align-items: center;
    gap: 8px;
    padding: 4px 12px;
    border-top: 1px solid var(--border);
    font-size: 11px;
    color: var(--text-3);
    background: var(--bg-2);
  }
  .hint {
    margin-left: auto;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
</style>
