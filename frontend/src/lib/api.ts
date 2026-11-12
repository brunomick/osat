// Capa fina sobre los bindings de Wails (window.go.main.App) y el runtime
// (window.runtime). La usamos en lugar de los archivos autogenerados en
// /wailsjs para tener tipos propios y no depender de la generación.

import { hasKey, tr } from './i18n'
import type { TermThemeSetting, UiTheme } from './termthemes'

export type AuthType = 'password' | 'key' | 'agent'
export type Protocol = 'ssh' | 'rdp' | 'vnc'

export interface PinnedDir {
  path: string
  /** Vacío = se muestra la ruta. */
  name?: string
}

export interface Connection {
  id: string
  name: string
  group: string
  protocol?: Protocol // ausente = ssh
  host: string
  port: number
  user: string
  domain?: string // solo RDP
  gateway?: string // solo RDP: RD Gateway ("host" o "host:puerto")
  authType: AuthType
  keyPath?: string
  /** Conexión SSH a través de la cual se llega (ProxyJump). Solo SSH. */
  jumpId?: string
  color?: string
  /** Solo SSH: reenviar el agente SSH a las terminales de esta conexión. */
  forwardAgent?: boolean
  /** Solo SSH: comando (una línea) que se escribe en el shell al abrir la terminal. */
  initialCommand?: string
  /** Solo SSH: variables de entorno "NOMBRE=valor". */
  env?: string[]
  /** Carpetas del servidor fijadas en la barra lateral. */
  pins?: PinnedDir[]
  favorite?: boolean
  favOrder?: number
  /** Usa el usuario y la contraseña por defecto (Configuración) en vez de los suyos. */
  useDefaultCreds?: boolean
  hasSecret: boolean
  createdAt?: number
  updatedAt?: number
}

export interface ConnectionInput extends Connection {
  secret: string
  keepSecret: boolean
}

/** Credenciales por defecto: las usan las conexiones marcadas y, si les falta, las que no tienen usuario o contraseña. */
export interface DefaultCreds {
  user: string
  /** Solo RDP. */
  domain?: string
  hasSecret: boolean
}

export interface DefaultCredsInput extends DefaultCreds {
  secret: string
  keepSecret: boolean
}

/** Estado del puente con Claude Code (servidor MCP local). */
export interface BridgeStatus {
  enabled: boolean
  port: number
  /** El puerto de siempre; si `port` es otro, estaba ocupado y se usó uno distinto. */
  preferredPort: number
  token: string
  /** "ask": lo que solo lee corre sin preguntar y lo demás pide permiso; "auto": sin permisos, Claude corre lo que quiera. */
  mode: BridgeMode
  url: string
  /** Comando para agregarlo a Claude Code. */
  command: string
  /** Reglas de permisos (JSON) para settings.json de Claude Code, según el modo. */
  permissions: string
}

export type BridgeMode = 'ask' | 'auto'

/** Ajustes de osat (settings.json). Los valores válidos y por defecto están en internal/settings. */
export interface Settings {
  terminal: {
    /** Pila CSS de fuentes. */
    font: string
    fontSize: number
    /** Líneas de historial. */
    scrollback: number
    cursor: 'bar' | 'block' | 'underline'
    cursorBlink: boolean
    rightClick: 'paste' | 'menu'
    bell: 'none' | 'visual' | 'sound'
    /** Colores de la terminal (ver lib/termthemes). */
    theme: TermThemeSetting
    /** Lo que se selecciona con el mouse se copia solo al portapapeles (como PuTTY). */
    copyOnSelect: boolean
    /** Alto de línea, en veces el tamaño de letra (1 a 2). */
    lineHeight: number
    /** Píxeles extra entre letras (0 a 5). */
    letterSpacing: number
    fontWeight: number
    fontWeightBold: number
    /** Margen entre el texto y el borde, en píxeles. */
    padding: number
    /** Contraste mínimo de los colores (1 = sin corregir). */
    minContrast: number
    boldBright: boolean
    cursorInactive: 'outline' | 'block' | 'bar' | 'underline' | 'none'
    /** Pegar varias líneas pide confirmación. */
    pasteConfirm: boolean
    /** Líneas por paso de la rueda. */
    scrollSpeed: number
    scrollOnInput: boolean
    scrollOnOutput: boolean
    /** Lo que corta una palabra al seleccionarla con doble clic. */
    wordSeparators: string
    linkClick: 'click' | 'ctrl'
    /** Segundos entre keepalives (conexiones nuevas). */
    keepAlive: number
    termType: 'xterm-256color' | 'xterm' | 'vt100' | 'linux'
    /** Guardar lo que muestra cada terminal en un archivo de logDir. */
    sessionLog: boolean
    logDir: string
  }
  files: {
    askDownloadDir: boolean
    downloadDir: string
    onConflict: 'ask' | 'overwrite' | 'skip'
  }
  general: {
    defaultShell: string
    verboseLog: boolean
    /** Al abrir osat vuelven las pestañas y el mosaico de la última vez. */
    restoreSession: boolean
    /** Tema de la interfaz. */
    appearance: 'system' | UiTheme
    /** Idioma de la interfaz. */
    language: 'system' | 'es' | 'en'
    /** Minutos sin usar osat para que se bloquee (0 = nunca). Solo vale con el bloqueo activado. */
    lockIdleMinutes: number
    /** Permite desbloquear con Windows Hello (solo cuenta si la PC lo tiene). */
    lockUseHello: boolean
    /** Sección Sitios y pestañas de navegador nativo (donde el sistema lo soporta). */
    webBrowser: boolean
    /** Al abrir osat, buscar si hay una versión nueva (y ofrecer instalarla). */
    autoUpdate: boolean
  }
}

/** Lo que se sabe de la última versión publicada (app_update.go). */
export interface UpdateInfo {
  current: string
  latest: string
  /** latest es más nueva que la que corre. */
  available: boolean
  /** Qué trae (markdown). */
  notes: string
  pageUrl: string
  /** osat se puede actualizar solo en este sistema. */
  canApply: boolean
  /** Si no se puede, por qué. */
  manual: string
}

/** Novedades de la versión que se acaba de instalar (vacío si no hay que mostrar nada). */
export interface WhatsNew {
  version: string
  notes: string
}

/** Estado del bloqueo con contraseña (app_lock.go). */
export interface LockState {
  /** Hay una contraseña definida. */
  enabled: boolean
  locked: boolean
  helloAvailable: boolean
  /** Hello disponible y permitido en Ajustes. */
  helloEnabled: boolean
}

/** Una terminal que se puede abrir en este equipo. */
export interface LocalShell {
  id: string
  name: string
}

/** Sitio local (127.0.0.1) del navegador integrado. url no termina en barra. */
export interface WebSite {
  id: string
  url: string
}

/** Sitio web guardado (sección Sitios de la barra lateral). url es http(s). */
export interface Site {
  id: string
  name: string
  url: string
  order: number
}

/** Rectángulo de la vista nativa de un sitio: píxeles CSS, relativo al área cliente de la ventana. */
export interface BrowserBounds {
  x: number
  y: number
  w: number
  h: number
}

/** Evento "browser:event" de la vista nativa de un sitio. Todos traen el estado actual; kind dice qué cambió. */
export interface BrowserEvent {
  id: string
  kind: 'nav' | 'title' | 'newWindow' | 'error'
  url: string
  title: string
  canBack: boolean
  canFwd: boolean
}

export type ImportSource = 'putty' | 'winscp' | 'mremoteng' | 'openssh' | 'osat'

/** Sesión encontrada en PuTTY / WinSCP / mRemoteNG, lista para importar. */
export interface ImportItem {
  name: string
  group: string
  protocol: Protocol
  host: string
  port: number
  user: string
  domain?: string
  gateway?: string
  authType: AuthType
  keyPath?: string
  /** Nombre de la conexión de salto (ProxyJump), que se enlaza al importar. */
  jump?: string
  forwardAgent?: boolean
  initialCommand?: string
  env?: string[]
  /** Ya hay una conexión con el mismo protocolo, host, puerto y usuario. */
  exists: boolean
  /** Solo de un .osat: posición en el archivo y si trae contraseña y clave. */
  ref: number
  hasSecret?: boolean
  hasKey?: boolean
}

export interface ImportResult {
  source: ImportSource
  location: string
  items: ImportItem[]
  /** Sesiones que no se pueden importar, con el motivo. */
  skipped: string[]
  /** La herramienta no tiene sesiones en este equipo (se puede elegir un archivo). */
  notFound: boolean
  /** Snippets que trae el archivo (solo en un .osat). */
  snippets: number
  /** Sitios web que trae el archivo (solo en un .osat). */
  sites: number
  /** El archivo trae credenciales por defecto, y su usuario (solo en un .osat). */
  hasDefaults?: boolean
  defaultsUser?: string
}

export interface ConnectRequest {
  connectionId: string
  secret: string
  saveSecret: boolean
  cols: number
  rows: number
  /** Contraseñas de los saltos pedidas al conectar, por id de conexión. */
  jumpSecrets?: Record<string, { secret: string; save: boolean }>
  /** Reconexión automática: el backend no pregunta nada (p. ej. un OTP) y corta con NEED_SECRET. */
  noPrompt?: boolean
  /** La terminal reconecta sobre lo que ya hay en pantalla: el arranque limpio no la borra. */
  reconnect?: boolean
}

export interface SessionInfo {
  id: string
  kind: 'terminal' | 'sftp' | 'tunnel'
  connId: string
  title: string
  home?: string
}

/** Reenvío de puerto guardado: un puerto de esta PC (solo loopback) que llega a un puerto del servidor
 * (host, "localhost" por defecto) o a un pod/servicio de Kubernetes (namespace + resource, con el kubectl del servidor). */
export interface Forward {
  id: string
  connId: string
  name: string
  /** Ausente = local. Remoto: un puerto del servidor (port) que llega a host:localPort de esta PC. */
  kind?: '' | 'remote' | 'dynamic'
  localPort: number
  host?: string
  port: number
  /** Solo remoto: el servidor escucha en todas sus interfaces (si tiene GatewayPorts). */
  bindAll?: boolean
  namespace?: string
  resource?: string // "pod/x" | "svc/x" | "deployment/x"
  autoStart?: boolean
}

/** Comando guardado para mandarlo a una terminal. Puede llevar variables {{nombre}}. */
export interface Snippet {
  id: string
  name: string
  /** Carpeta "A/B" para ordenarlos en el panel ("" = sin carpeta). */
  folder: string
  command: string
  /** paste: se escribe sin Enter; run: se escribe con Enter. */
  mode: 'paste' | 'run'
  /** Grupo de conexiones donde aparece, con sus subgrupos ("" = global). */
  group: string
}

/** Espacio de trabajo con nombre. `data` lo define y lo lee el frontend (lib/workspace.ts). */
export interface NamedWorkspace {
  id: string
  name: string
  data: unknown
}

/** Un reenvío en marcha. Los detenidos no figuran. */
export interface ForwardState {
  id: string
  status: 'starting' | 'running' | 'error'
  error?: string
  /** En marcha, pero el último intento no llegó al destino (p. ej. no hay nada escuchando ahí). */
  warning?: string
  localPort: number
  active: number
  total: number
  bytesIn: number
  bytesOut: number
}

export interface K8sNamespaces {
  names: string[]
  current: string
}

export interface K8sItem {
  kind: 'pod' | 'svc'
  name: string
  status: string
  ports: { port: number; name?: string }[]
}

export interface FileEntry {
  name: string
  path: string
  size: number
  mode: string
  isDir: boolean
  isLink: boolean
  modTime: number
}

export interface Listing {
  path: string
  entries: FileEntry[]
}

/** Carpeta de esta PC (panel "Este equipo"). path "::drives" = lista de unidades. */
export interface LocalListing extends Listing {
  parent: string // "" si no hay nivel superior
  crumbs: { name: string; path: string }[]
}

export interface Transfer {
  id: string
  sessionId: string
  /** 'both': una sincronización en las dos direcciones. */
  direction: 'up' | 'down' | 'both'
  name: string
  source: string
  target: string
  total: number
  done: number
  status: 'queued' | 'running' | 'done' | 'error' | 'canceled'
  error?: string
  started: number
  connId: string
  /** Falló por un corte de conexión y quedó el pedazo: Reintentar sigue desde resumeFrom (en subidas, como mínimo). */
  resumable?: boolean
  resumeFrom?: number
  /** Aviso del último intento (p. ej. que se empezó de cero). */
  warning?: string
}

export type ConflictPolicy = 'ask' | 'overwrite' | 'skip'

/** Lo elegido en el diálogo de sincronizar. exclude: patrones ya separados. */
export interface SyncRequest {
  remoteDir: string
  localDir: string
  direction: 'up' | 'down' | 'both'
  mode: 'sync' | 'mirror'
  compare: 'time+size' | 'size' | 'content'
  delete: boolean
  exclude: string[]
}

/** Una acción del plan de sincronización. rel: ruta relativa (con "/"). */
export interface SyncAction {
  rel: string
  op: 'upload' | 'download' | 'deleteLocal' | 'deleteRemote' | 'mkdirLocal' | 'mkdirRemote' | 'skip'
  reason: string
  size: number
}

/** Vista previa de una sincronización, con las carpetas reales que se compararon. */
export interface SyncPreview {
  planId: string
  remoteDir: string
  localDir: string
  actions: SyncAction[]
}

/** Lo que llega en "search:done:<id>" al terminar (o detener) una búsqueda de archivos. */
export interface SearchDone {
  count: number
  /** Había más que el máximo de resultados. */
  truncated: boolean
  error?: string
  /** Se pidió buscar por contenido y el servidor no puede: se buscó solo por nombre. */
  contentUnavailable?: boolean
}

/** Qué puede hacer la búsqueda en un servidor. note: qué falta, listo para mostrar. */
export interface SearchCaps {
  content: boolean
  note?: string
}

/** Lo que IronRDP necesita para conectarse por el proxy local (RdpOpen). */
export interface RdpSession {
  id: string
  proxyUrl: string
  token: string
  destination: string
  user: string
  domain: string
  password: string
}

/** Lo que noVNC necesita para conectarse por el proxy local (VncOpen). */
export interface VncSession {
  id: string
  url: string // ws://127.0.0.1:<puerto>/<token>
  password: string // vacía si la conexión no tiene
}

/** Pregunta keyboard-interactive del servidor (OTP, 2FA, etc.). */
export interface AuthPrompt {
  id: string
  host: string
  name: string
  instruction: string
  questions: { text: string; echo: boolean }[]
}

export interface OsatError {
  code:
    | 'NEED_SECRET'
    | 'SERVER_GONE'
    | 'AUTH_FAILED'
    | 'HOSTKEY_UNKNOWN'
    | 'HOSTKEY_MISMATCH'
    | 'CONFLICT'
    | 'CANCELLED'
    | 'NO_TRASH'
    | 'GATEWAY'
    | 'RDP_SERVER'
    | 'RDPCERT_UNKNOWN'
    | 'RDPCERT_MISMATCH'
    | 'PERMISSION'
    | 'SUDO_PASSWORD'
    | 'SUDO_BAD_PASSWORD'
    | 'LOCK_WRONG_PASSWORD'
    | 'LOCK_TOO_SHORT'
    | 'LOCK_STORE_UNAVAILABLE'
    | 'LOCK_NOT_ENABLED'
  message: string
  data?: Record<string, string>
}

declare global {
  interface Window {
    go: any
    runtime: any
  }
}

const app = () => {
  const a = window.go?.main?.App
  if (!a) throw new Error(tr('api.noBackend'))
  return a
}

/** Interpreta errores "OSAT:{json}" emitidos por el backend. */
export function parseError(e: unknown): OsatError | null {
  const s = typeof e === 'string' ? e : (e as any)?.message ?? String(e)
  const prefix = 'OSAT:'
  const i = s.indexOf(prefix)
  if (i < 0) return null
  try {
    return JSON.parse(s.slice(i + prefix.length))
  } catch {
    return null
  }
}

/** Se cortó la conexión con el servidor (código SERVER_GONE del backend). */
export const isServerGone = (e: unknown) => parseError(e)?.code === 'SERVER_GONE'

/**
 * El texto de un error con código en el idioma elegido. La clave sale del código y de data.kind (y data.reason):
 * err.CODE.kind.reason, err.CODE.kind, err.CODE.kind.default o err.CODE.default. Si no hay ninguna, queda el mensaje que mandó Go.
 * Con data.jump (error de un salto) se antepone «Salto …:».
 */
export function codedText(k: OsatError): string {
  const d = k.data ?? {}
  const base = `err.${k.code}`
  const key = [
    d.kind && d.reason && `${base}.${d.kind}.${d.reason}`,
    d.kind && `${base}.${d.kind}`,
    d.kind && `${base}.${d.kind}.default`,
    `${base}.default`,
  ].find((c) => c && hasKey(c))
  if (!key) return k.message
  const text = tr(key, d)
  return d.jump ? tr('err.jump', { name: d.jump, text }) : text
}

export function errorText(e: unknown): string {
  const k = parseError(e)
  if (k) return codedText(k)
  return typeof e === 'string' ? e : (e as any)?.message ?? String(e)
}

export const api = {
  listConnections: (): Promise<Connection[]> => app().ListConnections(),
  saveConnection: (c: ConnectionInput): Promise<Connection> => app().SaveConnection(c),
  deleteConnection: (id: string): Promise<void> => app().DeleteConnection(id),
  /** Grupos creados a mano (rutas "A/B"); los que tienen servidores salen de listConnections. */
  listGroups: (): Promise<string[]> => app().ListGroups(),
  addGroup: (path: string): Promise<void> => app().AddGroup(path),
  /** Mueve o renombra un grupo (y todo lo que tiene adentro) a la ruta `to`. */
  moveGroup: (from: string, to: string): Promise<void> => app().MoveGroup(from, to),
  /** Elimina el grupo con sus subgrupos y conexiones (y sus contraseñas y reenvíos); "" = todo. Devuelve cuántas conexiones borró. */
  deleteConnectionsIn: (group: string): Promise<number> => app().DeleteConnectionsIn(group),
  /** group vacío = sin grupo. */
  moveConnection: (id: string, group: string): Promise<void> => app().MoveConnection(id, group),
  /** Mueve el grupo `from` a `to` y lo deja antes de su hermano `before` ("" = al final de su nivel). */
  placeGroup: (from: string, to: string, before: string): Promise<void> => app().PlaceGroup(from, to, before),
  /** Pasa la conexión a `group` y la deja antes de la conexión `before` ("" = al final). */
  placeConnection: (id: string, group: string, before: string): Promise<void> => app().PlaceConnection(id, group, before),
  pinDir: (connId: string, path: string): Promise<void> => app().PinDir(connId, path),
  unpinDir: (connId: string, path: string): Promise<void> => app().UnpinDir(connId, path),
  /** name vacío = se muestra la ruta. */
  renamePin: (connId: string, path: string, name: string): Promise<void> => app().RenamePin(connId, path, name),
  setFavorite: (connId: string, fav: boolean): Promise<void> => app().SetFavorite(connId, fav),
  /** Deja la favorita antes de `beforeId` ("" = al final de Favoritos). */
  placeFavorite: (connId: string, beforeId: string): Promise<void> => app().PlaceFavorite(connId, beforeId),
  defaultCredsGet: (): Promise<DefaultCreds> => app().DefaultCredsGet(),
  defaultCredsSave: (d: DefaultCredsInput): Promise<DefaultCreds> => app().DefaultCredsSave(d),
  /** Marca o desmarca las conexiones del grupo y sus subgrupos ("" = todas). Devuelve cuántas cambiaron. */
  setUseDefaultCreds: (group: string, on: boolean): Promise<number> => app().SetUseDefaultCreds(group, on),
  /** current: ruta actual (el diálogo arranca en su carpeta). */
  pickKeyFile: (current: string): Promise<string> => app().PickKeyFile(current),
  /** path vacío = la ubicación habitual en este equipo; si no, un archivo exportado. master: contraseña maestra de mRemoteNG o WinSCP. */
  importScan: (source: ImportSource, path: string, master = ''): Promise<ImportResult> => app().ImportScan(source, path, master),
  /** Con las contraseñas del último importScan. Devuelve cuántas se guardaron. */
  importConnections: (items: ImportItem[]): Promise<number> => app().ImportConnections(items),
  pickImportFile: (source: ImportSource): Promise<string> => app().PickImportFile(source),
  /** Pregunta dónde guardar y exporta las conexiones elegidas (y, si se pide, todos los snippets y sitios y las credenciales por defecto). Devuelve la ruta ("" = cancelado). */
  /** Guarda texto en un .txt que elige el usuario; devuelve la ruta, o "" si canceló. */
  saveText: (defaultName: string, text: string): Promise<string> => app().SaveText(defaultName, text),
  /** lang: idioma de las notas ('es' o 'en'). */
  updateCheck: (lang: string): Promise<UpdateInfo> => app().UpdateCheck(lang),
  /** Baja e instala la versión que encontró updateCheck (avance: evento update:progress); después osat se reinicia. */
  updateApply: (): Promise<void> => app().UpdateApply(),
  updateOpenPage: (): Promise<void> => app().UpdateOpenPage(),
  whatsNew: (lang: string): Promise<WhatsNew> => app().WhatsNew(lang),
  exportConnections: (ids: string[], password: string, withSecrets: boolean, withSnippets: boolean, withSites: boolean, withDefaults: boolean): Promise<string> =>
    app().ExportConnections(ids, password, withSecrets, withSnippets, withSites, withDefaults),
  importOsatScan: (path: string, password: string): Promise<ImportResult> => app().ImportOsatScan(path, password),
  /** Da de alta las elegidas del último .osat abierto, con contraseñas y claves. */
  importOsat: (items: ImportItem[]): Promise<number> => app().ImportOsat(items),
  /** Reemplaza las credenciales por defecto por las del último .osat abierto. */
  importOsatDefaults: (): Promise<void> => app().ImportOsatDefaults(),

  settingsGet: (): Promise<Settings> => app().SettingsGet(),
  /** Guarda y devuelve lo que quedó guardado (lo fuera de rango vuelve a su valor por defecto). */
  settingsSave: (s: Settings): Promise<Settings> => app().SettingsSave(s),
  /** Fondo (y, en Windows, tema) de la ventana: 'dark' o 'light' ya resueltos. */
  setWindowTheme: (theme: string): Promise<void> => app().SetWindowTheme(theme),
  setLanguage: (lang: string): Promise<void> => app().SetLanguage(lang),
  settingsDefaults: (): Promise<Settings> => app().SettingsDefaults(),
  /** Manda un error de JavaScript que nadie atrapó al log de osat (Go lo limita a 50 por minuto). */
  logFrontendError: (message: string, stack: string): Promise<void> => app().LogFrontendError(message, stack),

  lockStatus: (): Promise<LockState> => app().LockStatus(),
  /** current: la contraseña actual (vacía si el bloqueo todavía no estaba activado). */
  lockSetPassword: (current: string, next: string): Promise<void> => app().LockSetPassword(current, next),
  lockDisable: (current: string): Promise<void> => app().LockDisable(current),
  lockUnlock: (password: string): Promise<void> => app().LockUnlock(password),
  /** No da error si se cancela o Hello no está: después hay que consultar lockStatus para saber si quedó desbloqueado. */
  lockUnlockHello: (): Promise<void> => app().LockUnlockHello(),
  /** La ventana de osat está al frente y no minimizada (recién ahí se lanza Hello). */
  lockWindowReady: (): Promise<boolean> => app().LockWindowReady(),
  lockNow: (): Promise<void> => app().LockNow(),

  trustHostKey: (host: string): Promise<void> => app().TrustHostKey(host),
  forgetHostKey: (host: string): Promise<void> => app().ForgetHostKey(host),

  openTerminal: (r: ConnectRequest): Promise<SessionInfo> => app().OpenTerminal(r),
  bridgeStatus: (): Promise<BridgeStatus> => app().BridgeStatus(),
  bridgeSetEnabled: (on: boolean): Promise<BridgeStatus> => app().BridgeSetEnabled(on),
  /** Cambia el modo de permisos; las reglas del panel siguen al modo. */
  bridgeSetMode: (mode: BridgeMode): Promise<BridgeStatus> => app().BridgeSetMode(mode),
  /** El token anterior deja de valer. */
  bridgeNewToken: (): Promise<BridgeStatus> => app().BridgeNewToken(),
  /** Comparte la terminal con Claude Code (necesita el puente encendido), o deja de compartirla. */
  termShare: (id: string, on: boolean): Promise<void> => app().TermShare(id, on),
  /** Terminales que se pueden abrir en este equipo (PowerShell, cmd, WSL, Git Bash...). */
  localShells: (): Promise<LocalShell[]> => app().LocalShells(),
  /** shell "" = la predeterminada; dir vacío = la carpeta personal. */
  openLocalTerminal: (shell: string, dir: string, cols: number, rows: number): Promise<SessionInfo> =>
    app().OpenLocalTerminal(shell, dir, cols, rows),
  openSFTP: (r: ConnectRequest): Promise<SessionInfo> => app().OpenSFTP(r),
  openSFTPFrom: (sid: string): Promise<SessionInfo> => app().OpenSFTPFrom(sid),
  openTerminalFrom: (sid: string, cols: number, rows: number): Promise<SessionInfo> =>
    app().OpenTerminalFrom(sid, cols, rows),
  closeSession: (id: string): Promise<void> => app().CloseSession(id),

  termAttach: (id: string): Promise<void> => app().TermAttach(id),
  termWrite: (id: string, data: string): Promise<void> => app().TermWrite(id, data),
  termResize: (id: string, cols: number, rows: number): Promise<void> => app().TermResize(id, cols, rows),

  sftpList: (id: string, dir: string): Promise<Listing> => app().SftpList(id, dir),
  sftpMkdir: (id: string, p: string): Promise<void> => app().SftpMkdir(id, p),
  sftpRename: (id: string, from: string, to: string): Promise<void> => app().SftpRename(id, from, to),
  sftpMove: (id: string, paths: string[], dir: string): Promise<void> => app().SftpMove(id, paths, dir),
  /** Copia en el mismo servidor (sin pisar: "x - copia.ext"). Devuelve las rutas de las copias. */
  sftpCopy: (id: string, paths: string[], dir: string): Promise<string[]> => app().SftpCopy(id, paths, dir),
  /** Crea en el servidor (con su tar o zip) un .tar.gz, .tgz, .tar o .zip con los elementos, que están en una misma carpeta. Devuelve su ruta. */
  sftpCompress: (id: string, paths: string[], name: string): Promise<string> => app().SftpCompress(id, paths, name),
  /** Extrae en el servidor un .tar.gz/.tgz/.tar.bz2/.tar.xz/.tar.zst/.tar/.zip sin pisar nada; devuelve la ruta de lo que creó. */
  sftpExtract: (id: string, archive: string): Promise<string> => app().SftpExtract(id, archive),
  /** Baja a un temporal y arrastra al sistema; false = cancelado. */
  sftpDragOut: (id: string, paths: string[]): Promise<boolean> => app().SftpDragOut(id, paths),
  sftpRemove: (id: string, paths: string[]): Promise<void> => app().SftpRemove(id, paths),
  sftpChmod: (id: string, p: string, mode: number): Promise<void> => app().SftpChmod(id, p, mode),
  sftpCreateFile: (id: string, p: string): Promise<void> => app().SftpCreateFile(id, p),
  sftpReadText: (id: string, p: string): Promise<string> => app().SftpReadText(id, p),
  sftpWriteText: (id: string, p: string, content: string): Promise<void> => app().SftpWriteText(id, p, content),
  /** Como root, con sudo en el servidor. password '' = la que se recuerda de esa conexión, si hay (ver lib/sudo.ts). */
  sftpSudoReadText: (id: string, p: string, password: string): Promise<string> => app().SftpSudoReadText(id, p, password),
  sftpSudoWriteText: (id: string, p: string, content: string, password: string): Promise<void> =>
    app().SftpSudoWriteText(id, p, content, password),
  sftpUpload: (id: string, paths: string[], dir: string, policy: ConflictPolicy): Promise<string[]> =>
    app().SftpUpload(id, paths, dir, policy),
  sftpDownload: (id: string, paths: string[], dir: string, policy: ConflictPolicy): Promise<string[]> =>
    app().SftpDownload(id, paths, dir, policy),
  pickUploadFiles: (): Promise<string[] | null> => app().PickUploadFiles(),
  pickUploadFolder: (): Promise<string> => app().PickUploadFolder(),
  pickDownloadDir: (): Promise<string> => app().PickDownloadDir(),
  pickLogDir: (): Promise<string> => app().PickLogDir(),
  answerAuthPrompt: (id: string, answers: string[] | null): Promise<void> => app().AnswerAuthPrompt(id, answers),
  rdpOpen: (r: { connectionId: string; secret: string; jumpSecrets?: Record<string, { secret: string; save: boolean }> }): Promise<RdpSession> =>
    app().RdpOpen(r),
  rdpTrustCert: (hostPort: string, fingerprint: string): Promise<void> => app().RdpTrustCert(hostPort, fingerprint),
  rdpSaveSecret: (connectionId: string, secret: string): Promise<void> => app().RdpSaveSecret(connectionId, secret),
  rdpClose: (id: string): Promise<void> => app().RdpClose(id),
  vncOpen: (r: { connectionId: string; secret: string; jumpSecrets?: Record<string, { secret: string; save: boolean }> }): Promise<VncSession> =>
    app().VncOpen(r),
  vncSaveSecret: (connectionId: string, secret: string): Promise<void> => app().VncSaveSecret(connectionId, secret),
  vncClose: (id: string): Promise<void> => app().VncClose(id),
  localHome: (): Promise<string> => app().LocalHome(),
  localList: (dir: string): Promise<LocalListing> => app().LocalList(dir),
  localMkdir: (p: string): Promise<void> => app().LocalMkdir(p),
  localRename: (from: string, to: string): Promise<void> => app().LocalRename(from, to),
  localMove: (paths: string[], dir: string): Promise<void> => app().LocalMove(paths, dir),
  /** Copia (sin pisar: "x - copia.ext"). Devuelve las rutas de las copias. */
  localCopy: (paths: string[], dir: string): Promise<string[]> => app().LocalCopy(paths, dir),
  /** A la papelera (Papelera de reciclaje en Windows); los que no pueden ir vuelven en un error NO_TRASH con sus posiciones en data.failed ("0,2"). */
  localRemove: (paths: string[]): Promise<void> => app().LocalRemove(paths),
  /** Elimina definitivamente (sin papelera). */
  localDeleteForever: (paths: string[]): Promise<void> => app().LocalDeleteForever(paths),
  localOpen: (p: string): Promise<void> => app().LocalOpen(p),
  /** Carpeta de los logs de osat. */
  logDir: (): Promise<string> => app().LogDir(),
  /** Versión, sistema, carpeta de datos y últimas 200 líneas del log, para pegar al pedir ayuda. */
  diagnostics: (): Promise<string> => app().Diagnostics(),
  localReveal: (p: string): Promise<void> => app().LocalReveal(p),
  localReadText: (p: string): Promise<string> => app().LocalReadText(p),
  localWriteText: (p: string, content: string): Promise<void> => app().LocalWriteText(p, content),
  /** Arrastre nativo al Explorador; false = cancelado. */
  localDragOut: (paths: string[]): Promise<boolean> => app().LocalDragOut(paths),
  /** Navegador integrado: sirve una carpeta del servidor en 127.0.0.1 (owner = id de la pestaña, que la cierra). */
  webServeFolder: (owner: string, sessionId: string, root: string): Promise<WebSite> => app().WebServeFolder(owner, sessionId, root),
  /** Muestra un puerto del servidor (visto desde el servidor) por su conexión SSH. scheme '' = http. */
  webTunnel: (owner: string, sessionId: string, scheme: string, host: string, port: number): Promise<WebSite> =>
    app().WebTunnel(owner, sessionId, scheme, host, port),
  /** Muestra un servicio de esta PC (solo localhost). */
  webLocal: (owner: string, scheme: string, host: string, port: number): Promise<WebSite> => app().WebLocal(owner, scheme, host, port),
  /** Cierra los sitios que abrió esa pestaña. */
  webClose: (owner: string): Promise<void> => app().WebClose(owner),
  /** Abre una dirección http(s) en el navegador del sistema. */
  webOpenExternal: (url: string): Promise<void> => app().WebOpenExternal(url),
  /** Sesión que solo sostiene la conexión SSH (para los reenvíos de puertos). */
  openTunnel: (r: ConnectRequest): Promise<SessionInfo> => app().OpenTunnel(r),
  openTunnelFrom: (sid: string): Promise<SessionInfo> => app().OpenTunnelFrom(sid),
  forwardList: (connId: string): Promise<Forward[]> => app().ForwardList(connId),
  /** id vacío = nuevo. Si estaba en marcha, se detiene (hay que volver a arrancarlo). */
  forwardSave: (f: Omit<Forward, 'id'> & { id?: string }): Promise<Forward> => app().ForwardSave({ id: '', ...f }),
  forwardDelete: (id: string): Promise<void> => app().ForwardDelete(id),
  /** owner = id de la pestaña, que detiene sus reenvíos al cerrarse. Vuelve cuando ya escucha; si falla, el motivo queda en forwardStates. */
  forwardStart: (owner: string, sessionId: string, id: string): Promise<void> => app().ForwardStart(owner, sessionId, id),
  forwardStop: (id: string): Promise<void> => app().ForwardStop(id),
  forwardStopAll: (owner: string): Promise<void> => app().ForwardStopAll(owner),
  forwardStates: (connId: string): Promise<ForwardState[]> => app().ForwardStates(connId),
  /** El puerto de esta PC que se pidió si está libre; si no, el siguiente que lo esté. */
  forwardFreePort: (preferred: number): Promise<number> => app().ForwardFreePort(preferred),
  snippetsList: (): Promise<Snippet[]> => app().SnippetsList(),
  /** id vacío = nuevo. */
  snippetSave: (sn: Omit<Snippet, 'id'> & { id?: string }): Promise<Snippet> => app().SnippetSave({ id: '', ...sn }),
  snippetDelete: (id: string): Promise<void> => app().SnippetDelete(id),
  sitesList: (): Promise<Site[]> => app().SitesList(),
  /** id vacío = nuevo (al final); con id actualiza nombre y URL. */
  siteSave: (s: Pick<Site, 'name' | 'url'> & Partial<Pick<Site, 'id' | 'order'>>): Promise<Site> => app().SiteSave({ id: '', order: 0, ...s }),
  siteDelete: (id: string): Promise<void> => app().SiteDelete(id),
  /** Mueve el sitio antes de beforeId ("" = al final). */
  sitePlace: (id: string, beforeId: string): Promise<void> => app().SitePlace(id, beforeId),
  /** Navegador nativo de los sitios web. id = id de la pestaña; los eventos llegan como "browser:event" (BrowserEvent). */
  browserSupported: (): Promise<boolean> => app().BrowserSupported(),
  /** Crea la vista nativa de la pestaña y carga la dirección. scale = devicePixelRatio. */
  browserOpen: (id: string, url: string, b: BrowserBounds, scale: number): Promise<void> => app().BrowserOpen(id, url, b, scale),
  browserNavigate: (id: string, url: string): Promise<void> => app().BrowserNavigate(id, url),
  browserBack: (id: string): Promise<void> => app().BrowserBack(id),
  browserForward: (id: string): Promise<void> => app().BrowserForward(id),
  browserReload: (id: string): Promise<void> => app().BrowserReload(id),
  browserSetBounds: (id: string, b: BrowserBounds, scale: number): Promise<void> => app().BrowserSetBounds(id, b, scale),
  /** La vista nativa se dibuja encima del HTML: se oculta cuando algo de osat tiene que quedar encima (lib/overlay.ts). */
  browserSetVisible: (id: string, visible: boolean): Promise<void> => app().BrowserSetVisible(id, visible),
  browserClose: (id: string): Promise<void> => app().BrowserClose(id),
  /** La última sesión guardada (null si no hay). */
  workspaceLastGet: (): Promise<unknown> => app().WorkspaceLastGet(),
  /** null la borra. */
  workspaceLastSet: (data: unknown): Promise<void> => app().WorkspaceLastSet(data),
  workspacesList: (): Promise<NamedWorkspace[]> => app().WorkspacesList(),
  /** id vacío = nuevo. Falla si el nombre ya lo tiene otro. */
  workspaceSave: (id: string, name: string, data: unknown): Promise<NamedWorkspace> => app().WorkspaceSave(id, name, data),
  workspaceRename: (id: string, name: string): Promise<void> => app().WorkspaceRename(id, name),
  workspaceDelete: (id: string): Promise<void> => app().WorkspaceDelete(id),
  k8sNamespaces: (sessionId: string): Promise<K8sNamespaces> => app().K8sNamespaces(sessionId),
  k8sResources: (sessionId: string, namespace: string): Promise<K8sItem[]> => app().K8sResources(sessionId, namespace),
  /** Busca en dir y sus subcarpetas (nombre con comodines; contains = texto del contenido, opcional). Devuelve la carpeta real
   * donde busca; los resultados llegan como eventos search:results:<searchId> (FileEntry[], size -1 = no se sabe, mode '?' =
   * no se sabe si es carpeta) y search:done:<searchId> (SearchDone). Suscribirse antes de llamar. */
  searchStart: (searchId: string, sessionId: string, dir: string, name: string, contains: string): Promise<string> =>
    app().SearchStart(searchId, sessionId, dir, name, contains),
  searchStop: (searchId: string): Promise<void> => app().SearchStop(searchId),
  searchCaps: (sessionId: string): Promise<SearchCaps> => app().SearchCaps(sessionId),
  cancelTransfer: (id: string): Promise<void> => app().CancelTransfer(id),
  /** Vuelve a correr una transferencia fallada (misma fila) por una sesión SFTP abierta de esa conexión. */
  retryTransfer: (id: string): Promise<void> => app().RetryTransfer(id),
  clearTransfers: (): Promise<void> => app().ClearTransfers(),
  /** Compara la carpeta del servidor con una de esta PC y devuelve la vista previa (el plan queda guardado para syncRun). */
  syncCompare: (sessionId: string, req: SyncRequest): Promise<SyncPreview> => app().SyncCompare(sessionId, req),
  /** Ejecuta lo marcado (rutas relativas) del plan como una transferencia del panel. Devuelve su id. */
  syncRun: (sessionId: string, planId: string, selected: string[]): Promise<string> => app().SyncRun(sessionId, planId, selected),
  /** Elige una carpeta de esta PC ('' = cancelado); arranca en start si existe. */
  pickFolder: (title: string, start: string): Promise<string> => app().PickFolder(title, start),
}

// ---- runtime ----

export function on(event: string, cb: (...args: any[]) => void): () => void {
  return window.runtime.EventsOnMultiple(event, cb, -1)
}

export function onFileDrop(cb: (x: number, y: number, paths: string[]) => void) {
  window.runtime?.OnFileDrop?.(cb, true)
}

export async function clipboardGet(): Promise<string> {
  try {
    return (await window.runtime.ClipboardGetText()) ?? ''
  } catch {
    return navigator.clipboard?.readText?.() ?? ''
  }
}

export async function clipboardSet(text: string): Promise<void> {
  try {
    await window.runtime.ClipboardSetText(text)
  } catch {
    await navigator.clipboard?.writeText?.(text)
  }
}

// ---- utilidades ----

export function b64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

export function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`
  const u = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${u[i]}`
}

export function fmtDate(unix: number): string {
  if (!unix) return ''
  const d = new Date(unix * 1000)
  return d.toLocaleString(tr('locale.tag'), { dateStyle: 'short', timeStyle: 'short' })
}

export function shellQuote(s: string): string {
  return `'${s.replace(/'/g, `'\\''`)}'`
}
