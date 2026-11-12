<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { t, tr } from '../lib/i18n'
  import ContextMenu, { type MenuItem } from './ContextMenu.svelte'
  import TerminalView from './TerminalView.svelte'
  import EditorView from './EditorView.svelte'
  import FileList from './FileList.svelte'
  import SftpSearch from './SftpSearch.svelte'
  import SyncDialog from './SyncDialog.svelte'
  import { api, errorText, fmtSize, isServerGone, on, type FileEntry, type Listing, type SessionInfo } from '../lib/api'
  import { connectTab, CancelledError } from '../lib/connect'
  import { reconnect, type Reconnector } from '../lib/reconnect'
  import { confirmDialog, promptDialog, renameDialog } from '../lib/dialog'
  import { allIn, describe, fileDrag, fsViews, type FsSource } from '../lib/filedrag'
  import { isMac, keys } from '../lib/platform'
  import { moveErrorText, transfer } from '../lib/transfer'
  import { fileClip } from '../lib/fileclip'
  import { askRoot, isPermissionError } from '../lib/sudo'
  import { get } from 'svelte/store'
  import {
    activeTabId,
    patchTab,
    dropHandlers,
    uploadFinished,
    openTab,
    openEditor,
    openBrowser,
    openRepo,
    isServerPinned,
    toggleServerPin,
    handoffs,
    settings,
    tabs,
    toast,
    type Tab,
  } from '../lib/stores'

  export let tab: Tab
  export let active: boolean
  /** Visible y en el panel enfocado: solo entonces responde a los atajos. */
  export let focused = false

  let sessionId: string | null = null
  let home = '/'
  let cwd = ''
  let pathInput = ''
  let entries: FileEntry[] = []
  let selected = new Set<string>()
  let visible: FileEntry[] = []
  let history: string[] = []
  let forward: string[] = [] // lo que se dejó con Atrás, para volver con Adelante
  let loading = false
  let error = ''
  let filter = ''
  let menu: { x: number; y: number; items: MenuItem[] } | null = null
  let source: FsSource

  // Archivos ocultos de Unix (nombre con punto): ocultos hasta que se pida verlos.
  let showHidden = false
  $: shown = showHidden ? entries : entries.filter((e) => !e.name.startsWith('.'))
  function toggleHidden() {
    showHidden = !showHidden
    // Lo seleccionado que deja de verse no debería seguir afectado por Descargar/Eliminar.
    if (!showHidden) selected = new Set([...selected].filter((p) => !p.split('/').pop()!.startsWith('.')))
  }

  $: selEntries = entries.filter((e) => selected.has(e.path))
  $: source = { tabId: tab.id, side: 'remote', connId: tab.connId, sessionId: sessionId ?? undefined }
  $: dropDir = $fileDrag?.target?.tabId === tab.id ? $fileDrag.target.dir : null
  $: crumbs = (() => {
    const parts = cwd.split('/').filter(Boolean)
    return [{ name: '/', path: '/' }, ...parts.map((p, i) => ({ name: p, path: '/' + parts.slice(0, i + 1).join('/') }))]
  })()

  async function connect() {
    error = ''
    loading = true
    patchTab(tab.id, { status: 'connecting', sessionId: undefined })
    try {
      const info = await connectTab(tab)
      await started(info)
    } catch (e) {
      loading = false
      patchTab(tab.id, { status: 'error' })
      error = e instanceof CancelledError ? tr('term.cancelled') : errorText(e)
    }
  }

  // Sesión nueva (al conectar o reconectar): vuelve a la carpeta en la que estaba, o al inicio.
  // La primera vez arranca en tab.initialDir si viene (una pestaña restaurada de un espacio de trabajo).
  async function started(info: SessionInfo) {
    cache.clear() // sesión nueva: lo de la anterior puede haber cambiado
    sessionId = info.id
    home = info.home || '/'
    unlost?.()
    unlost = on(`session:lost:${info.id}`, lostConnection)
    patchTab(tab.id, { status: 'ready', sessionId: info.id })
    error = ''
    const first = !cwd && !!tab.initialDir
    await navigate(cwd || tab.initialDir || home, false)
    if (first && !cwd && sessionId) await navigate(home, false) // esa carpeta ya no existe (o no hay permiso): al inicio
  }

  // ---------- Reconexión automática (ver lib/reconnect.ts) ----------

  let retry: Reconnector | null = null
  let retryText = ''
  let unlost: (() => void) | null = null

  function lostConnection() {
    if (retry) return
    unlost?.()
    unlost = null
    if (sessionId) api.closeSession(sessionId).catch(() => {})
    sessionId = null
    loading = false
    error = tr('sftp.lost')
    patchTab(tab.id, { status: 'connecting', sessionId: undefined })
    retry = reconnect(
      () => connectTab({ ...tab, fromSession: undefined }, undefined, undefined, { interactive: false }),
      (s) => {
        if (s.phase === 'waiting') retryText = tr('sftp.retryWaiting', { attempt: s.attempt, seconds: s.seconds })
        else if (s.phase === 'trying') retryText = tr('sftp.retryTrying', { attempt: s.attempt })
        else {
          if (!retry) return // la vista se cerró
          retry = null
          retryText = ''
          if (s.phase === 'connected') {
            started(s.result).catch((e) => (error = errorText(e)))
          } else if (s.phase === 'needsUser') {
            error = errorText(s.error)
            patchTab(tab.id, { status: 'error' })
          } else {
            patchTab(tab.id, { status: 'closed' })
          }
        }
      },
      { discard: (info) => api.closeSession(info.id).catch(() => {}) },
    )
  }

  // ---------- Listados: caché y precarga ----------
  // Con un servidor lejano cada listado tarda varias idas y vueltas. Las
  // carpetas ya vistas (o precargadas al pasar el mouse) se muestran al
  // instante desde la caché y se actualizan en segundo plano.
  const cache = new Map<string, Listing>()
  const inflight = new Map<string, Promise<Listing>>()
  const CACHE_MAX = 300
  let navSeq = 0 // descarta respuestas de navegaciones viejas
  let hoverTimer: ReturnType<typeof setTimeout>

  // Misma normalización que el backend (path.Clean) para rutas absolutas simples.
  const key = (dir: string) => (dir.startsWith('/') ? dir.replace(/\/+/g, '/').replace(/(.)\/$/, '$1') : dir)

  function list(dir: string): Promise<Listing> {
    const k = key(dir)
    const pending = inflight.get(k)
    if (pending) return pending
    const p = api
      .sftpList(sessionId!, dir)
      .then((l) => {
        l.entries ??= []
        cache.delete(l.path) // reinsertar: la más reciente queda al final
        cache.set(l.path, l)
        if (cache.size > CACHE_MAX) cache.delete(cache.keys().next().value!)
        return l
      })
      .finally(() => inflight.delete(k))
    inflight.set(k, p)
    return p
  }

  function prefetch(e: FileEntry) {
    clearTimeout(hoverTimer)
    if (!sessionId || cache.has(e.path)) return
    hoverTimer = setTimeout(() => {
      if (sessionId && !cache.has(e.path) && inflight.size < 3) list(e.path).catch(() => {})
    }, 150)
  }

  function show(l: Listing, pushHistory: boolean) {
    if (pushHistory && cwd && cwd !== l.path) {
      history = [...history.slice(-49), cwd]
      forward = []
    }
    if (cwd === l.path) {
      // Misma carpeta (actualización): conservar lo seleccionado que sigue existiendo.
      const paths = new Set(l.entries.map((e) => e.path))
      selected = new Set([...selected].filter((p) => paths.has(p)))
    } else {
      // Al salir hacia arriba (Subir, Atrás, la barra de ruta) queda seleccionada la carpeta de la que se venía.
      const base = l.path === '/' ? '/' : l.path + '/'
      const name = cwd.startsWith(base) ? cwd.slice(base.length).split('/')[0] : ''
      const from = name && (showHidden || !name.startsWith('.')) ? l.entries.find((e) => e.path === base + name) : undefined
      selected = new Set(from ? [from.path] : [])
      if (from) tick().then(() => document.querySelector(`[data-fs-tab="${tab.id}"] [data-drop="${CSS.escape(from.path)}"]`)?.scrollIntoView({ block: 'nearest' }))
    }
    cwd = l.path
    patchTab(tab.id, { cwd })
    pathInput = l.path
    entries = l.entries
    error = ''
  }

  async function navigate(dir: string, pushHistory = true) {
    if (!sessionId) return
    const seq = ++navSeq
    const hit = cache.get(key(dir))
    if (hit) show(hit, pushHistory)
    loading = !hit
    try {
      const l = await list(dir)
      if (seq !== navSeq) return
      show(l, pushHistory && !hit)
    } catch (e) {
      if (seq !== navSeq) return
      cache.delete(key(dir))
      const msg = errorText(e)
      if (isServerGone(e)) {
        lostConnection()
      } else {
        toast(msg, 'error')
        pathInput = cwd
      }
    } finally {
      if (seq === navSeq) loading = false
    }
  }

  const refresh = () => navigate(cwd, false)
  const goUp = () => cwd !== '/' && navigate(cwd.replace(/\/[^/]+\/?$/, '') || '/')
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
    if (!focused || menu || syncOpen) return
    if (e.button === 3) goBack()
    else if (e.button === 4) goForward()
    else return
    e.preventDefault()
  }
  const join = (name: string) => (cwd.endsWith('/') ? cwd + name : `${cwd}/${name}`)

  // Doble clic / Enter: carpetas se navegan; archivos se abren en el editor
  // (o se descargan si no son texto editable).
  async function open(e: FileEntry) {
    if (e.isDir) navigate(e.path)
    else if (!(await edit(e, true))) download([e.path])
  }

  // ---------- Arrastrar elementos ----------
  // Soltar sobre una carpeta (de la lista o de la barra de ruta) de este u otro
  // explorador del mismo servidor mueve los elementos ahí; soltar archivos de
  // "Este equipo" los sube; sacar el cursor de la ventana pasa a un arrastre
  // nativo del sistema para soltarlos en el Explorador o el escritorio.
  const fsView = {
    cwd: () => cwd,
    verb(src: FsSource, paths: string[], dir: string) {
      if (!sessionId) return null
      if (src.side === 'local') return tr('sftp.upload', { what: describe(paths), dir })
      if (src.connId !== tab.connId) return null // entre servidores distintos: todavía no
      return allIn(paths, dir) ? null : tr('local.move', { what: describe(paths), dir })
    },
    drop(src: FsSource, paths: string[], dir: string) {
      if (src.side === 'local') upload(paths, dir)
      else moveTo(paths, dir, src.tabId)
    },
    refresh: () => refresh(),
  }

  async function moveTo(paths: string[], dir: string, fromTab = tab.id) {
    if (!sessionId) return
    try {
      await api.sftpMove(sessionId, paths, dir)
      toast(`${describe(paths)} → ${dir}`, 'success', 2500)
      selected = new Set()
    } catch (e) {
      toast(moveErrorText(e), 'error', 6000)
    }
    refresh()
    if (fromTab !== tab.id) fsViews.get(fromTab)?.refresh()
  }

  async function dragOut(paths: string[]) {
    if (!sessionId) return
    toast(tr('sftp.preparing', { what: describe(paths) }), 'info', 2500)
    try {
      if (await api.sftpDragOut(sessionId, paths)) toast(tr('sftp.copiedToPc', { what: describe(paths) }), 'success', 2500)
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  // ---------- acciones ----------

  async function upload(paths: string[], dir = cwd) {
    if (!sessionId || !paths.length) return
    const sid = sessionId
    await transfer((policy) => api.sftpUpload(sid, paths, dir, policy))
  }

  async function uploadDialog(folder = false) {
    if (!sessionId) return
    try {
      const paths = folder ? [await api.pickUploadFolder()].filter(Boolean) : ((await api.pickUploadFiles()) ?? [])
      await upload(paths)
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function download(paths: string[]) {
    if (!sessionId || !paths.length) return
    const sid = sessionId
    try {
      // Con "Preguntar dónde descargar" apagado va directo a la carpeta de los ajustes.
      const { askDownloadDir, downloadDir } = get(settings).files
      const dir = askDownloadDir || !downloadDir ? await api.pickDownloadDir() : downloadDir
      if (dir) await transfer((policy) => api.sftpDownload(sid, paths, dir, policy))
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function mkdir() {
    const name = await promptDialog({ title: tr('local.newFolder'), message: tr('local.insideOf', { dir: cwd }), okText: tr('common.create') })
    if (!name || !sessionId) return
    try {
      await api.sftpMkdir(sessionId, join(name))
      refresh()
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function newFile() {
    const name = await promptDialog({ title: tr('sftp.newFile'), message: tr('local.insideOf', { dir: cwd }), okText: tr('common.create') })
    if (!name || !sessionId) return
    try {
      await api.sftpCreateFile(sessionId, join(name))
      await refresh()
      editHere(join(name))
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function rename(e: FileEntry) {
    const name = await renameDialog(e)
    if (!name || !sessionId) return
    try {
      await api.sftpRename(sessionId, e.path, join(name))
      refresh()
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  // ---------- Copiar / Pegar ----------
  // "Copiar" guarda las rutas en el portapapeles de archivos de OSAT; "Pegar" las
  // copia en la carpeta de esta lista (o dentro de una carpeta elegida). En la misma
  // carpeta crea "x - copia.ext" para renombrarla después; nunca pisa nada. SFTP no
  // copia en el servidor: los bytes pasan por esta PC, así que un archivo muy grande
  // tarda. Si lo copiado viene de "Este equipo", pegar lo sube.
  let copying = false

  function copySel(list: FileEntry[]) {
    if (!sessionId || !list.length) return
    const paths = list.map((e) => e.path)
    fileClip.set({ source, paths })
    toast(tr('local.copiedHint', { what: describe(paths), keys: keys('Ctrl+V') }), 'info', 2500)
  }

  async function paste(dir = cwd) {
    const clip = get(fileClip)
    if (!clip || !sessionId || copying) return
    if (clip.source.side === 'local') return upload(clip.paths, dir)
    if (clip.source.connId !== tab.connId) return toast(tr('sftp.betweenServers'), 'error')
    copying = true
    try {
      const made = await api.sftpCopy(sessionId, clip.paths, dir)
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

  // ---------- Comprimir / Extraer ----------
  // Se hacen en el servidor, con su propio tar / zip / unzip: los bytes no pasan por esta PC.
  // Comprimir crea un archivo de lo seleccionado en esta carpeta; el formato lo da la extensión del nombre
  // (.tar.gz por defecto, .zip, .tar). Extraer no pisa nada (ver Manager.Extract en el backend).
  let compressing = false

  const archiveExt = /\.(tar\.gz|tgz|tar\.bz2|tbz2|tar\.xz|txz|tar\.zst|tzst|tar|zip)$/i
  const isArchive = (e: FileEntry) => !e.isDir && archiveExt.test(e.name) && e.name.replace(archiveExt, '') !== ''

  async function extract(e: FileEntry) {
    if (!sessionId || compressing) return
    compressing = true
    toast(tr('sftp.extracting'), 'info', 4000)
    try {
      const made = await api.sftpExtract(sessionId, e.path)
      toast(tr('sftp.extracted', { name: made.split('/').pop() ?? '' }), 'success', 3000)
      await refresh()
      selected = new Set([made])
    } catch (err) {
      toast(errorText(err), 'error', 6000)
      refresh()
    } finally {
      compressing = false
    }
  }

  async function compress(list: FileEntry[], ext: '.zip' | '.tar.gz') {
    if (!sessionId || !list.length || compressing) return
    const base = list.length === 1 ? list[0].name : cwd.replace(/\/+$/, '').split('/').pop() || tr('sftp.defaultArchiveName')
    const typed = await promptDialog({
      title: tr('sftp.compressTitle', { ext }),
      message: tr('sftp.compressMsg', { what: describe(list.map((e) => e.path)), ext }),
      value: base + ext,
      selectEnd: base.length,
      okText: tr('sftp.compress'),
    })
    if (!typed?.trim()) return
    // El formato lo eligió el botón: si el nombre no termina en esa extensión (o .tgz), se la agrega.
    const t = typed.trim().toLowerCase()
    const name = t.endsWith(ext) || (ext === '.tar.gz' && t.endsWith('.tgz')) ? typed.trim() : typed.trim() + ext
    compressing = true
    toast(tr('sftp.compressing'), 'info', 4000)
    try {
      const made = await api.sftpCompress(sessionId, list.map((e) => e.path), name)
      toast(tr('sftp.created', { name: made.split('/').pop() ?? '' }), 'success', 3000)
      await refresh()
      selected = new Set([made])
    } catch (e) {
      toast(errorText(e), 'error', 6000)
      refresh()
    } finally {
      compressing = false
    }
  }

  async function remove(list: FileEntry[]) {
    if (!sessionId || !list.length) return
    const ok = await confirmDialog({
      title: tr('sftp.deleteTitle'),
      message:
        list.length === 1
          ? tr(list[0].isDir ? 'sftp.deleteOneDir' : 'sftp.deleteOne', { name: list[0].name })
          : tr('sftp.deleteMany', { count: list.length }),
      okText: tr('common.delete'),
      danger: true,
    })
    if (!ok) return
    try {
      await api.sftpRemove(sessionId, list.map((e) => e.path))
      refresh()
    } catch (err) {
      toast(errorText(err), 'error')
      refresh()
    }
  }

  function modeToOctal(mode: string) {
    const p = mode.slice(-9)
    let out = ''
    for (let i = 0; i < 9; i += 3) {
      let n = 0
      if (p[i] === 'r') n += 4
      if (p[i + 1] === 'w') n += 2
      if (/[xst]/.test(p[i + 2])) n += 1
      out += n
    }
    return out
  }

  async function chmod(e: FileEntry) {
    const v = await promptDialog({
      title: tr('sftp.permissions'),
      message: tr('sftp.chmodMsg', { name: e.name }),
      value: modeToOctal(e.mode),
      okText: tr('sftp.apply'),
    })
    if (!v || !sessionId) return
    if (!/^[0-7]{3,4}$/.test(v)) return toast(tr('sftp.badMode'), 'error')
    try {
      await api.sftpChmod(sessionId, e.path, parseInt(v, 8))
      refresh()
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  // ---------- Buscar ----------
  // Panel de búsqueda de archivos en esta carpeta y sus subcarpetas (ver SftpSearch). Doble clic en un
  // resultado lo abre; clic derecho > "Mostrar en su carpeta" navega hasta él.
  let searchOpen = false
  let searchView: SftpSearch | undefined

  async function openSearch() {
    if (!sessionId) return
    if (searchOpen) return searchView?.focus()
    searchOpen = true
    await tick()
    searchView?.focus()
  }

  async function openFound(e: FileEntry) {
    if (!sessionId) return
    if (e.isDir) return navigate(e.path)
    // mode '?': el servidor no dijo si es carpeta (find sin -printf) o es un enlace: si se puede listar, es una carpeta.
    if (e.mode === '?') {
      try {
        await api.sftpList(sessionId, e.path)
        return navigate(e.path)
      } catch {
        // es un archivo
      }
    }
    open(e)
  }

  async function revealFound(e: FileEntry) {
    if (e.name.startsWith('.')) showHidden = true
    await navigate(e.path.replace(/\/[^/]+\/?$/, '') || '/')
    selected = new Set([e.path])
  }

  // ---------- Sincronizar ----------
  // Diálogo para sincronizar esta carpeta con una de esta PC (ver SyncDialog). Mientras está abierto, los
  // atajos del explorador no responden (Supr borraría lo seleccionado detrás del diálogo).
  let syncOpen = false

  // Abre el archivo en el editor de abajo. Antes se verifica que sea texto
  // editable, para no abrir un editor que solo muestre un error.
  // Devuelve false si no se puede editar (binario, otra codificación, > 2 MB).
  async function edit(e: FileEntry, quiet = false, inTab = false) {
    if (!sessionId) return false
    let text: string
    try {
      text = await api.sftpReadText(sessionId, e.path)
    } catch (err) {
      // Sin permiso: se ofrece abrirlo como root en vez de descargarlo.
      if (isPermissionError(err)) {
        editAsRoot(e, true)
        return true
      }
      if (!quiet) toast(errorText(err), 'error')
      return false
    }
    // El editor recibe el texto ya leído: no lo vuelve a pedir al servidor.
    if (inTab) openEditor(tab, sessionId, e.path, text)
    else editHere(e.path, text)
    return true
  }

  // Como root (sudo): siempre en su propia pestaña, nunca en el panel de abajo. La pestaña lo lee con
  // sudo y pide la contraseña si hace falta. ask: preguntar antes (después de un permiso denegado).
  async function editAsRoot(e: FileEntry, ask = false) {
    const sid = sessionId
    if (!sid || (ask && !(await askRoot('read')))) return
    openEditor(tab, sid, e.path, undefined, true)
  }

  // ---------- Panel de abajo ("dock") ----------
  // Aloja una terminal y los archivos abiertos, en un split horizontal (50/50,
  // ajustable). Cada uno puede pasar a una pestaña independiente sin perder la
  // sesión ni lo que tenía (pantalla de la terminal, texto sin guardar).
  interface DockItem {
    tab: Tab // pseudo-pestaña (no está en el store de pestañas)
    dirty?: boolean
  }
  let dock: DockItem[] = []
  let dockActive: string | null = null
  let dockViews: Record<string, any> = {} // TerminalView | EditorView
  let dockSeq = 0
  let splitRatio = 0.5

  // Zoom de la lista de archivos (Ctrl + rueda, Ctrl +/-/0).
  let listZoom = 1
  const zoomList = (dir: number) => (listZoom = Math.round(Math.min(2, Math.max(0.6, listZoom + dir * 0.1)) * 10) / 10)
  let panesEl: HTMLDivElement

  // El explorador muestra ● (y pide confirmar al cerrarlo) si un editor de abajo tiene cambios.
  $: dockDirty = dock.some((d) => d.dirty)
  $: if (dockDirty !== !!tab.dirty) patchTab(tab.id, { dirty: dockDirty })

  /** text: contenido ya leído de un archivo (el editor lo muestra sin pedirlo). */
  function addToDock(extra: Partial<Tab> & Pick<Tab, 'kind'>, text?: string) {
    const t: Tab = {
      id: `${tab.id}:dock${++dockSeq}`,
      connId: tab.connId,
      title: tab.title,
      color: tab.color,
      fromSession: sessionId ?? undefined,
      status: 'connecting',
      ...extra,
    }
    if (text !== undefined) handoffs.set(t.id, { text, saved: text })
    dock = [...dock, { tab: t }]
    dockActive = t.id
  }

  function terminalHere(dir = cwd) {
    const d = dock.find((x) => x.tab.kind === 'terminal')
    if (!d) return addToDock({ kind: 'terminal', initialDir: dir })
    dockActive = d.tab.id
    dockViews[d.tab.id]?.cd(dir)
  }

  function editHere(path: string, text?: string) {
    // Si ya está abierto en una pestaña independiente, se va ahí.
    const open = get(tabs).find((t) => t.kind === 'editor' && t.connId === tab.connId && t.path === path && !t.sudo)
    if (open) return activeTabId.set(open.id)
    const d = dock.find((x) => x.tab.kind === 'editor' && x.tab.path === path)
    if (d) dockActive = d.tab.id
    else addToDock({ kind: 'editor', path, title: path.split('/').pop() || path }, text)
  }

  function removeFromDock(id: string) {
    const i = dock.findIndex((d) => d.tab.id === id)
    dock = dock.filter((d) => d.tab.id !== id)
    delete dockViews[id]
    if (dockActive === id) dockActive = (dock[i] ?? dock[i - 1])?.tab.id ?? null
  }

  async function closeDocked(id: string) {
    const d = dock.find((x) => x.tab.id === id)
    if (!d) return
    if (d.dirty) {
      const ok = await confirmDialog({
        title: tr('sftp.unsavedTitle'),
        message: tr('sftp.unsavedMsg', { path: d.tab.path ?? '' }),
        okText: tr('sftp.discardClose'),
        danger: true,
      })
      if (!ok) return
    }
    removeFromDock(id)
  }

  // Pasa el elemento de abajo a una pestaña independiente, con la misma sesión.
  // toRoot: el editor no tuvo permiso y se pasa a root (sudo); si estaba guardando, la pestaña reintenta.
  function detachDocked(id: string, toRoot = false) {
    const d = dock.find((x) => x.tab.id === id)
    if (!d) return
    const { kind, path, title, initialDir } = d.tab
    const sudo = toRoot || d.tab.sudo || undefined
    const base = { kind, connId: tab.connId, color: tab.color, fromSession: sessionId ?? undefined }
    const h = dockViews[id]?.detach()
    if (kind === 'terminal') {
      // Sin sesión todavía (conectando / caída): la pestaña abre una nueva.
      if (h) openTab({ ...base, title: tab.title, sessionId: h.sessionId }, { screen: h.screen })
      else openTab({ ...base, title: tab.title, initialDir })
    } else {
      if (h) openTab({ ...base, title, path, sudo, sessionId: h.sessionId || undefined }, { text: h.text, saved: h.saved, save: toRoot })
      else openTab({ ...base, title, path, sudo })
    }
    removeFromDock(id)
  }

  // Flechas arriba/abajo: mueven el divisor de a 5 %.
  function splitKey(e: KeyboardEvent) {
    const d = e.key === 'ArrowUp' ? -0.05 : e.key === 'ArrowDown' ? 0.05 : 0
    if (!d) return
    e.preventDefault()
    splitRatio = Math.min(0.85, Math.max(0.15, splitRatio + d))
  }

  function startSplitDrag(e: MouseEvent) {
    e.preventDefault()
    const rect = panesEl.getBoundingClientRect()
    const move = (ev: MouseEvent) => {
      splitRatio = Math.min(0.85, Math.max(0.15, (ev.clientY - rect.top) / rect.height))
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  // Si el foco quedó en la terminal, los atajos del explorador (Supr, F2…)
  // no funcionarían al volver a la lista: se lo sacamos.
  function blurTerminal() {
    const el = document.activeElement as HTMLElement | null
    if (el?.classList.contains('xterm-helper-textarea')) el.blur()
  }

  function terminalInTab(dir = cwd) {
    openTab({
      kind: 'terminal',
      connId: tab.connId,
      title: tab.title,
      color: tab.color,
      fromSession: sessionId ?? undefined,
      initialDir: dir,
    })
  }

  function rowMenu(ev: MouseEvent, e: FileEntry) {
    const many = selected.size > 1
    const sel = entries.filter((x) => selected.has(x.path))
    menu = {
      x: ev.clientX,
      y: ev.clientY,
      items: [
        ...(e.isDir && !many ? [{ label: tr('common.open'), icon: 'folder', action: () => navigate(e.path) }] : []),
        { label: many ? tr('sftp.downloadMany', { count: sel.length }) : tr('sftp.download'), icon: 'download', action: () => download(sel.map((x) => x.path)) },
        ...(!e.isDir && !many
          ? [
              { label: tr('sftp.edit'), icon: 'edit', action: () => edit(e) },
              { label: tr('sftp.editNewTab'), action: () => edit(e, false, true) },
              { label: tr('sftp.openAsRoot'), icon: 'lock', action: () => editAsRoot(e) },
              { label: tr('sftp.openBrowser'), icon: 'globe', action: () => openBrowser(tab, sessionId ?? undefined, { file: e.path }) },
            ]
          : []),
        ...(e.isDir && !many
          ? [
              { label: isServerPinned(tab.connId, e.path) ? tr('sftp.unpinFolder') : tr('sftp.pinFolder'), icon: 'pin', action: () => toggleServerPin(tab.connId, e.path) },
              { label: tr('sftp.openAsRepo'), icon: 'repo', action: () => openRepo(tab, sessionId ?? undefined, e.path) },
              { label: tr('sftp.openBrowser'), icon: 'globe', action: () => openBrowser(tab, sessionId ?? undefined, { root: e.path }) },
              { label: tr('local.terminalHere'), icon: 'terminal', action: () => terminalHere(e.path) },
              { label: tr('sftp.terminalNewTab'), action: () => terminalInTab(e.path) },
            ]
          : []),
        { separator: true, label: '' },
        { label: tr('term.copy'), icon: 'copy', hint: keys('Ctrl+C'), action: () => copySel(sel) },
        ...(e.isDir && !many
          ? [{ label: tr('local.pasteInto'), icon: 'paste', disabled: !get(fileClip), action: () => paste(e.path) }]
          : []),
        { label: compressing ? tr('sftp.working') : tr('sftp.compressZip'), icon: 'file', disabled: compressing, action: () => compress(sel, '.zip') },
        { label: compressing ? tr('sftp.working') : tr('sftp.compressTarGz'), icon: 'file', disabled: compressing, action: () => compress(sel, '.tar.gz') },
        ...(!many && isArchive(e) ? [{ label: tr('sftp.extractHere'), icon: 'folderPlus', disabled: compressing, action: () => extract(e) }] : []),
        { label: tr('local.rename'), hint: 'F2', disabled: many, action: () => rename(e) },
        { label: tr('sftp.permissionsMenu'), icon: 'lock', disabled: many, action: () => chmod(e) },
        { label: tr('local.copyPath'), icon: 'copy', action: () => window.runtime?.ClipboardSetText?.(sel.map((x) => x.path).join('\n')) },
        { separator: true, label: '' },
        { label: tr('local.deleteMenu'), icon: 'trash', danger: true, action: () => remove(sel) },
      ],
    }
  }

  function bgMenu(ev: MouseEvent) {
    menu = {
      x: ev.clientX,
      y: ev.clientY,
      items: [
        { label: tr('term.paste'), icon: 'paste', hint: keys('Ctrl+V'), disabled: !get(fileClip), action: () => paste() },
        { separator: true, label: '' },
        { label: tr('sftp.uploadFiles'), icon: 'upload', action: () => uploadDialog() },
        { label: tr('sftp.uploadFolder'), icon: 'upload', action: () => uploadDialog(true) },
        { label: tr('sftp.syncMenu'), icon: 'swap', disabled: !sessionId, action: () => (syncOpen = true) },
        { separator: true, label: '' },
        { label: tr('local.newFolder'), icon: 'folderPlus', action: mkdir },
        { label: tr('sftp.newFile'), icon: 'file', action: newFile },
        { label: isServerPinned(tab.connId, cwd) ? tr('sftp.unpinFolder') : tr('sftp.pinFolder'), icon: 'pin', action: () => toggleServerPin(tab.connId, cwd) },
        { label: tr('sftp.openFolderAsRepo'), icon: 'repo', action: () => openRepo(tab, sessionId ?? undefined, cwd) },
        { label: tr('local.terminalHere'), icon: 'terminal', action: () => terminalHere() },
        { label: tr('sftp.terminalNewTab'), action: () => terminalInTab() },
        { separator: true, label: '' },
        { label: tr('local.refresh'), icon: 'refresh', action: refresh },
      ],
    }
  }

  function onKey(e: KeyboardEvent) {
    if (!focused || menu || syncOpen) return
    const tag = (e.target as HTMLElement)?.tagName
    if (tag === 'INPUT' || tag === 'TEXTAREA') return
    if (e.altKey && e.key === 'ArrowLeft') goBack()
    else if (e.altKey && e.key === 'ArrowRight') goForward()
    else if (isMac() && e.metaKey && e.key === 'Backspace') remove(selEntries) // como Supr, al estilo del Finder
    else if (e.key === 'Backspace') goUp()
    else if (e.key === 'F5') refresh()
    else if (e.key === 'Delete') remove(selEntries)
    else if (e.key === 'F2' && selEntries.length === 1) rename(selEntries[0])
    else if (e.key === 'Enter' && selEntries.length === 1) open(selEntries[0])
    else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'f') openSearch()
    else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'a') selected = new Set(visible.map((x) => x.path))
    else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'c' && selEntries.length) copySel(selEntries)
    else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'v') paste()
    else if ((e.ctrlKey || e.metaKey) && (e.key === '=' || e.key === '+')) zoomList(1)
    else if ((e.ctrlKey || e.metaKey) && e.key === '-') zoomList(-1)
    else if ((e.ctrlKey || e.metaKey) && e.key === '0') listZoom = 1
    else return
    e.preventDefault()
  }

  let refreshTimer: ReturnType<typeof setTimeout>
  const unsubUpload = uploadFinished.subscribe((u) => {
    if (u && u.sessionId === sessionId) {
      clearTimeout(refreshTimer)
      refreshTimer = setTimeout(refresh, 300)
    }
  })

  onMount(() => {
    dropHandlers.set(tab.id, upload)
    fsViews.set(tab.id, fsView)
    window.addEventListener('keydown', onKey)
    window.addEventListener('mouseup', onMouse)
    connect()
  })

  onDestroy(() => {
    dropHandlers.delete(tab.id)
    fsViews.delete(tab.id)
    window.removeEventListener('keydown', onKey)
    window.removeEventListener('mouseup', onMouse)
    unsubUpload()
    unlost?.()
    const r = retry
    retry = null
    r?.cancel() // si un intento conecta después, la sesión se cierra (discard)
  })
</script>

<div class="sftp">
  <div class="toolbar">
    <button class="icon" title={$t('local.backTip')} disabled={!history.length} on:click={goBack} aria-label={$t('local.back')}><Icon name="back" /></button>
    <button class="icon" title={$t('local.forwardTip')} disabled={!forward.length} on:click={goForward} aria-label={$t('local.forward')}><Icon name="forward" /></button>
    <button class="icon" title={$t('local.upTip')} disabled={cwd === '/'} on:click={goUp} aria-label={$t('local.up')}><Icon name="up" /></button>
    <button class="icon" title={$t('sftp.home')} on:click={() => navigate(home)} aria-label={$t('sftp.home')}><Icon name="home" /></button>
    <button class="icon" title={$t('local.refreshTip')} on:click={refresh} aria-label={$t('local.refresh')}><Icon name="refresh" /></button>
    <button
      class="icon"
      class:on={showHidden}
      title={showHidden ? $t('sftp.hideHidden') : $t('sftp.showHidden')}
      aria-label={showHidden ? $t('sftp.hideHidden') : $t('sftp.showHidden')}
      aria-pressed={showHidden}
      on:click={toggleHidden}
    >
      <Icon name={showHidden ? 'eye' : 'eyeOff'} />
    </button>

    <form class="path" on:submit|preventDefault={() => navigate(pathInput)}>
      <div class="crumbs">
        {#each crumbs as c, i}
          <button type="button" class="crumb" class:droptarget={dropDir === c.path} data-drop={c.path} on:click={() => navigate(c.path)}>{c.name}</button>
          {#if i > 0 && i < crumbs.length - 1}<span class="slash">/</span>{/if}
        {/each}
      </div>
      <input class="mono" bind:value={pathInput} spellcheck="false" title={$t('local.pathTip')} />
    </form>

    <div class="filter">
      <Icon name="search" size={13} />
      <input placeholder={$t('local.filter')} bind:value={filter} />
    </div>

    <div class="sep" />
    <button class="icon" title={$t('sftp.tbUploadTip')} aria-label={$t('sftp.tbUploadTip')} on:click={() => uploadDialog()}><Icon name="upload" /></button>
    <button class="icon" title={$t('sftp.tbDownloadTip')} aria-label={$t('sftp.tbDownloadTip')} disabled={!selected.size} on:click={() => download([...selected])}>
      <Icon name="download" />
    </button>
    <button class="icon" class:on={searchOpen} title={$t('sftp.tbSearchTip', { keys: keys('Ctrl+F') })} aria-label={$t('sftp.tbSearchTip', { keys: keys('Ctrl+F') })} disabled={!sessionId} on:click={openSearch}>
      <Icon name="search" />
    </button>
    <button class="icon" class:on={syncOpen} title={$t('sftp.tbSyncTip')} aria-label={$t('sftp.tbSyncTip')} disabled={!sessionId} on:click={() => (syncOpen = true)}>
      <Icon name="swap" />
    </button>
    <button class="icon" title={$t('local.newFolder')} on:click={mkdir} aria-label={$t('local.newFolder')}><Icon name="folderPlus" /></button>
    <button class="icon" title={$t('sftp.terminalInFolder')} on:click={() => terminalHere()} aria-label={$t('sftp.terminalInFolder')}><Icon name="terminal" /></button>
    <button class="icon" title={$t('sftp.openFolderAsRepo')} disabled={!sessionId || !cwd} on:click={() => openRepo(tab, sessionId ?? undefined, cwd)} aria-label={$t('sftp.openFolderAsRepo')}>
      <Icon name="repo" />
    </button>
    <button class="icon" title={$t('sftp.browserTip')} disabled={!sessionId} on:click={() => openBrowser(tab, sessionId ?? undefined)} aria-label={$t('sidebar.browser')}>
      <Icon name="globe" />
    </button>
  </div>

  {#if searchOpen && sessionId}
    <SftpSearch
      bind:this={searchView}
      {sessionId}
      {cwd}
      on:open={(e) => openFound(e.detail)}
      on:reveal={(e) => revealFound(e.detail)}
      on:close={() => (searchOpen = false)}
    />
  {/if}

  <div class="panes" bind:this={panesEl}>
    <div class="files" style="flex: {dock.length ? splitRatio : 1} 1 0">
      {#if error}
        <div class="error">
          <p>{error}</p>
          {#if retry}
            <p class="retry">{retryText}</p>
            <div class="retrybtns">
              <button class="primary" on:click={() => retry?.retryNow()}>{$t('sftp.retryNow')}</button>
              <button on:click={() => retry?.cancel()}>{$t('common.cancel')}</button>
            </div>
          {:else}
            <button class="primary" on:click={connect}>{$t('sftp.reconnect')}</button>
          {/if}
        </div>
      {:else}
        <div class="listwrap" role="presentation" on:mousedown={blurTerminal}>
          <FileList
            entries={shown}
            {source}
            {cwd}
            {filter}
            {loading}
            bind:selected
            bind:visible
            bind:zoom={listZoom}
            emptyText={$t('sftp.emptyDrop')}
            osDropHint={$t('sftp.dropHint', { dir: cwd })}
            dragOut={dragOut}
            on:open={(e) => open(e.detail)}
            on:hover={(e) => prefetch(e.detail)}
            on:menu={(e) => (e.detail.entry ? rowMenu(e.detail.ev, e.detail.entry) : bgMenu(e.detail.ev))}
          />
        </div>
      {/if}

      <div class="status">
        {#if loading}{$t('snippets.loading')}{:else if copying}{$t('local.copying')}{:else}
          {$t('local.items', { count: visible.length })}{entries.length > shown.length ? $t('sftp.hiddenCount', { count: entries.length - shown.length }) : ''}{selected.size ? $t('local.selected', { count: selected.size, size: fmtSize(selEntries.reduce((a, e) => a + (e.isDir ? 0 : e.size), 0)) }) : ''}
        {/if}
      </div>
    </div>

    {#if dock.length}
      <div
        class="divider"
        role="slider"
        aria-orientation="horizontal"
        aria-label={$t('sftp.panelsSize')}
        aria-valuenow={Math.round(splitRatio * 100)}
        aria-valuemin={15}
        aria-valuemax={85}
        tabindex="0"
        title={$t('sftp.dragResize')}
        on:mousedown={startSplitDrag}
        on:keydown={splitKey}
      />
      <div class="termpane" style="flex: {1 - splitRatio} 1 0">
        <div class="termbar">
          {#each dock as d (d.tab.id)}
            <button
              class="dtab"
              class:on={d.tab.id === dockActive}
              title={d.tab.path ?? $t('sidebar.tabTerminal')}
              on:click={() => (dockActive = d.tab.id)}
              on:auxclick={(e) => e.button === 1 && closeDocked(d.tab.id)}
            >
              <Icon name={d.tab.kind === 'terminal' ? 'terminal' : 'file'} size={13} />
              {d.tab.kind === 'terminal' ? $t('sidebar.tabTerminal') : d.tab.title}{d.dirty ? ' ●' : ''}
            </button>
          {/each}
          <span class="spacer" />
          <button class="icon" title={$t('sftp.openIndependent')} on:click={() => dockActive && detachDocked(dockActive)} aria-label={$t('sftp.openIndependent')}>
            <Icon name="external" size={13} />
          </button>
          <button class="icon" title={$t('common.close')} on:click={() => dockActive && closeDocked(dockActive)} aria-label={$t('common.close')}>
            <Icon name="x" size={13} />
          </button>
        </div>
        {#each dock as d, i (d.tab.id)}
          <div class="dockview" class:hidden={d.tab.id !== dockActive}>
            {#if d.tab.kind === 'terminal'}
              <TerminalView
                bind:this={dockViews[d.tab.id]}
                tab={d.tab}
                active={active && d.tab.id === dockActive}
                focused={focused && d.tab.id === dockActive}
                embedded
              />
            {:else}
              <EditorView
                bind:this={dockViews[d.tab.id]}
                bind:dirty={dock[i].dirty}
                tab={d.tab}
                focused={focused && d.tab.id === dockActive}
                embedded
                toRoot={() => detachDocked(d.tab.id, true)}
              />
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

{#if menu}
  <ContextMenu {...menu} on:close={() => (menu = null)} />
{/if}

{#if syncOpen && sessionId}
  <SyncDialog {sessionId} {cwd} on:close={() => (syncOpen = false)} />
{/if}

<style>
  .sftp {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  /* Si el panel es angosto, los botones bajan a otra fila en vez de cortarse. */
  .toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 4px;
    padding: 6px 8px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-2);
  }
  /* Base cero (cuenta su mínimo): con la barra en varias filas, primero se achican la ruta y el filtro
     y recién después bajan los botones. */
  .path {
    flex: 1 1 0;
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
  .filter {
    display: flex;
    align-items: center;
    gap: 4px;
    border: 1px solid var(--border);
    padding: 0 6px;
    background: var(--bg);
    color: var(--text-3);
    flex: 1 1 0;
    min-width: 70px;
    max-width: 140px;
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
  .panes {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  .files,
  .termpane {
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  .divider {
    flex: none;
    height: 5px;
    cursor: row-resize;
    border-top: 1px solid var(--border);
  }
  .termbar {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 2px 4px;
    border-bottom: 1px solid var(--border);
    color: var(--text-2);
  }
  .dtab {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    border: 0;
    background: transparent;
    color: inherit;
    padding: 2px 8px;
  }
  /* Íconos más compactos (como los de la barra lateral): son 14 y tienen que entrar en un panel angosto. */
  .toolbar button.icon {
    padding: 6px 7px;
  }
  .toolbar button.icon.on {
    background: var(--bg-4);
  }
  .dtab.on {
    background: var(--bg-4);
    color: var(--text);
  }
  .dockview {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  .dockview.hidden {
    display: none;
  }
  .termbar .spacer {
    flex: 1;
  }
  .listwrap {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  .droptarget {
    outline: 1px solid var(--text-2);
    outline-offset: -1px;
  }
  .error {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 12px;
    color: var(--text-2);
  }
  .error .retry {
    font-size: 12px;
    color: var(--text-3);
  }
  .retrybtns {
    display: flex;
    gap: 8px;
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
</style>
