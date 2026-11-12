import { writable, derived, get } from 'svelte/store'
import { api, errorText, type BridgeStatus, type Connection, type LocalShell, type Settings, type Site, type Transfer } from './api'
import { confirmDialog } from './dialog'
import { locale, resolveLanguage, tr, type Locale } from './i18n'
import { migrateDefaultShell } from './defaultshell'
import { siteHost } from './siteUrl'
import { activate, addTab, findPane, focusedPane, focusedTabs, layout, removeTab } from './layout'

// ---------- Conexiones ----------

export const connections = writable<Connection[]>([])
/** Grupos creados a mano (pueden estar vacíos). Los demás salen de `connections`. */
export const groupPaths = writable<string[]>([])

export async function reloadConnections() {
  const [conns, groups] = await Promise.all([api.listConnections(), api.listGroups()])
  connections.set(conns ?? [])
  groupPaths.set(groups ?? [])
}

// ---------- Pestañas ----------

export type TabKind =
  | 'terminal'
  | 'sftp'
  | 'editor'
  | 'rdp'
  | 'vnc'
  | 'local'
  | 'repo'
  | 'browser'
  | 'site'
  | 'ports'
  | 'settings'
  | 'import'
  | 'connection'
  | 'snippets'
  | 'whatsnew'

/** Ícono de cada tipo de pestaña (barra de pestañas, barra lateral y la pestaña flotante al arrastrarla). */
export const kindIcon: Record<TabKind, string> = {
  terminal: 'terminal',
  sftp: 'folder',
  editor: 'file',
  rdp: 'monitor',
  vnc: 'vnc',
  local: 'pc',
  repo: 'repo',
  browser: 'globe',
  site: 'globe',
  ports: 'ports',
  settings: 'gear',
  import: 'download',
  connection: 'server',
  snippets: 'code',
  whatsnew: 'star',
}

/** connId de las pestañas "Este equipo" (archivos de esta PC, sin servidor). */
export const LOCAL = 'local'

export interface Tab {
  id: string // id local de la pestaña (estable aunque se reconecte)
  kind: TabKind
  connId: string
  title: string
  color?: string
  /** Si se abrió desde otra pestaña, reutiliza su conexión SSH. */
  fromSession?: string
  /** Para terminales abiertas desde SFTP (y "Este equipo" duplicado), y SFTP restaurado: carpeta inicial. */
  initialDir?: string
  /** Para terminales de este equipo (connId LOCAL): id de la shell (powershell, cmd, wsl, gitbash...). */
  shell?: string
  /** Terminal compartida con Claude Code (puede leerla y escribir en ella). Vale solo para la sesión actual. */
  shared?: boolean
  /** Para SFTP y "Este equipo": carpeta que se está mostrando (la usa la barra lateral). */
  cwd?: string
  /** Para el editor: archivo remoto abierto y si tiene cambios sin guardar.
   * Para un repositorio: la carpeta raíz. Para el navegador: el archivo a mostrar (con initialDir como raíz del sitio). */
  path?: string
  /** Para el editor: el archivo se lee y se guarda como root, con sudo en el servidor. */
  sudo?: boolean
  /** Para el navegador: dirección con la que arranca. Para un sitio web: la dirección actual (se guarda en los workspaces). */
  url?: string
  dirty?: boolean
  /** Para los puertos: cuántos reenvíos están en marcha (cerrar la pestaña los detiene). */
  forwarding?: number
  /** Para el formulario de conexión: los datos con que arranca (con id si se edita una existente). */
  conn?: Partial<Connection>
  sessionId?: string
  status: 'connecting' | 'ready' | 'closed' | 'error'
  /** Restaurada de un espacio de trabajo: su vista se monta (y conecta) recién la primera vez que se muestra (App.svelte). */
  deferConnect?: boolean
}

let seq = 0
export const tabs = writable<Tab[]>([])

/** Pestaña activa del panel enfocado. set() la muestra en su panel y enfoca ese panel. */
export const activeTabId = {
  subscribe: derived([layout, focusedPane], ([$l, $f]) => findPane($l, $f)?.active ?? null).subscribe,
  set: (id: string | null) => id && activate(id),
}
export const activeTab = derived([tabs, activeTabId], ([$t, $a]) => $t.find((t) => t.id === $a) ?? null)

/** Estado que una vista embebida (terminal/editor del explorador) le pasa a la
 * pestaña que la reemplaza: la sesión viaja en Tab.sessionId y esto aparte. */
export interface Handoff {
  screen?: string // contenido de la terminal (addon-serialize)
  text?: string // texto del editor (puede tener cambios sin guardar)
  saved?: string // contenido que hay en el servidor
  save?: boolean // guardar apenas se abre (reintento como root de un guardado sin permiso)
}
export const handoffs = new Map<string, Handoff>()

export function openTab(t: Omit<Tab, 'id' | 'status'>, handoff?: Handoff) {
  const tab: Tab = { ...t, id: `tab-${++seq}`, status: 'connecting' }
  if (handoff) handoffs.set(tab.id, handoff)
  tabs.update((ts) => [...ts, tab])
  addTab(tab.id) // en el panel enfocado
  return tab.id
}

export function patchTab(id: string, patch: Partial<Tab>) {
  tabs.update((ts) => ts.map((t) => (t.id === id ? { ...t, ...patch } : t)))
}

export async function closeTab(id: string) {
  let t = get(tabs).find((x) => x.id === id)
  if (!t) return
  if (t.dirty) {
    activeTabId.set(id)
    const ok = await confirmDialog({
      title: tr('sftp.unsavedTitle'),
      message: tr('sftp.unsavedMsg', { path: t.path ?? t.title }),
      okText: tr('sftp.discardClose'),
      danger: true,
    })
    if (!ok) return
  }
  if (t.kind === 'ports' && t.forwarding) {
    activeTabId.set(id)
    const n = t.forwarding
    const ok = await confirmDialog({
      title: tr('stores.portsTitle'),
      message: tr(n === 1 ? 'stores.portsMsg.one' : 'stores.portsMsg.other', { count: n }),
      okText: tr('stores.portsOk'),
      danger: true,
    })
    if (!ok) return
  }
  // Releer: mientras se mostraba el diálogo las pestañas pudieron cambiar.
  t = get(tabs).find((x) => x.id === id)
  if (!t) return
  if (t.kind === 'ports') api.forwardStopAll(t.id).catch(() => {}) // los reenvíos que arrancó
  if (t.sessionId) {
    const close = t.kind === 'rdp' ? api.rdpClose : t.kind === 'vnc' ? api.vncClose : api.closeSession
    close(t.sessionId).catch(() => {})
  }
  if (t.kind === 'browser') api.webClose(t.id).catch(() => {}) // los sitios locales que levantó
  if (t.kind === 'site') api.browserClose(t.id).catch(() => {}) // su vista nativa
  removeTab(id) // del layout (el panel muestra la vecina, o desaparece si queda vacío)
  tabs.update((ts) => ts.filter((x) => x.id !== id))
}

/** Ctrl+Tab: siguiente / anterior pestaña del panel enfocado. */
export function cycleTab(dir: 1 | -1) {
  const ids = focusedTabs()
  if (!ids.length) return
  const idx = ids.indexOf(get(activeTabId) ?? '')
  activate(ids[(idx + dir + ids.length) % ids.length])
}

export function openForConnection(c: Connection, kind: TabKind) {
  // Las conexiones RDP y VNC solo tienen escritorio remoto.
  openTab({ kind: c.protocol === 'rdp' || c.protocol === 'vnc' ? c.protocol : kind, connId: c.id, title: c.name, color: c.color })
}

/** Abre una carpeta del servidor como un repositorio (árbol de archivos + editor), o activa la que ya está abierta. */
export function openRepo(from: Pick<Tab, 'connId' | 'color'>, sessionId: string | undefined, dir: string) {
  const existing = get(tabs).find((t) => t.kind === 'repo' && t.connId === from.connId && t.path === dir)
  if (existing) return activeTabId.set(existing.id)
  openTab({
    kind: 'repo',
    connId: from.connId,
    title: dir.replace(/\/+$/, '').split('/').pop() || '/',
    color: from.color,
    fromSession: sessionId,
    path: dir,
  })
}

/** Reenvío de puertos de un servidor: una sola pestaña por servidor (los reenvíos son suyos). */
export function openPorts(c: Pick<Connection, 'id' | 'name' | 'color'>, sessionId?: string) {
  const existing = get(tabs).find((t) => t.kind === 'ports' && t.connId === c.id)
  if (existing) return activeTabId.set(existing.id)
  openTab({ kind: 'ports', connId: c.id, title: c.name, color: c.color, fromSession: sessionId })
}

/** Pestañas de osat sin servidor (ajustes, importar, formulario de conexión): no se duplican ni se guardan en los espacios de trabajo. */
export const isAppTab = (t: Pick<Tab, 'kind'>) =>
  t.kind === 'settings' || t.kind === 'import' || t.kind === 'connection' || t.kind === 'snippets' || t.kind === 'whatsnew'

/** Ajustes, importar conexiones y Snippets: una sola pestaña de cada una. */
export function openAppTab(kind: 'settings' | 'import' | 'snippets') {
  const existing = get(tabs).find((t) => t.kind === kind)
  if (existing) return activeTabId.set(existing.id)
  openTab({ kind, connId: '', title: '' }) // el título se arma al mostrarlo (lib/tabtitle)
}

/** Formulario de una conexión nueva (una sola pestaña) o de editar una existente (una por conexión). */
export function openConnForm(conn: Partial<Connection> = {}) {
  const existing = get(tabs).find((t) => t.kind === 'connection' && (t.conn?.id ?? '') === (conn.id ?? ''))
  if (existing) return activeTabId.set(existing.id)
  openTab({ kind: 'connection', connId: '', title: conn.id ? (conn.name ?? '') : '', conn })
}

/** Navegador integrado. Con un servidor (connId), todo se ve desde ese servidor: localhost:3000 es el suyo.
 * file + root: archivo del servidor a mostrar y carpeta que se sirve como sitio (por defecto, la del archivo). */
export function openBrowser(
  from: Pick<Tab, 'connId' | 'color'>,
  sessionId: string | undefined,
  what: { url?: string; file?: string; root?: string } = {},
) {
  const file = what.file
  openTab({
    kind: 'browser',
    connId: from.connId,
    title: file ? (file.split('/').pop() ?? '') : '', // vacío = el título por defecto (lib/tabtitle)
    color: from.color,
    fromSession: sessionId,
    url: what.url,
    path: file,
    initialDir: what.root,
  })
}

/** Sitio web de internet en una pestaña, con el navegador nativo (SiteView). name: el título hasta que la página tenga el suyo. */
export function openSite(url: string, name?: string) {
  return openTab({ kind: 'site', connId: '', title: name || siteHost(url), url })
}

/** Pestaña de navegador vacía: la página se abre al escribir una dirección. */
export function openBlankSite() {
  return openTab({ kind: 'site', connId: '', title: tr('site.newTab'), url: '' })
}

/** Sitios web guardados (los que muestra la barra lateral; la estrella de SiteView los agrega y quita). */
export const sites = writable<Site[]>([])

/** Este sistema tiene navegador nativo para los sitios (lo dice Go al arrancar). */
export const browserSupported = writable(false)

export async function reloadSites() {
  sites.set((await api.sitesList()) ?? [])
}

// ---------- Barra lateral ----------

const SIDEBAR_KEY = 'osat.sidebarOpen'

/** La barra lateral de servidores se puede ocultar (botón de menú o Ctrl+B). */
export const sidebarOpen = writable<boolean>(
  (() => {
    try {
      return localStorage.getItem(SIDEBAR_KEY) !== '0'
    } catch {
      return true
    }
  })(),
)
sidebarOpen.subscribe((v) => {
  try {
    localStorage.setItem(SIDEBAR_KEY, v ? '1' : '0')
  } catch {
    // sin almacenamiento: vale hasta cerrar la app
  }
})

/** Abre un archivo remoto en una pestaña de editor, o activa la que ya lo tiene.
 * text: contenido ya leído (el editor lo muestra sin volver a pedirlo).
 * sudo: como root; el mismo archivo puede estar abierto normal y como root en pestañas distintas. */
export function openEditor(
  from: Pick<Tab, 'connId' | 'color'>,
  sessionId: string | undefined,
  path: string,
  text?: string,
  sudo = false,
) {
  const existing = get(tabs).find((t) => t.kind === 'editor' && t.connId === from.connId && t.path === path && !!t.sudo === sudo)
  if (existing) return activeTabId.set(existing.id)
  openTab(
    {
      kind: 'editor',
      connId: from.connId,
      title: path.split(/[\\/]/).pop() || path,
      color: from.color,
      fromSession: sessionId,
      path,
      sudo: sudo || undefined,
    },
    text === undefined ? undefined : { text, saved: text },
  )
}

// ---------- Transferencias ----------

export const transfers = writable<Record<string, Transfer>>({})
export const transfersOpen = writable(false)

export function upsertTransfer(t: Transfer) {
  transfers.update((m) => ({ ...m, [t.id]: t }))
}

export function clearFinishedTransfers() {
  transfers.update((m) => {
    const out: Record<string, Transfer> = {}
    for (const [k, v] of Object.entries(m)) if (v.status === 'running' || v.status === 'queued') out[k] = v
    return out
  })
  api.clearTransfers().catch(() => {})
}

// Notifica a las vistas SFTP que terminó una subida (para refrescar).
export const uploadFinished = writable<{ sessionId: string; at: number } | null>(null)
// Y a las de "Este equipo" que terminó una descarga (ruta local del resultado).
export const downloadFinished = writable<{ target: string; at: number } | null>(null)

/** Abre los archivos de esta PC en una pestaña. */
export function openLocal(initialDir?: string) {
  return openTab({ kind: 'local', connId: LOCAL, title: '', initialDir })
}

// "Acerca de osat" (se abre con el logo de la barra de título).
export const aboutOpen = writable(false)

// ---------- Ajustes ----------

/** Lo que se usa hasta que llegan los de settings.json (y fuera de la app). Refleja internal/settings.Defaults. */
const fallbackSettings: Settings = {
  terminal: {
    font: "'JetBrains Mono Variable', Consolas, monospace",
    fontSize: 13,
    scrollback: 10000,
    cursor: 'bar',
    cursorBlink: true,
    rightClick: 'paste',
    bell: 'none',
    theme: 'auto',
    copyOnSelect: true,
    lineHeight: 1.2,
    letterSpacing: 0,
    fontWeight: 400,
    fontWeightBold: 600,
    padding: 10,
    minContrast: 4.5,
    boldBright: false,
    cursorInactive: 'outline',
    pasteConfirm: false,
    scrollSpeed: 1,
    scrollOnInput: true,
    scrollOnOutput: false,
    wordSeparators: " ()[]{}',\"`",
    linkClick: 'click',
    keepAlive: 30,
    termType: 'xterm-256color',
    sessionLog: false,
    logDir: '',
  },
  files: { askDownloadDir: true, downloadDir: '', onConflict: 'ask' },
  general: { defaultShell: '', verboseLog: false, restoreSession: true, appearance: 'dark', language: 'system', lockIdleMinutes: 0, lockUseHello: true, webBrowser: true, autoUpdate: true, betaUpdates: false },
}

export const settings = writable<Settings>(fallbackSettings)

// El idioma de la interfaz sigue al ajuste (con "system", al idioma del navegador) y se le avisa a Go.
let appliedLocale: Locale | null = null
settings.subscribe((s) => {
  const loc = resolveLanguage(s.general.language, typeof navigator === 'undefined' ? 'es' : navigator.language)
  if (loc === appliedLocale) return
  appliedLocale = loc
  locale.set(loc)
  try {
    api.setLanguage(loc).catch(() => {})
  } catch {
    // fuera de la app no hay backend
  }
})

// Los cambios se guardan de a uno: cada uno parte de lo último que quedó guardado, así dos seguidos no se pisan.
let settingsChain: Promise<unknown> = Promise.resolve()

/** Guarda los ajustes que resultan de `change` y actualiza el store con lo que quedó guardado (normalizado). Avisa si falla. */
export function updateSettings(change: (s: Settings) => Settings): Promise<unknown> {
  settingsChain = settingsChain.then(async () => {
    try {
      settings.set(await api.settingsSave(change(get(settings))))
    } catch (e) {
      toast(errorText(e), 'error')
    }
  })
  return settingsChain
}

export async function loadSettings() {
  settings.set(await api.settingsGet())
  if (!get(settings).general.defaultShell) {
    // La terminal local por defecto era la "última usada" y vivía en el navegador: pasa a los ajustes, una sola vez.
    await loadLocalShells()
    await migrateDefaultShell({
      current: get(settings).general.defaultShell,
      shells: get(localShells),
      storage: localStorage,
      save: (id) => api.settingsSave({ ...get(settings), general: { ...get(settings).general, defaultShell: id } }).then((s) => settings.set(s)),
    }).catch(() => {}) // se reintenta la próxima vez que abra osat
  }
}

// Exportar conexiones a un .osat: el grupo que arranca marcado ("" = todas), null = cerrado.
export const exportGroup = writable<string | null>(null)

// Pedido de abrir el panel de snippets en una terminal (id de su pestaña, desde el menú de la pestaña):
// la terminal lo atiende y lo borra. null = ninguno.
export const snippetsRequest = writable<string | null>(null)
// Igual, para guardar lo que muestra la terminal como .txt.
export const saveTextRequest = writable<string | null>(null)

// Diálogo para elegir las terminales del broadcast (lib/broadcast.ts).
export const broadcastDialogOpen = writable(false)

// ---------- Claude Code ----------
// Puente (servidor MCP local) para que Claude Code lea y escriba en las
// terminales que se compartan, una por una.

export const bridge = writable<BridgeStatus>({ enabled: false, port: 0, preferredPort: 0, token: '', mode: 'ask', url: '', command: '', permissions: '' })

/** Sección de Configuración a desplegar y mostrar al abrirla (la consume SettingsView). */
export const settingsSection = writable<'claude' | null>(null)

export async function loadBridge() {
  bridge.set(await api.bridgeStatus())
}

/** Comparte la terminal de esa pestaña con Claude Code, o deja de compartirla. */
export async function toggleShare(tab: Tab) {
  if (!tab.sessionId) return
  const on = !tab.shared
  if (on && !get(bridge).enabled) {
    settingsSection.set('claude') // primero hay que encender el puente (Configuración → Claude MCP)
    openAppTab('settings')
    return
  }
  try {
    await api.termShare(tab.sessionId, on)
    patchTab(tab.id, { shared: on })
  } catch (e) {
    toast(errorText(e), 'error')
  }
}

// ---------- Terminales de este equipo ----------

export const localShells = writable<LocalShell[]>([])

export async function loadLocalShells() {
  localShells.set((await api.localShells().catch(() => [])) ?? [])
}

/** La terminal que se abre con "Abrir terminal aquí": la de los ajustes (PowerShell mientras no haya una elegida). */
export const defaultShell = derived(settings, (s) => s.general.defaultShell || 'powershell')

/** Abre una terminal de este equipo con esa shell (vacío = la predeterminada) en dir (vacío = carpeta personal). */
export function openLocalTerminal(shell: string, dir = '') {
  const id = shell || get(defaultShell)
  const found = get(localShells).find((s) => s.id === id)
  return openTab({ kind: 'terminal', connId: LOCAL, title: found?.name ?? 'Terminal', shell: id, initialDir: dir || undefined })
}

// ---------- Carpetas fijadas ----------
// Carpetas de esta PC con acceso rápido bajo "Este equipo" en la barra lateral.

const PINS_KEY = 'osat.pinnedDirs'

function loadPins(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(PINS_KEY) ?? '[]')
    return Array.isArray(v) ? v.filter((p): p is string => typeof p === 'string') : []
  } catch {
    return []
  }
}

export const pinnedDirs = writable<string[]>(loadPins())
pinnedDirs.subscribe((v) => {
  try {
    localStorage.setItem(PINS_KEY, JSON.stringify(v))
  } catch {
    // sin almacenamiento: las carpetas fijadas valen solo hasta cerrar la app
  }
})

// Nombre propio de una carpeta fijada (si no tiene, se muestra el de la carpeta).
const NAMES_KEY = 'osat.pinNames'

function loadNames(): Record<string, string> {
  try {
    const v = JSON.parse(localStorage.getItem(NAMES_KEY) ?? '{}')
    return v && typeof v === 'object' && !Array.isArray(v) ? v : {}
  } catch {
    return {}
  }
}

export const pinNames = writable<Record<string, string>>(loadNames())
pinNames.subscribe((v) => {
  try {
    localStorage.setItem(NAMES_KEY, JSON.stringify(v))
  } catch {
    // ídem pinnedDirs
  }
})

/** Le pone nombre a una carpeta fijada; vacío = vuelve al nombre de la carpeta. */
export function renamePinnedDir(path: string, name: string) {
  const { [path]: _old, ...rest } = get(pinNames)
  pinNames.set(name.trim() ? { ...rest, [path]: name.trim() } : rest)
}

/** Fija la carpeta, o la suelta (con su nombre) si ya estaba fijada. */
export function togglePinnedDir(path: string) {
  if (get(pinnedDirs).includes(path)) {
    pinnedDirs.update((l) => l.filter((p) => p !== path))
    renamePinnedDir(path, '')
  } else {
    pinnedDirs.update((l) => [...l, path])
  }
}

/** Abre el SFTP de la conexión en esa carpeta, o activa la pestaña SFTP que ya está ahí. */
export function openSftpAt(c: Pick<Connection, 'id' | 'name' | 'color'>, dir: string) {
  const open = get(tabs).find((t) => t.kind === 'sftp' && t.connId === c.id && t.cwd === dir)
  if (open) activeTabId.set(open.id)
  else openTab({ kind: 'sftp', connId: c.id, title: c.name, color: c.color, initialDir: dir })
}

/** ¿La carpeta del servidor está fijada en la barra lateral? */
export function isServerPinned(connId: string, dir: string) {
  return !!get(connections).find((c) => c.id === connId)?.pins?.some((p) => p.path === dir)
}

/** Fija la carpeta del servidor, o la suelta si ya estaba fijada. */
export async function toggleServerPin(connId: string, dir: string) {
  try {
    if (isServerPinned(connId, dir)) await api.unpinDir(connId, dir)
    else await api.pinDir(connId, dir)
    await reloadConnections()
  } catch (e) {
    toast(errorText(e), 'error')
  }
}

/** Pestaña "Este equipo" ya abierta en esa carpeta, o una nueva. */
export function openPinnedDir(path: string) {
  const open = get(tabs).find((t) => t.kind === 'local' && t.cwd === path)
  if (open) activeTabId.set(open.id)
  else openLocal(path)
}

// ---------- Drag & drop desde el SO ----------

/** La vista SFTP activa registra aquí su handler de archivos soltados. */
export const dropHandlers = new Map<string, (paths: string[]) => void>()

// ---------- Toasts ----------

export interface Toast {
  id: number
  kind: 'info' | 'error' | 'success'
  text: string
}
export const toasts = writable<Toast[]>([])
let toastSeq = 0
export function toast(text: string, kind: Toast['kind'] = 'info', ms = 4000) {
  const id = ++toastSeq
  toasts.update((t) => [...t, { id, kind, text }])
  setTimeout(() => toasts.update((t) => t.filter((x) => x.id !== id)), ms)
}
