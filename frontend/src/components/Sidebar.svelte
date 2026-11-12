<script lang="ts">
  import { t as tl, tr } from '../lib/i18n'
  import { tabTitle } from '../lib/tabtitle'
  import { createEventDispatcher, onDestroy, onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { treeNav } from '../lib/treenav'
  import LogoMark from '../lib/LogoMark.svelte'
  import ContextMenu, { type MenuItem } from './ContextMenu.svelte'
  import ConnRow from './ConnRow.svelte'
  import { api, errorText, type Connection, type NamedWorkspace, type PinnedDir, type Site } from '../lib/api'
  import { confirmDialog, promptDialog, questionsDialog } from '../lib/dialog'
  import { overlayHolder } from '../lib/overlay'
  import { filterSites, placeBeforeId } from '../lib/sites'
  import { normalizeSiteUrl, siteHost } from '../lib/siteUrl'
  import { baseName, buildRows, FAV_KEY, favoriteConns, isWithin, parentOf } from '../lib/groups'
  import { keys } from '../lib/platform'
  import {
    deleteWorkspace,
    loadWorkspaces,
    namedWorkspaces,
    openNamedWorkspace,
    renameWorkspace,
    saveCurrentWorkspace,
    updateWorkspace,
  } from '../lib/workspaces'
  import {
    activeTabId,
    browserSupported,
    exportGroup,
    closeTab,
    connections,
    defaultShell,
    groupPaths,
    kindIcon,
    loadLocalShells,
    localShells,
    LOCAL,
    openBlankSite,
    openBrowser,
    openSite,
    openForConnection,
    openLocal,
    openLocalTerminal,
    openAppTab,
    openPinnedDir,
    openSftpAt,
    openPorts,
    pinnedDirs,
    pinNames,
    reloadConnections,
    reloadSites,
    renamePinnedDir,
    settings,
    sidebarOpen,
    sites,
    snippetsRequest,
    tabs,
    toast,
    togglePinnedDir,
    type Tab,
  } from '../lib/stores'

  const dispatch = createEventDispatcher<{ new: void; edit: Connection }>()

  // Los snippets se abren en la terminal activa (el panel vive dentro de cada terminal, ver TerminalView).
  function openSnippets() {
    const t = $tabs.find((x) => x.id === $activeTabId)
    if (t?.kind === 'terminal') snippetsRequest.set(t.id)
    else toast(tr('sidebar.snippetsNoTerminal'), 'info')
  }

  // Clic derecho en el botón: usarlos en la terminal activa o abrir la pestaña para editarlos.
  function snippetsMenu(e: MouseEvent) {
    e.preventDefault()
    menu = {
      x: e.clientX,
      y: e.clientY,
      items: [
        { label: tr('sidebar.snippetsUse'), icon: 'code', hint: keys('Ctrl+Shift+S'), action: openSnippets },
        { label: tr('snippets.manage'), icon: 'edit', action: () => openAppTab('snippets') },
      ],
    }
  }

  let query = ''
  // Grupos plegados o abiertos a mano: se recuerdan al reabrir osat. Un grupo que no figura arranca
  // plegado (Favoritos, abierto). Clave nueva: el estado viejo solo guardaba los plegados.
  const COLLAPSED_KEY = 'osat.groupsCollapsed'
  let collapsed: Record<string, boolean> = (() => {
    try {
      return JSON.parse(localStorage.getItem(COLLAPSED_KEY) ?? '{}') as Record<string, boolean>
    } catch {
      return {}
    }
  })()
  $: try {
    localStorage.setItem(COLLAPSED_KEY, JSON.stringify(collapsed))
  } catch {
    // sin almacenamiento: vale hasta cerrar la app
  }
  // Reactiva: el encabezado de Favoritos la llama desde el markup y tiene que volver a pintarse cuando cambia `collapsed`.
  $: isCollapsed = (path: string) => collapsed[path] ?? path !== FAV_KEY
  let menu: { x: number; y: number; items: MenuItem[] } | null = null

  $: filtered = $connections.filter((c) => {
    const q = query.trim().toLowerCase()
    if (!q) return true
    return [c.name, c.host, c.user, c.group].some((v) => v?.toLowerCase().includes(q))
  })

  // Al buscar solo se ven los grupos que tienen coincidencias, abiertos.
  $: rows = query.trim() ? buildRows(filtered, [], {}) : buildRows(filtered, $groupPaths, collapsed, true)

  // Favoritos: acceso directo a esas conexiones (también filtrado por la búsqueda).
  $: favs = favoriteConns(filtered)

  $: openConnIds = new Set($tabs.filter((t) => t.status === 'ready').map((t) => t.connId))

  // Pestañas abiertas de cada servidor, para listarlas debajo de él.
  $: tabsByConn = $tabs.reduce<Record<string, Tab[]>>((m, t) => ((m[t.connId] ??= []).push(t), m), {})

  // Hijos de "Este equipo" en el árbol: carpetas fijadas y después sus pestañas.
  $: localTabs = tabsByConn[LOCAL] ?? []
  $: localKids = $pinnedDirs.length + localTabs.length

  // Pie: cuántos servidores hay y cuántos tienen una sesión abierta.

  /** Rama del árbol para el hijo i de n: el último cierra la rama. */
  const branch = (i: number, n: number) => (i === n - 1 ? '└─' : '├─')

  function tabLabel(t: Tab, list: Tab[], tf: typeof tr) {
    if (t.kind === 'terminal' && t.connId === LOCAL) return t.title // PowerShell, cmd...
    if (t.kind === 'sftp' || t.kind === 'local') return t.cwd && t.cwd !== '::drives' ? t.cwd : t.kind === 'sftp' ? 'SFTP' : tabTitle(tf, t)
    if (t.kind === 'editor' || t.kind === 'repo') return (t.dirty ? '● ' : '') + t.title
    if (t.kind === 'browser') return tabTitle(tf, t)
    if (t.kind === 'ports') return tf('sidebar.tabPorts') // los reenvíos activos van en la insignia verde
    const same = list.filter((x) => x.kind === t.kind)
    const base = t.kind === 'rdp' || t.kind === 'vnc' ? tf('sidebar.tabRdp') : tf('sidebar.tabTerminal')
    return same.length > 1 ? `${base} ${same.indexOf(t) + 1}` : base
  }

  /** Nombre de una carpeta fijada: su último tramo ("C:\Users\yo\Descargas" → "Descargas"). */
  const dirLabel = (p: string) => p.replace(/[\\/]+$/, '').split(/[\\/]/).pop() || p

  async function editPinName(p: string) {
    const name = await promptDialog({
      title: tr('sidebar.editName'),
      message: p,
      value: $pinNames[p] ?? dirLabel(p),
      okText: tr('common.save'),
    })
    if (name !== null) renamePinnedDir(p, name)
  }

  function pinMenu(e: MouseEvent, p: string) {
    e.preventDefault()
    menu = {
      x: e.clientX,
      y: e.clientY,
      items: [
        { label: tr('sidebar.editName'), icon: 'edit', action: () => editPinName(p) },
        ...($pinNames[p] ? [{ label: tr('sidebar.useFolderName'), icon: 'refresh', action: () => renamePinnedDir(p, '') }] : []),
        { separator: true, label: '' },
        { label: tr('sidebar.unpin'), icon: 'x', danger: true, action: () => togglePinnedDir(p) },
      ],
    }
  }

  // Carpetas fijadas de un servidor (sub-filas bajo su fila, antes de las pestañas).
  async function editServerPinName(c: Connection, p: PinnedDir) {
    const name = await promptDialog({ title: tr('sidebar.setName'), message: p.path, value: p.name || dirLabel(p.path), okText: tr('common.save') })
    if (name !== null) await run(() => api.renamePin(c.id, p.path, name.trim() === dirLabel(p.path) ? '' : name.trim()))
  }

  const unpin = (c: Connection, p: PinnedDir) => run(() => api.unpinDir(c.id, p.path))

  function serverPinMenu(e: MouseEvent, c: Connection, p: PinnedDir) {
    menu = {
      x: e.clientX,
      y: e.clientY,
      items: [
        { label: tr('common.open'), icon: 'folder', action: () => openSftpAt(c, p.path) },
        { label: tr('sidebar.setNameMenu'), icon: 'edit', action: () => editServerPinName(c, p) },
        { separator: true, label: '' },
        { label: tr('sidebar.unpinShort'), icon: 'x', danger: true, action: () => unpin(c, p) },
      ],
    }
  }

  // ---------- terminales de este equipo ----------

  onMount(loadLocalShells)

  // Lista de terminales para elegir cuál abrir (en la carpeta personal).
  function shellMenu(x: number, y: number) {
    menu = {
      x,
      y,
      items: $localShells.length
        ? $localShells.map((s) => ({
            label: s.id === $defaultShell ? tr('sidebar.defaultSuffix', { name: s.name }) : s.name,
            icon: 'terminal',
            action: () => openLocalTerminal(s.id),
          }))
        : [{ label: tr('sidebar.noTerminal'), disabled: true }],
    }
  }

  function shellMenuFrom(e: MouseEvent) {
    const r = (e.currentTarget as HTMLElement).getBoundingClientRect()
    shellMenu(r.left, r.bottom + 2)
  }

  // ---------- workspaces ----------
  // Espacios de trabajo con nombre: un clic abre uno (reemplaza las pestañas); clic derecho, el resto.

  const WS_KEY = 'osat.workspacesCollapsed'
  let wsCollapsed = (() => {
    try {
      return localStorage.getItem(WS_KEY) === '1'
    } catch {
      return false
    }
  })()
  $: try {
    localStorage.setItem(WS_KEY, wsCollapsed ? '1' : '0')
  } catch {
    // sin almacenamiento: vale hasta cerrar la app
  }

  onMount(() => {
    loadWorkspaces().catch((e) => toast(tr('sidebar.workspacesLoadError', { error: errorText(e) }), 'error'))
  })

  function wsMenu(e: MouseEvent, w?: NamedWorkspace) {
    e.preventDefault()
    e.stopPropagation()
    menu = {
      x: e.clientX,
      y: e.clientY,
      items: w
        ? [
            { label: tr('common.open'), icon: 'splitRight', action: () => openNamedWorkspace(w) },
            { label: tr('sidebar.rename'), icon: 'edit', action: () => renameWorkspace(w) },
            { label: tr('sidebar.updateWorkspace'), icon: 'refresh', action: () => updateWorkspace(w) },
            { separator: true, label: '' },
            { label: tr('sidebar.delete'), icon: 'trash', danger: true, action: () => deleteWorkspace(w) },
          ]
        : [{ label: tr('sidebar.saveWorkspace'), icon: 'plus', action: saveCurrentWorkspace }],
    }
  }

  // ---------- sitios web ----------
  // Páginas de internet guardadas: un clic abre una pestaña con el navegador nativo (SiteView). Se ordenan arrastrando.

  const SITES_KEY = 'osat.sitesCollapsed'
  let sitesCollapsed = (() => {
    try {
      return localStorage.getItem(SITES_KEY) === '1'
    } catch {
      return false
    }
  })()
  $: try {
    localStorage.setItem(SITES_KEY, sitesCollapsed ? '1' : '0')
  } catch {
    // sin almacenamiento: vale hasta cerrar la app
  }

  // Sin navegador (sistema sin soporte o ajuste apagado) no hay sección.
  $: sitesOn = $browserSupported && $settings.general.webBrowser
  $: shownSites = filterSites($sites, query)

  async function saveSite(s: Site | undefined) {
    let name = s?.name ?? ''
    let address = s?.url ?? ''
    for (;;) {
      const a = await questionsDialog({
        title: s ? tr('sites.edit') : tr('sites.add'),
        message: '',
        questions: [
          { text: tr('sites.name'), echo: true, value: name },
          { text: tr('sites.url'), echo: true, value: address },
        ],
      })
      if (!a) return
      name = a[0].trim()
      address = a[1].trim()
      const url = normalizeSiteUrl(address)
      if (!url) {
        toast(tr('site.badUrl'), 'error')
        continue
      }
      try {
        await api.siteSave({ ...(s ? { id: s.id, order: s.order } : {}), name: name || siteHost(url), url })
        await reloadSites()
        sitesCollapsed = false
      } catch (e) {
        toast(errorText(e), 'error')
      }
      return
    }
  }

  async function removeSite(s: Site) {
    const ok = await confirmDialog({ title: tr('sites.remove'), message: tr('sites.removeAsk', { name: s.name }), okText: tr('sites.remove'), danger: true })
    if (!ok) return
    try {
      await api.siteDelete(s.id)
      await reloadSites()
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  function siteMenu(e: MouseEvent, s: Site) {
    e.preventDefault()
    e.stopPropagation()
    menu = {
      x: e.clientX,
      y: e.clientY,
      items: [
        { label: tr('common.open'), icon: 'globe', action: () => openSite(s.url, s.name) },
        { label: tr('sites.editMenu'), icon: 'edit', action: () => saveSite(s) },
        { separator: true, label: '' },
        { label: tr('sites.remove'), icon: 'trash', danger: true, action: () => removeSite(s) },
      ],
    }
  }

  // ---------- grupos ----------

  let justDragged = false // el clic que sigue a soltar un arrastre no debe plegar el grupo

  function toggle(path: string) {
    if (!justDragged) collapsed[path] = !isCollapsed(path)
  }

  const expand = (path: string) => path && (collapsed = { ...collapsed, [path]: false })

  async function run(job: () => Promise<void>) {
    try {
      await job()
      await reloadConnections()
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  // Marca o desmarca las conexiones del grupo (y sus subgrupos; "" = todas) para usar las credenciales por defecto.
  const setUseDefaults = (group: string, on: boolean) =>
    run(async () => {
      const n = await api.setUseDefaultCreds(group, on)
      toast(tr(on ? 'sidebar.useDefaultsDone' : 'sidebar.stopDefaultsDone', { count: n }), 'success')
    })

  async function addGroup(parent: string) {
    const name = (
      await promptDialog({
        title: parent ? tr('sidebar.newSubgroup') : tr('sidebar.newGroup'),
        message: parent ? tr('sidebar.insideOf', { parent }) : tr('sidebar.groupName'),
        okText: tr('common.create'),
      })
    )?.trim()
    if (!name) return
    await run(() => api.addGroup(parent ? `${parent}/${name}` : name))
    expand(parent)
  }

  async function renameGroup(path: string) {
    const name = (await promptDialog({ title: tr('sidebar.editName'), message: path, value: baseName(path), okText: tr('common.save') }))?.trim()
    if (!name || name === baseName(path)) return
    const parent = parentOf(path)
    const to = parent ? `${parent}/${name}` : name
    if (name.includes('/')) return toast(tr('sidebar.groupSlash'), 'error')
    if ($groupPaths.some((g) => g.toLowerCase() === to.toLowerCase() && g !== path)) return toast(tr('sidebar.groupExists', { name: to }), 'error')
    await run(() => api.moveGroup(path, to))
    // El grupo y sus subgrupos siguen plegados si lo estaban.
    collapsed = Object.fromEntries(Object.entries(collapsed).map(([p, v]) => [isWithin(p, path) ? to + p.slice(path.length) : p, v]))
  }

  // Eliminar un grupo borra también sus subgrupos y servidores (path "" = todo),
  // con sus contraseñas y reenvíos; las pestañas abiertas siguen abiertas.
  async function removeGroup(path: string) {
    const conns = $connections.filter((c) => (path ? isWithin(c.group, path) : true)).length
    const subs = new Set(
      [...$groupPaths, ...$connections.map((c) => c.group)].filter((g) => g && g !== path && (!path || isWithin(g, path))),
    ).size
    if (!path && !conns && !subs) return toast(tr('sidebar.nothingToDelete'))
    const plural = (base: string, n: number) => tr(`${base}.${n === 1 ? 'one' : 'other'}`, { count: n })
    const what = [conns && plural('sidebar.connCount', conns), subs && plural(path ? 'sidebar.subgroupCount' : 'sidebar.groupCount', subs)]
      .filter(Boolean)
      .join(tr('sidebar.and'))
    const ok = await confirmDialog({
      title: path ? tr('sidebar.removeGroup') : tr('sidebar.removeAll'),
      message:
        (path ? (what ? tr('sidebar.removeGroupAskWith', { path, what }) : tr('sidebar.removeGroupAsk', { path })) : tr('sidebar.removeAllAsk', { what })) +
        (conns ? ' ' + tr('sidebar.removeCredsNote') : '') +
        ' ' +
        tr('sidebar.noUndo'),
      okText: tr('common.delete'),
      danger: true,
    })
    if (ok) await run(() => api.deleteConnectionsIn(path).then(() => {}))
  }

  // Clic derecho: sobre un servidor o un grupo, sus opciones; en cualquier otro
  // lado, las de toda la lista.
  async function removeConnection(c: Connection) {
    const ok = await confirmDialog({
      title: tr('sidebar.removeConn'),
      message: tr('sidebar.removeConnAsk', { name: c.name }),
      okText: tr('common.delete'),
      danger: true,
    })
    if (ok) await run(() => api.deleteConnection(c.id))
  }

  function listMenu(e: MouseEvent) {
    e.preventDefault()
    // Una carpeta fijada de un servidor (data-menu-pin).
    const pinRow = (e.target as HTMLElement).closest<HTMLElement>('[data-menu-pin]')
    if (pinRow) {
      const pc = $connections.find((x) => x.id === pinRow.dataset.menuPin)
      const pin = pc?.pins?.find((x) => x.path === pinRow.dataset.pinPath)
      if (pc && pin) return serverPinMenu(e, pc, pin)
    }
    // La fila del servidor o una de sus pestañas abiertas (data-menu-conn).
    const row = (e.target as HTMLElement).closest<HTMLElement>('[data-conn-row], [data-menu-conn]')
    const connId = row?.dataset.connRow ?? row?.dataset.menuConn
    const c = connId !== undefined ? $connections.find((x) => x.id === connId) : undefined
    if (c) {
      menu = {
        x: e.clientX,
        y: e.clientY,
        items: [
          { label: c.favorite ? tr('sidebar.unfavorite') : tr('sidebar.favorite'), icon: 'star', action: () => run(() => api.setFavorite(c.id, !c.favorite)) },
          { separator: true, label: '' },
          { label: tr('sidebar.removeConn'), icon: 'trash', danger: true, action: () => removeConnection(c) },
        ],
      }
      return
    }
    const path = (e.target as HTMLElement).closest<HTMLElement>('[data-group-row]')?.dataset.groupRow
    const items: MenuItem[] =
      path === undefined
        ? [
            { label: tr('sidebar.addGroup'), icon: 'folderPlus', action: () => addGroup('') },
            { label: tr('sidebar.exportConns'), icon: 'upload', action: () => exportGroup.set('') },
            { label: tr('sidebar.useDefaultsAll'), icon: 'key', action: () => setUseDefaults('', true) },
            { label: tr('sidebar.stopDefaultsAll'), icon: 'key', action: () => setUseDefaults('', false) },
            { separator: true, label: '' },
            { label: tr('sidebar.removeAll'), icon: 'trash', danger: true, action: () => removeGroup('') },
          ]
        : [
            { label: tr('sidebar.addSubgroup'), icon: 'folderPlus', action: () => addGroup(path) },
            { label: tr('sidebar.editName'), icon: 'edit', action: () => renameGroup(path) },
            { label: tr('sidebar.exportGroup'), icon: 'upload', action: () => exportGroup.set(path) },
            { label: tr('sidebar.useDefaults'), icon: 'key', action: () => setUseDefaults(path, true) },
            { label: tr('sidebar.stopDefaults'), icon: 'key', action: () => setUseDefaults(path, false) },
            { separator: true, label: '' },
            { label: tr('sidebar.removeGroup'), icon: 'trash', danger: true, action: () => removeGroup(path) },
          ]
    menu = { x: e.clientX, y: e.clientY, items }
  }

  // ---------- arrastrar ----------
  // Se arrastran grupos y servidores. Un grupo soltado en el centro de otro
  // entra en él; en su borde de arriba (o de abajo, si está plegado) queda
  // antes (o después) de él. Un servidor soltado sobre otro queda antes o
  // después de él; sobre un grupo, entra al final. "Sin grupo" es la raíz.
  // Con el mouse a mano (no HTML5 drag) como las pestañas: el arrastre nativo
  // choca con el de archivos de Wails.
  interface Press {
    kind: 'group' | 'conn' | 'site'
    id: string // ruta del grupo o id del servidor
    label: string
    fromFav?: boolean // arrancó en una fila de Favoritos
    x: number
    y: number
  }
  // Dónde cae: en el grupo `group` ("" = sin grupo), al final o antes/después
  // de la fila hermana `ref` (ruta del grupo o id del servidor).
  interface Target {
    group: string
    ref?: string
    side?: 'before' | 'after'
    fav?: boolean // dentro de Favoritos (ref = favorita hermana)
    site?: boolean // dentro de Sitios (ref = sitio hermano; sin ref, al final)
  }
  let press: Press | null = null
  let drag: (Press & { target: Target | null }) | null = null // target null: no se puede soltar acá

  // El fantasma que sigue al mouse es HTML: la vista nativa de un sitio (que se dibuja encima) se oculta mientras dura el arrastre.
  const holdDrag = overlayHolder()
  $: holdDrag(!!drag)
  onDestroy(() => holdDrag(false))

  function down(e: MouseEvent, kind: Press['kind'], id: string, label: string, fromFav = false) {
    if (e.button !== 0 || (e.target as HTMLElement).closest('button.icon')) return
    press = { kind, id, label, fromFav, x: e.clientX, y: e.clientY }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  function move(e: MouseEvent) {
    if (!press) return
    if (!drag && Math.hypot(e.clientX - press.x, e.clientY - press.y) < 6) return
    drag = { ...press, x: e.clientX, y: e.clientY, target: targetAt(e.clientX, e.clientY, press) }
  }

  function targetAt(x: number, y: number, p: Press): Target | null {
    const el = (document.elementFromPoint(x, y) as HTMLElement | null)?.closest<HTMLElement>(
      '[data-fav-row], [data-fav-zone], [data-site-row], [data-site-zone], [data-drop-group], [data-drop-block]',
    )
    if (!el || el.hasAttribute('data-drop-block')) return null
    const box = el.getBoundingClientRect()
    const f = (y - box.top) / box.height // 0 arriba, 1 abajo
    // Sitios: solo se reordenan entre sí, y nada más entra ahí.
    const siteRow = el.dataset.siteRow
    const inSites = siteRow !== undefined || el.hasAttribute('data-site-zone')
    if (p.kind === 'site') {
      if (!inSites || siteRow === p.id) return null
      return siteRow === undefined ? { group: '', site: true } : { group: '', site: true, ref: siteRow, side: f < 0.5 ? 'before' : 'after' }
    }
    if (inSites) return null
    // Favoritos: solo reciben conexiones, y lo que sale de ahí solo se reordena ahí.
    const favRow = el.dataset.favRow
    if (favRow !== undefined || el.hasAttribute('data-fav-zone')) {
      if (p.kind !== 'conn' || favRow === p.id) return null
      return favRow === undefined ? { group: '', fav: true } : { group: '', fav: true, ref: favRow, side: f < 0.5 ? 'before' : 'after' }
    }
    if (p.fromFav) return null
    const g = el.dataset.groupRow
    const id = el.dataset.connRow
    let t: Target = { group: el.dataset.dropGroup! }
    if (p.kind === 'group' && g !== undefined) {
      // "Después" de un grupo abierto se confundiría con entrar en él.
      if (f < 0.3) t = { group: parentOf(g), ref: g, side: 'before' }
      else if (f > 0.7 && isCollapsed(g)) t = { group: parentOf(g), ref: g, side: 'after' }
    } else if (p.kind === 'conn' && id !== undefined) {
      t = { group: t.group, ref: id, side: f < 0.5 ? 'before' : 'after' }
    }
    if (t.ref === p.id) return null // sobre sí mismo
    if (p.kind === 'group' && isWithin(t.group, p.id)) return null // adentro de sí mismo
    return t
  }

  /** El hermano que sigue a la fila `ref` (sin contar `skip`, lo que se arrastra), o "" si es el último. */
  function nextSibling(ref: string, skip: string): string {
    const i = rows.findIndex((r) => (r.kind === 'group' ? r.path : r.c.id) === ref)
    if (i < 0) return ''
    const depth = rows[i].depth
    const kind = rows[i].kind
    for (const r of rows.slice(i + 1)) {
      if (r.depth > depth) continue // adentro de un grupo hermano
      if (r.depth < depth || r.kind !== kind) return ''
      const key = r.kind === 'group' ? r.path : r.c.id
      if (key !== skip) return key
    }
    return ''
  }

  function up() {
    const d = drag
    press = drag = null
    window.removeEventListener('mousemove', move)
    window.removeEventListener('mouseup', up)
    if (!d) return
    justDragged = true
    setTimeout(() => (justDragged = false))
    if (d.target === null) return
    const t = d.target
    if (t.site) {
      const before = placeBeforeId(shownSites, d.id, t.ref, t.side)
      api
        .sitePlace(d.id, before)
        .then(reloadSites)
        .catch((e) => toast(errorText(e), 'error'))
      return
    }
    if (t.fav) {
      const i = favs.findIndex((c) => c.id === t.ref)
      const next = !t.ref ? '' : t.side === 'before' ? t.ref : (favs.slice(i + 1).find((c) => c.id !== d.id)?.id ?? '')
      run(async () => {
        if (!$connections.find((c) => c.id === d.id)?.favorite) await api.setFavorite(d.id, true)
        if (next || d.fromFav) await api.placeFavorite(d.id, next)
      }).then(() => expand(FAV_KEY))
      return
    }
    const before = !t.ref ? '' : t.side === 'before' ? t.ref : nextSibling(t.ref, d.id)
    run(() =>
      d.kind === 'conn'
        ? api.placeConnection(d.id, t.group, before)
        : api.placeGroup(d.id, t.group ? `${t.group}/${baseName(d.id)}` : baseName(d.id), before),
    ).then(() => expand(t.group))
  }
</script>

<aside>
  <!-- Una sola fila: menú, búsqueda, snippets, ajustes y nueva conexión. -->
  <header>
    <button class="icon" title={$tl('sidebar.hideTip', { keys: keys('Ctrl+Shift+B') })} aria-label={$tl('sidebar.hide')} on:click={() => sidebarOpen.set(false)}>
      <Icon name="menu" size={15} />
    </button>
    <div class="search">
      <Icon name="search" size={12} />
      <input aria-label={$tl('sidebar.searchLabel')} placeholder={$tl('sidebar.search')} bind:value={query} />
    </div>
    <button class="icon" title={$tl('sidebar.snippetsTip', { keys: keys('Ctrl+Shift+S') })} aria-label={$tl('sidebar.snippets')} on:click={openSnippets} on:contextmenu={snippetsMenu}>
      <Icon name="code" size={15} />
    </button>
    <button class="icon" title={$tl('sidebar.settingsTip', { keys: keys('Ctrl+,') })} aria-label={$tl('sidebar.settings')} on:click={() => openAppTab('settings')}>
      <Icon name="gear" size={15} />
    </button>
    <button class="icon add" title={$tl('sidebar.newConnTip', { keys: keys('Ctrl+Shift+N') })} aria-label={$tl('sidebar.newConn')} on:click={() => dispatch('new')}>
      <Icon name="plus" size={15} />
    </button>
  </header>

  <!-- "Este equipo" queda fijo arriba, siempre, aunque la lista de servidores se desplace. -->
  <div class="pinned" role="tree" aria-label={tabTitle($tl, { kind: 'local', title: '' })} tabindex="-1" use:treeNav>
    <div
      class="item local"
      role="treeitem"
      aria-level="1"
      aria-selected="false"
      tabindex="-1"
      on:keydown={(e) => e.target === e.currentTarget && e.key === 'Enter' && openLocal()}
      title={$tl('sidebar.localTip')}
      data-drop-block
      on:dblclick={() => openLocal()}
      on:contextmenu={(e) => (e.preventDefault(), shellMenu(e.clientX, e.clientY))}
    >
      <span class="ficon"><Icon name="pc" size={13} /></span>
      <span class="name">{tabTitle($tl, { kind: 'local', title: '' })}</span>
      <div class="actions">
        <button class="icon" tabindex="-1" title={$tl('sidebar.localNewTermTip')} on:click|stopPropagation={shellMenuFrom} aria-label={$tl('sidebar.localNewTerm')}>
          <Icon name="terminal" size={14} />
        </button>
        <button class="icon" tabindex="-1" title={$tl('sidebar.browserTip')} on:click|stopPropagation={() => openBrowser({ connId: LOCAL }, undefined)} aria-label={$tl('sidebar.browser')}>
          <Icon name="globe" size={14} />
        </button>
        <button class="icon" tabindex="-1" title={$tl('sidebar.openAnotherTab')} on:click={() => openLocal()} aria-label={$tl('sidebar.openAnotherTab')}><Icon name="plus" size={14} /></button>
      </div>
    </div>
    {#each $pinnedDirs as p, i (p)}
      <div
        class="subtab"
        role="treeitem"
        aria-level="2"
        aria-selected="false"
        tabindex="-1"
        title={$tl('sidebar.pinTip', { path: p })}
        data-drop-block
        on:click={() => openPinnedDir(p)}
        on:keydown={(e) => e.key === 'Enter' && openPinnedDir(p)}
        on:contextmenu={(e) => pinMenu(e, p)}
      >
        <span class="branch">{branch(i, localKids)}</span>
        <Icon name="pin" size={12} filled />
        <span class="label">{$pinNames[p] || dirLabel(p)}</span>
        <button class="icon close" tabindex="-1" title={$tl('sidebar.unpin')} on:click|stopPropagation={() => togglePinnedDir(p)} aria-label={$tl('sidebar.unpin')}>
          <Icon name="x" size={11} />
        </button>
      </div>
    {/each}
    {#each localTabs as t, i (t.id)}
      <div
        class="subtab"
        class:active={t.id === $activeTabId}
        role="treeitem"
        aria-level="2"
        aria-selected={t.id === $activeTabId}
        tabindex="-1"
        title={t.kind === 'editor' ? t.path : t.cwd !== '::drives' ? t.cwd : undefined}
        data-drop-block
        on:click={() => activeTabId.set(t.id)}
        on:keydown={(e) => e.key === 'Enter' && activeTabId.set(t.id)}
      >
        <span class="branch">{branch($pinnedDirs.length + i, localKids)}</span>
        <Icon name={t.kind === 'editor' || t.kind === 'terminal' || t.kind === 'browser' ? kindIcon[t.kind] : 'folder'} size={12} />
        <span class="label" class:mono={t.kind !== 'terminal'}>{tabLabel(t, localTabs, $tl)}</span>
        <button class="icon close" tabindex="-1" title={$tl('sidebar.closeTab')} on:click|stopPropagation={() => closeTab(t.id)} aria-label={$tl('sidebar.closeTab')}>
          <Icon name="x" size={11} />
        </button>
      </div>
    {/each}
  </div>
  <div class="divider" />

  <!-- Sitios web guardados: arriba de Workspaces. -->
  {#if sitesOn && (shownSites.length || !query.trim())}
    <div class="sites" role="tree" aria-label={$tl('sites.title')} tabindex="-1" use:treeNav>
      <div
        class="group top sitehead"
        class:dropping={drag?.target?.site && !drag.target.ref}
        title={$tl('sites.tip')}
        data-site-zone
        role="treeitem"
        aria-level="1"
        aria-expanded={!sitesCollapsed}
        aria-selected="false"
        tabindex="-1"
        on:click={() => (sitesCollapsed = !sitesCollapsed)}
        on:keydown={(e) => e.target === e.currentTarget && e.key === 'Enter' && (sitesCollapsed = !sitesCollapsed)}
      >
        <span class="chev">{sitesCollapsed ? '▸' : '▾'}</span>
        <span class="gname">{$tl('sites.title')}</span>
        <button class="icon" tabindex="-1" title={$tl('sites.newTab')} aria-label={$tl('sites.newTab')} on:click|stopPropagation={openBlankSite}>
          <Icon name="globe" size={12} />
        </button>
        <button class="icon" tabindex="-1" title={$tl('sites.add')} aria-label={$tl('sites.add')} on:click|stopPropagation={() => saveSite(undefined)}>
          <Icon name="plus" size={12} />
        </button>
      </div>
      {#if !sitesCollapsed || query.trim()}
        {#each shownSites as s (s.id)}
          <div
            class="item site"
            class:dragging={drag?.kind === 'site' && drag.id === s.id}
            class:drop-before={drag?.target?.site && drag.target.ref === s.id && drag.target.side === 'before'}
            class:drop-after={drag?.target?.site && drag.target.ref === s.id && drag.target.side === 'after'}
            role="treeitem"
            aria-level="2"
            aria-selected="false"
            tabindex="-1"
            title={s.url}
            data-site-row={s.id}
            on:mousedown={(e) => down(e, 'site', s.id, s.name)}
            on:click={() => !justDragged && openSite(s.url, s.name)}
            on:keydown={(e) => e.target === e.currentTarget && e.key === 'Enter' && openSite(s.url, s.name)}
            on:contextmenu={(e) => siteMenu(e, s)}
          >
            <span class="ficon"><Icon name="globe" size={13} /></span>
            <span class="name">{s.name}</span>
          </div>
        {/each}
      {/if}
    </div>
    <div class="divider" />
  {/if}

  <!-- Workspaces: pestañas y mosaico guardados con nombre. -->
  <div class="workspaces" data-drop-block role="tree" aria-label={$tl('sidebar.workspaces')} tabindex="-1" use:treeNav>
    <button class="group top wshead" role="treeitem" aria-level="1" aria-expanded={!wsCollapsed} aria-selected="false" tabindex="-1" title={$tl('sidebar.workspacesTip')} on:click={() => (wsCollapsed = !wsCollapsed)} on:contextmenu={(e) => wsMenu(e)}>
      <span class="chev">{wsCollapsed ? '▸' : '▾'}</span>
      <span class="gname">{$tl('sidebar.workspaces')}</span>
    </button>
    {#if !wsCollapsed}
      {#each $namedWorkspaces as w, i (w.id)}
        <div
          class="subtab"
          role="treeitem"
          aria-level="2"
          aria-selected="false"
          tabindex="-1"
          title={$tl('sidebar.workspaceTip', { name: w.name })}
          on:click={() => openNamedWorkspace(w)}
          on:keydown={(e) => e.key === 'Enter' && openNamedWorkspace(w)}
          on:contextmenu={(e) => wsMenu(e, w)}
        >
          <span class="branch">{branch(i, $namedWorkspaces.length + 1)}</span>
          <Icon name="splitRight" size={12} />
          <span class="label">{w.name}</span>
        </div>
      {/each}
      <div
        class="subtab add"
        role="treeitem"
        aria-level="2"
        aria-selected="false"
        tabindex="-1"
        title={$tl('sidebar.saveWorkspaceTip')}
        on:click={saveCurrentWorkspace}
        on:keydown={(e) => e.key === 'Enter' && saveCurrentWorkspace()}
        on:contextmenu={(e) => wsMenu(e)}
      >
        <span class="branch">└─</span>
        <Icon name="plus" size={12} />
        <span class="label">{$tl('sidebar.saveWorkspace')}</span>
      </div>
    {/if}
  </div>
  <div class="divider" />

  <div class="list" role="tree" aria-label={$tl('sidebar.servers')} tabindex="-1" data-drop-group="" use:treeNav on:contextmenu={listMenu}>
    {#if favs.length}
      <button
        class="group top"
        class:dropping={drag?.target?.fav && !drag.target.ref}
        title={$tl('sidebar.favoritesTip')}
        data-fav-zone
        role="treeitem"
        aria-level="1"
        aria-expanded={!isCollapsed(FAV_KEY)}
        aria-selected="false"
        tabindex="-1"
        on:click={() => toggle(FAV_KEY)}
      >
        <span class="chev">{isCollapsed(FAV_KEY) ? '▸' : '▾'}</span>
        <span class="gname">{$tl('sidebar.favorites')}</span>
      </button>
      {#if !isCollapsed(FAV_KEY)}
        {#each favs as c (c.id)}
          <ConnRow
            {c}
            fav
            connected={openConnIds.has(c.id)}
            dragging={drag?.kind === 'conn' && drag.id === c.id && !!drag.fromFav}
            dropBefore={!!drag?.target?.fav && drag.target.ref === c.id && drag.target.side === 'before'}
            dropAfter={!!drag?.target?.fav && drag.target.ref === c.id && drag.target.side === 'after'}
            on:mousedown={(e) => down(e, 'conn', c.id, c.name, true)}
            on:edit={(e) => dispatch('edit', e.detail)}
          />
        {/each}
      {/if}
      <div class="divider" />
    {:else if drag?.kind === 'conn'}
      <div class="rootdrop" class:dropping={drag.target?.fav} data-fav-zone>{$tl('sidebar.dropFavorites')}</div>
    {/if}
    {#each rows as r (r.key)}
      {#if r.kind === 'group'}
        <button
          class="group"
          class:top={r.depth === 0}
          class:dropping={!drag?.target?.fav && drag?.target?.group === r.path && !drag.target.ref}
          class:drop-before={!drag?.target?.fav && drag?.target?.ref === r.path && drag.target.side === 'before'}
          class:drop-after={!drag?.target?.fav && drag?.target?.ref === r.path && drag.target.side === 'after'}
          class:dragging={drag?.kind === 'group' && drag.id === r.path}
          style="--d: {r.depth}"
          title={$tl('sidebar.groupTip')}
          data-drop-group={r.path}
          data-group-row={r.path}
          role="treeitem"
          aria-level={r.depth + 1}
          aria-expanded={r.open}
          aria-selected="false"
          tabindex="-1"
          on:mousedown={(e) => down(e, 'group', r.path, r.name)}
          on:click={() => toggle(r.path)}
        >
          <span class="guides">{#each Array(r.depth) as _}<span class="guide" />{/each}</span>
          <span class="chev">{r.open ? '▾' : '▸'}</span>
          <span class="gname">{r.name}</span>
        </button>
      {:else}
        {@const c = r.c}
        <ConnRow
          {c}
          depth={r.depth}
          connected={openConnIds.has(c.id)}
          dragging={drag?.kind === 'conn' && drag.id === c.id}
          dropBefore={!drag?.target?.fav && drag?.target?.ref === c.id && drag.target.side === 'before'}
          dropAfter={!drag?.target?.fav && drag?.target?.ref === c.id && drag.target.side === 'after'}
          on:mousedown={(e) => down(e, 'conn', c.id, c.name)}
          on:edit={(e) => dispatch('edit', e.detail)}
        />
        {#each c.pins ?? [] as p, i (p.path)}
          <div
            class="subtab"
            role="treeitem"
            aria-level={r.depth + 2}
            aria-selected="false"
            tabindex="-1"
            style="--d: {r.depth}"
            title={$tl('sidebar.serverPinTip', { path: p.path })}
            data-drop-group={c.group}
            data-menu-pin={c.id}
            data-pin-path={p.path}
            on:click={() => openSftpAt(c, p.path)}
            on:keydown={(e) => e.key === 'Enter' && openSftpAt(c, p.path)}
          >
            <span class="guides">{#each Array(r.depth) as _}<span class="guide" />{/each}</span>
            <span class="branch">{branch(i, (c.pins?.length ?? 0) + (tabsByConn[c.id]?.length ?? 0))}</span>
            <Icon name="pin" size={12} filled />
            <span class="label">{p.name || dirLabel(p.path)}</span>
            <button class="icon close" tabindex="-1" title={$tl('sidebar.unpinFolder')} on:click|stopPropagation={() => unpin(c, p)} aria-label={$tl('sidebar.unpinFolder')}>
              <Icon name="x" size={11} />
            </button>
          </div>
        {/each}
        {#each tabsByConn[c.id] ?? [] as t, i (t.id)}
          {@const list = tabsByConn[c.id]}
          {@const np = c.pins?.length ?? 0}
          <div
            class="subtab"
            class:active={t.id === $activeTabId}
            class:down={t.status === 'closed' || t.status === 'error'}
            role="treeitem"
            aria-level={r.depth + 2}
            aria-selected={t.id === $activeTabId}
            tabindex="-1"
            style="--d: {r.depth}"
            title={t.kind === 'sftp' ? `SFTP · ${t.cwd ?? ''}` : t.kind === 'editor' ? t.path : tabLabel(t, list, $tl)}
            data-drop-group={c.group}
            data-menu-conn={c.id}
            on:click={() => activeTabId.set(t.id)}
            on:keydown={(e) => e.key === 'Enter' && activeTabId.set(t.id)}
          >
            <span class="guides">{#each Array(r.depth) as _}<span class="guide" />{/each}</span>
            <span class="branch">{branch(np + i, np + list.length)}</span>
            <Icon name={kindIcon[t.kind]} size={12} />
            <span class="label" class:mono={t.kind === 'sftp' || t.kind === 'local'}>{tabLabel(t, list, $tl)}</span>
            {#if t.kind === 'ports' && t.forwarding}
              <span class="live" title={$tl('sidebar.activeForwards')}>{t.forwarding}</span>
            {/if}
            <button class="icon close" tabindex="-1" title={$tl('sidebar.closeTab')} on:click|stopPropagation={() => closeTab(t.id)} aria-label={$tl('sidebar.closeTab')}>
              <Icon name="x" size={11} />
            </button>
          </div>
        {/each}
      {/if}
    {:else}
      <div class="empty">
        {#if query}
          {$tl('sidebar.noResults', { query })}
        {:else}
          <span class="mark"><LogoMark size={40} /></span>
          {$tl('sidebar.noServers')}
          <button class="primary" on:click={() => dispatch('new')}>{$tl('sidebar.addServer')}</button>
          <span class="tip">{$tl('sidebar.importTip')}</span>
        {/if}
      </div>
    {/each}
    {#if drag && drag.kind !== 'site'}
      <div class="rootdrop" class:dropping={!drag.target?.fav && drag.target?.group === '' && !drag.target.ref} data-drop-group="">{$tl('sidebar.noGroup')}</div>
    {/if}
  </div>
</aside>

{#if menu}
  <ContextMenu {...menu} on:close={() => (menu = null)} />
{/if}
{#if drag}
  <div class="ghost" style="left: {drag.x + 14}px; top: {drag.y + 14}px">{drag.label}</div>
{/if}

<style>
  aside {
    background: var(--bg-2);
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  /* Una fila: el botón de menú queda donde está en la franja de la barra contraída. */
  header {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 8px 8px 8px 6px;
    border-bottom: 1px solid var(--border);
  }
  header > .icon {
    padding: 4px 5px;
    color: var(--text-2);
  }
  header > .icon:hover {
    color: var(--text);
  }
  header > .add {
    color: var(--text);
  }
  .search {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 6px;
    background: var(--bg);
    border: 1px solid var(--border);
    padding: 0 8px;
    color: var(--text-3);
  }
  .search input {
    border: 0;
    background: transparent;
    flex: 1;
    min-width: 0;
    padding: 4px 0;
  }
  .search input:focus {
    outline: none;
  }
  .search:focus-within {
    border-color: var(--text-2);
  }
  .pinned {
    flex: none;
    max-height: 35%;
    overflow: auto;
    padding-top: 6px;
  }
  .sites {
    flex: none;
    max-height: 30%;
    overflow: auto;
    margin-top: 4px;
  }
  .workspaces {
    flex: none;
    max-height: 30%;
    overflow: auto;
    margin-top: 4px;
  }
  .subtab.add {
    color: var(--text-3);
  }
  .divider {
    flex: none;
    height: 1px;
    background: var(--border);
    margin: 6px 10px;
  }
  .list {
    flex: 1;
    overflow: auto;
    padding-bottom: 8px;
  }
  .ficon {
    display: inline-flex;
    color: var(--text-2);
  }
  /* Filas de una línea, como un árbol de archivos. */
  .group {
    display: flex;
    align-items: center;
    width: 100%;
    height: 24px;
    background: transparent;
    border: 0;
    padding: 0 10px 0 7px;
    color: var(--text-2);
    font-size: 12px;
    text-align: left;
  }
  /* Aire solo antes de cada grupo de primer nivel: adentro, las guías siguen sin cortes. */
  .group.top:not(:first-child) {
    margin-top: 4px;
  }
  /* Guías del árbol: una columna de 10px por nivel (= ▾ + su margen), con la línea justo al centro del ▾ de ese nivel. */
  .guides {
    display: flex;
    align-self: stretch;
    flex: none;
  }
  .guide {
    width: 10px;
    background: linear-gradient(var(--guide), var(--guide)) 4px 0 / 1px 100% no-repeat;
  }
  .group:hover {
    background: transparent !important;
    color: var(--text);
  }
  .chev {
    display: flex;
    justify-content: center;
    width: 9px;
    margin-right: 1px;
    flex: none;
    overflow: visible;
  }
  .gname {
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .item {
    position: relative;
    display: flex;
    align-items: center;
    height: 24px;
    /* El nombre queda alineado con el de su grupo; las guías marcan el nivel. */
    padding: 0 10px 0 7px;
    cursor: default;
    outline: none;
  }
  .item.local {
    height: 26px;
  }
  .item.local .ficon,
  .item.site .ficon {
    margin-right: 8px;
  }
  /* El encabezado de Sitios lleva el "+" a la derecha. */
  .sitehead .icon {
    margin-left: 6px;
    padding: 0 3px;
    color: var(--text-3);
  }
  .sitehead .icon:hover {
    color: var(--text);
  }
  .item:hover,
  .item:focus-visible {
    background: var(--bg-3);
  }
  /* Arrastrando: lo que se mueve queda atenuado y el grupo destino se marca. */
  .dragging {
    opacity: 0.45;
  }
  .group.dropping,
  .rootdrop.dropping {
    background: var(--bg-4) !important;
    color: var(--text);
    outline: 1px solid var(--text-2);
    outline-offset: -1px;
  }
  /* Ordenando: una línea marca dónde va a quedar. */
  .drop-before {
    box-shadow: inset 0 2px 0 var(--accent);
  }
  .drop-after {
    box-shadow: inset 0 -2px 0 var(--accent);
  }
  .rootdrop {
    margin: 8px 10px 0;
    padding: 6px 8px;
    border: 1px dashed var(--border);
    color: var(--text-3);
    font-size: 11px;
  }
  .ghost {
    position: fixed;
    z-index: 1200;
    pointer-events: none;
    padding: 2px 6px;
    background: var(--bg-4);
    border: 1px solid var(--border);
    white-space: nowrap;
  }
  /* Barra al borde de la fila: negra por defecto (o del color del servidor) y verde mientras hay una sesión abierta. */
  .name {
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* Al pasar el mouse, las acciones ocupan el lugar de la IP. */
  .actions {
    display: none;
    gap: 2px;
    margin-left: 8px;
  }
  .actions button.icon {
    padding: 0 4px;
  }
  .item:hover .actions,
  .item:focus-within .actions {
    display: flex;
  }
  /* Pestañas abiertas: ramas del árbol debajo de su servidor. */
  .subtab {
    display: flex;
    align-items: center;
    height: 22px;
    padding: 0 10px 0 7px;
    color: var(--text-2);
    cursor: default;
    outline: none;
  }
  .subtab:hover,
  .subtab:focus-visible {
    background: var(--bg-3);
  }
  .subtab.active {
    background: var(--bg-4);
    color: var(--text);
  }
  .branch {
    flex: none;
    margin-right: 6px;
    color: var(--guide);
    white-space: pre;
  }
  .subtab.active .branch {
    color: var(--guide-2);
  }
  .subtab .label {
    margin-left: 6px;
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* Puertos: cuántos reenvíos andan (verde). */
  .live {
    flex: none;
    margin-left: 6px;
    font-size: 10px;
    line-height: 15px;
    padding: 0 5px;
    background: var(--success);
    color: var(--bg);
  }
  .subtab .close {
    display: none;
    margin-left: 6px;
    padding: 0 3px;
  }
  .subtab:hover .close {
    display: inline-flex;
  }
  /* Sesión cerrada o con error: sin punto de estado, el texto en rojo. */
  .subtab.down {
    color: var(--danger);
  }
  .empty {
    padding: 24px 12px;
    color: var(--text-3);
    text-align: center;
    display: flex;
    flex-direction: column;
    gap: 12px;
    align-items: center;
  }
  .empty .mark {
    display: flex;
  }
  .empty .tip {
    font-size: 11px;
    line-height: 1.5;
  }
</style>
