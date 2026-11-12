<script lang="ts">
  import { onMount, onDestroy, tick } from 'svelte'
  import { Terminal } from '@xterm/xterm'
  import { FitAddon } from '@xterm/addon-fit'
  import { WebLinksAddon } from '@xterm/addon-web-links'
  import { SerializeAddon } from '@xterm/addon-serialize'
  import { SearchAddon } from '@xterm/addon-search'
  import { t, tr } from '../lib/i18n'
  import { api, on, b64ToBytes, clipboardGet, clipboardSet, errorText, shellQuote, type SessionInfo } from '../lib/api'
  import { beep } from '../lib/bell'
  import { bracketedModes, broadcast, destinations, pasteEach, recipients, setBracketed } from '../lib/broadcast'
  import { connectTab, CancelledError } from '../lib/connect'
  import { confirmDialog } from '../lib/dialog'
  import { reconnect, type Reconnector } from '../lib/reconnect'
  import { isAndroid, keys } from '../lib/platform'
  import { resolve } from '../lib/termthemes'
  import { uiTheme } from '../lib/theme'
  import { closeTab, connections, handoffs, LOCAL, patchTab, saveTextRequest, settings, snippetsRequest, tabs, toast, type Tab } from '../lib/stores'
  import ContextMenu, { type MenuItem } from './ContextMenu.svelte'
  import SnippetsPanel from './SnippetsPanel.svelte'

  export let tab: Tab
  export let active: boolean
  /** Embebida en otra vista (p. ej. abajo del explorador SFTP): no es una
   * pestaña, así que cierra su propia sesión y no roba el foco al activarse. */
  export let embedded = false
  /** Visible y en el panel enfocado: es la que recibe el teclado. */
  export let focused = false

  /** Cambia de carpeta en la shell (espacio inicial: no queda en el historial). */
  export function cd(dir: string) {
    if (state === 'ready' && sessionId) api.termWrite(sessionId, ` cd ${shellQuote(dir)}\r`).catch(() => {})
    term?.focus()
  }

  /** Entrega la sesión y lo que hay en pantalla para que otra vista (una
   * pestaña) la adopte. Después de esto esta vista ya no la cierra. */
  export function detach(): { sessionId: string; screen: string } | null {
    if (state !== 'ready' || !sessionId) return null
    const out = { sessionId, screen: serializer.serialize() }
    cleanupSession()
    sessionId = null
    state = 'closed'
    return out
  }

  let host: HTMLDivElement
  let term: Terminal
  let fit: FitAddon
  let serializer: SerializeAddon
  let search: SearchAddon
  let ro: ResizeObserver
  let unsubs: (() => void)[] = []

  // Android: el teclado del teléfono no tiene Esc, Tab, Ctrl, Alt ni flechas; van en una barra abajo.
  // Ctrl y Alt quedan marcados hasta la próxima tecla: Ctrl la manda como carácter de control
  // (Ctrl+C = \x03) y Alt le antepone Esc (como hacen las terminales con Meta).
  const android = isAndroid()
  let ctrl = false
  let alt = false
  const arrow = (k: string) => (term.modes.applicationCursorKeysMode ? '\x1bO' : '\x1b[') + k
  const topKeys: { label: string; send: () => string }[] = [
    { label: 'Esc', send: () => '\x1b' },
    { label: 'Tab', send: () => '\t' },
  ]
  const extraKeys: { label: string; send: () => string }[] = [
    { label: '←', send: () => arrow('D') },
    { label: '↑', send: () => arrow('A') },
    { label: '↓', send: () => arrow('B') },
    { label: '→', send: () => arrow('C') },
    { label: '|', send: () => '|' },
    { label: '/', send: () => '/' },
    { label: '-', send: () => '-' },
    { label: '~', send: () => '~' },
  ]
  function withMods(d: string) {
    if ((!ctrl && !alt) || d.length !== 1) return d
    if (ctrl) {
      const c = d.toUpperCase().charCodeAt(0)
      if (c >= 64 && c <= 95) d = String.fromCharCode(c - 64)
    }
    if (alt) d = '\x1b' + d
    ctrl = alt = false
    return d
  }
  let sessionId: string | null = null
  let state: 'connecting' | 'ready' | 'reconnecting' | 'closed' = 'connecting'
  let retry: Reconnector | null = null
  let resizeTimer: ReturnType<typeof setTimeout>

  // Visual propia, igual para todos los servidores: los colores que piden
  // (prompt, ls, vim…) son índices ANSI y se pintan con esta paleta, así un
  // Ubuntu y un Amazon Linux se ven iguales. Solo el color de 24 bits
  // (truecolor) llega tal cual.
  // La fuente (incluida en la app, main.ts), el tamaño, el historial y el cursor salen de los ajustes.
  // fontSize es el de esta terminal: arranca con el de los ajustes y el zoom lo cambia solo acá.
  let fontSize = $settings.terminal.fontSize
  // Colores: el ajuste Terminal.Theme (lib/termthemes); "Igual que la interfaz" sigue el tema de la interfaz.
  $: palette = resolve($settings.terminal.theme, $uiTheme)
  $: if (term) term.options.theme = palette
  // Margen (ajuste Terminal.Padding): a la izquierda un poco más y abajo un poco menos; a la derecha queda la barra de scroll.
  $: pad = ((p) => `${p}px 4px ${Math.round(p * 0.6)}px ${p + 4}px`)($settings.terminal.padding)

  function cleanupSession() {
    unsubs.forEach((u) => u())
    unsubs = []
  }

  // Adopta una sesión que ya estaba abierta en otra vista (ver detach).
  async function adopt(id: string, screen?: string) {
    sessionId = id
    unsubs.push(
      on(`term:data:${id}`, (b64: string) => term.write(b64ToBytes(b64))),
      on(`term:exit:${id}`, (msg: string) => onExit(msg)),
    )
    if (screen) term.write(screen)
    state = 'ready'
    patchTab(tab.id, { status: 'ready', sessionId: id })
    fit.fit()
    await api.termResize(id, term.cols, term.rows).catch(() => {})
    if (focused) term.focus()
  }

  async function connect(reuse = true) {
    const h = handoffs.get(tab.id)
    if (h && tab.sessionId) {
      handoffs.delete(tab.id)
      return adopt(tab.sessionId, h.screen)
    }
    cleanupSession()
    state = 'connecting'
    patchTab(tab.id, { status: 'connecting', sessionId: undefined, shared: false })
    term.write(`\x1b[90m${tab.connId === LOCAL ? tr('term.opening', { title: tab.title }) : tr('term.connectingTo', { title: tab.title })}\x1b[0m\r\n`)
    try {
      fit.fit()
      const t = reuse ? tab : { ...tab, fromSession: undefined }
      const info = await connectTab(t, term.cols, term.rows)
      sessionId = info.id
      unsubs.push(
        on(`term:data:${info.id}`, (b64: string) => term.write(b64ToBytes(b64))),
        on(`term:exit:${info.id}`, (msg: string) => onExit(msg)),
      )
      term.reset() // borra el "Conectando…" antes de recibir el banner
      await api.termAttach(info.id)
      state = 'ready'
      patchTab(tab.id, { status: 'ready', sessionId: info.id })
      await api.termResize(info.id, term.cols, term.rows)
      // En las terminales de este equipo la carpeta ya se pasó al abrir la shell.
      if (tab.initialDir && tab.connId !== LOCAL) {
        await api.termWrite(info.id, ` cd ${shellQuote(tab.initialDir)} && clear\r`)
        patchTab(tab.id, { initialDir: undefined })
      }
      if (focused) term.focus()
    } catch (e) {
      state = 'closed'
      patchTab(tab.id, { status: 'error' })
      const msg = e instanceof CancelledError ? tr('term.cancelled') : errorText(e)
      term.write(`\r\n\x1b[31m✖ ${msg}\x1b[0m\r\n\x1b[90m${tr('term.pressRetry')}\x1b[0m\r\n`)
    }
  }

  function onExit(msg: string) {
    cleanupSession()
    sessionId = null
    state = 'closed'
    patchTab(tab.id, { status: 'closed', sessionId: undefined, shared: false })
    // Sin texto, la shell terminó (exit, logout, Ctrl+D): se cierra la pestaña, como en Windows Terminal.
    // Embebida abajo del SFTP no es una pestaña: queda el aviso para abrir otra.
    if (!msg && !embedded) {
      closeTab(tab.id)
      return
    }
    // Con texto, la conexión se cortó (no fue un exit): se reintenta sola.
    if (msg && tab.connId !== LOCAL) {
      term.write(`\r\n\x1b[33m● ${tr('term.closedMsg', { msg })}\x1b[0m\r\n`)
      autoReconnect()
      return
    }
    term.write(
      tab.connId === LOCAL
        ? `\r\n\x1b[33m● ${tr('term.ended')}\x1b[0m \x1b[90m${tr('term.pressOpenOther')}\x1b[0m\r\n`
        : `\r\n\x1b[33m● ${msg ? tr('term.closedMsg', { msg }) : tr('term.closed')}\x1b[0m \x1b[90m${tr('term.pressReconnect')}\x1b[0m\r\n`,
    )
  }

  // ---------- Reconexión automática (ver lib/reconnect.ts) ----------
  // La línea de estado se reescribe en el lugar; lo que había en pantalla queda.

  const statusLine = (text: string) => term.write(`\r\x1b[2K\x1b[90m${text}\x1b[0m`)

  function autoReconnect() {
    state = 'reconnecting'
    patchTab(tab.id, { status: 'connecting' })
    retry = reconnect(
      () => {
        fit.fit()
        return connectTab({ ...tab, fromSession: undefined }, term.cols, term.rows, { interactive: false, reconnect: true })
      },
      (s) => {
        if (s.phase === 'waiting') statusLine(tr('term.reconnectWaiting', { attempt: s.attempt, seconds: s.seconds }))
        else if (s.phase === 'trying') statusLine(tr('term.reconnectTrying', { attempt: s.attempt }))
        else if (s.phase === 'connected') reconnected(s.result)
        else if (state === 'reconnecting') stopReconnect(s.phase === 'needsUser' ? errorText(s.error) : tr('term.reconnectCancelled')) // no al cerrar la vista
      },
      { discard: (info) => api.closeSession(info.id).catch(() => {}) },
    )
  }

  // El ciclo terminó sin conectar: queda el "Presioná Enter" de siempre, que pregunta lo que haga falta.
  function stopReconnect(msg: string) {
    retry = null
    state = 'closed'
    patchTab(tab.id, { status: 'closed' })
    term.write(`\r\x1b[2K\x1b[33m● ${msg.replace(/\.$/, '')}.\x1b[0m \x1b[90m${tr('term.pressReconnect')}\x1b[0m\r\n`)
  }

  async function reconnected(info: SessionInfo) {
    retry = null
    sessionId = info.id
    unsubs.push(
      on(`term:data:${info.id}`, (b64: string) => term.write(b64ToBytes(b64))),
      on(`term:exit:${info.id}`, (msg: string) => onExit(msg)),
    )
    // Sin term.reset(): el shell nuevo sigue debajo de lo que había.
    term.write(`\r\x1b[2K\x1b[90m${tr('term.reconnected')}\x1b[0m\r\n`)
    try {
      await api.termAttach(info.id)
      state = 'ready'
      patchTab(tab.id, { status: 'ready', sessionId: info.id })
      await api.termResize(info.id, term.cols, term.rows)
    } catch (e) {
      cleanupSession()
      sessionId = null
      stopReconnect(errorText(e))
    }
  }

  function doResize() {
    clearTimeout(resizeTimer)
    resizeTimer = setTimeout(() => {
      if (!host || host.clientWidth === 0) return
      fit.fit()
      if (sessionId) api.termResize(sessionId, term.cols, term.rows).catch(() => {})
    }, 60)
  }

  function setFont(size: number) {
    fontSize = Math.max(9, Math.min(28, size))
    term.options.fontSize = fontSize
    doResize()
  }

  // Ajustes en vivo: lo que cambia en Ajustes se aplica a esta terminal sin reiniciarla. Cambiar el
  // tamaño de letra pisa el zoom de esta terminal; las demás opciones no lo tocan.
  let applied = $settings.terminal
  function applyTerminalSettings(t: typeof applied) {
    const prev = applied
    applied = t
    if (t.font !== prev.font) term.options.fontFamily = t.font
    if (t.scrollback !== prev.scrollback) term.options.scrollback = t.scrollback
    if (t.cursor !== prev.cursor) term.options.cursorStyle = t.cursor
    if (t.cursorBlink !== prev.cursorBlink) term.options.cursorBlink = t.cursorBlink
    if (t.lineHeight !== prev.lineHeight) term.options.lineHeight = t.lineHeight
    if (t.letterSpacing !== prev.letterSpacing) term.options.letterSpacing = t.letterSpacing
    if (t.fontWeight !== prev.fontWeight) term.options.fontWeight = t.fontWeight
    if (t.fontWeightBold !== prev.fontWeightBold) term.options.fontWeightBold = t.fontWeightBold
    if (t.minContrast !== prev.minContrast) term.options.minimumContrastRatio = t.minContrast
    if (t.boldBright !== prev.boldBright) term.options.drawBoldTextInBrightColors = t.boldBright
    if (t.cursorInactive !== prev.cursorInactive) term.options.cursorInactiveStyle = t.cursorInactive
    if (t.scrollSpeed !== prev.scrollSpeed) term.options.scrollSensitivity = t.scrollSpeed
    if (t.scrollOnInput !== prev.scrollOnInput) term.options.scrollOnUserInput = t.scrollOnInput
    if (t.wordSeparators !== prev.wordSeparators) term.options.wordSeparator = t.wordSeparators
    // Otra fuente, otro alto de línea o espaciado miden distinto: cambian las columnas y las filas.
    const metrics = (['font', 'lineHeight', 'letterSpacing', 'fontWeight', 'fontWeightBold'] as const).some((k) => t[k] !== prev[k])
    if (t.fontSize !== prev.fontSize) setFont(t.fontSize)
    else if (metrics) doResize()
  }
  $: if (term) applyTerminalSettings($settings.terminal)

  async function paste() {
    const text = await clipboardGet()
    if (!text || !sessionId) return
    // Varias líneas (o una que termina en Enter) se ejecutan apenas se pegan: según el ajuste, se pregunta antes.
    if ($settings.terminal.pasteConfirm && /[\r\n]/.test(text)) {
      const count = text.replace(/\r?\n$/, '').split(/\r?\n|\r/).length
      const ok = await confirmDialog({ title: tr('term.pasteConfirmTitle'), message: tr(count === 1 ? 'term.pasteConfirmMsg.one' : 'term.pasteConfirmMsg.other', { count }), okText: tr('term.paste') })
      term.focus()
      if (!ok || !sessionId) return
    }
    term.paste(text)
  }

  // ---------- Búsqueda (Ctrl+Shift+F) ----------

  let searchOpen = false
  let searchInput: HTMLInputElement
  let query = ''
  let caseSensitive = false
  let wholeWord = false
  let regex = false
  /** Coincidencia actual (desde 0) y total; null = todavía no hay datos (el addon avisa un momento después de buscar). */
  let result: { index: number; count: number } | null = null

  $: counter = !result ? '' : result.count === 0 ? $t('term.noResults') : result.index < 0 ? $t('term.results', { count: result.count }) : $t('term.resultOf', { index: result.index + 1, count: result.count })

  // El addon exige colores #RRGGBB sin transparencia: se mezcla `fg` sobre `bg` al porcentaje p.
  function parseHex(c: string): [number, number, number] | null {
    const m = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(c.trim())
    if (!m) return null
    const h = m[1].length === 3 ? m[1].replace(/./g, '$&$&') : m[1]
    return [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16)) as [number, number, number]
  }
  function mix(fg: string, bg: string, p: number) {
    const a = parseHex(fg)
    const b = parseHex(bg)
    if (!a || !b) return fg
    return '#' + a.map((v, i) => Math.round(v * p + b[i] * (1 - p)).toString(16).padStart(2, '0')).join('')
  }

  // Los colores salen de las variables CSS en el momento de buscar (así siguen al tema).
  function searchDecorations() {
    const css = getComputedStyle(document.documentElement)
    const v = (name: string) => css.getPropertyValue(name).trim()
    return {
      matchBackground: mix(v('--warning'), v('--bg'), 0.35),
      activeMatchBackground: v('--signal'),
      matchOverviewRuler: v('--warning'),
      activeMatchColorOverviewRuler: v('--signal'),
    }
  }

  /** incremental: mientras se escribe, sigue en la coincidencia actual si todavía vale. */
  function runSearch(dir: 'incremental' | 'next' | 'prev') {
    if (!query) {
      search.clearDecorations()
      result = null
      return
    }
    const opts = { caseSensitive, wholeWord, regex, incremental: dir === 'incremental', decorations: searchDecorations() }
    let found = false
    try {
      found = dir === 'prev' ? search.findPrevious(query, opts) : search.findNext(query, opts)
    } catch {
      // expresión regular inválida: se muestra como sin resultados
    }
    if (!found) result = { index: -1, count: 0 }
  }

  async function openSearch() {
    searchOpen = true
    await tick()
    searchInput?.focus()
    searchInput?.select()
  }

  function closeSearch() {
    search.clearDecorations()
    searchOpen = false
    result = null
    term.focus()
  }

  function toggle(opt: 'case' | 'word' | 'regex') {
    if (opt === 'case') caseSensitive = !caseSensitive
    else if (opt === 'word') wholeWord = !wholeWord
    else regex = !regex
    runSearch('incremental')
    searchInput?.focus()
  }

  function onSearchKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      closeSearch()
    } else if (e.key === 'Enter') {
      e.preventDefault()
      e.stopPropagation()
      runSearch(e.shiftKey ? 'prev' : 'next')
    } else if ((e.ctrlKey || e.metaKey) && e.shiftKey && e.code === 'KeyF') {
      e.preventDefault()
      searchInput.select()
    }
  }

  // ---------- Snippets (Ctrl+Shift+S, o "Snippets…" en el menú de la pestaña) ----------

  let snippetsOpen = false
  /** Grupo de la conexión (los snippets de ese grupo y sus ancestros se ven acá); null = esta PC. */
  $: snippetsGroup = tab.connId === LOCAL ? null : ($connections.find((c) => c.id === tab.connId)?.group ?? null)
  // El menú de la pestaña pide abrirlo.
  $: if ($snippetsRequest === tab.id && term) {
    snippetsRequest.set(null)
    snippetsOpen = true
  }

  // ---------- Guardar como TXT (menú de la terminal o de la pestaña) ----------
  // Todo lo que quedó en la terminal: el historial (hasta el límite de "Historial" en los ajustes) y la pantalla.

  $: if ($saveTextRequest === tab.id && term) {
    saveTextRequest.set(null)
    saveText()
  }

  async function saveText() {
    const buf = term.buffer.active
    const lines: string[] = []
    for (let i = 0; i < buf.length; i++) {
      const line = buf.getLine(i)
      if (!line) continue
      const text = line.translateToString(true)
      // Una línea larga que la terminal partió en dos se vuelve a unir.
      if (line.isWrapped && lines.length) lines[lines.length - 1] += text
      else lines.push(text)
    }
    while (lines.length && !lines[lines.length - 1]) lines.pop()
    const d = new Date()
    const p2 = (n: number) => String(n).padStart(2, '0')
    const stamp = `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())}_${p2(d.getHours())}-${p2(d.getMinutes())}`
    const name = `${tab.title.replace(/[^\p{L}\p{N}._-]+/gu, '_') || 'terminal'}_${stamp}.txt`
    try {
      const path = await api.saveText(name, lines.join('\r\n') + '\r\n')
      if (path) toast(tr('term.savedText', { path }), 'success')
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  function closeSnippets() {
    snippetsOpen = false
    term.focus()
  }

  // Sale por el mismo camino que lo tipeado: paste() respeta el modo de pegado entre corchetes de la shell.
  // Con `all` (Ejecutar en las N terminales) va directo a todas las del broadcast, esta participe o no.
  function sendSnippet({ text, enter, all }: { text: string; enter: boolean; all?: boolean }) {
    if (all) {
      const to = destinations($tabs)
      if (!to.length) return toast(tr('term.broadcastNone'), 'error')
      // Cada destino con su propio modo de pegado entre corchetes (lo publica cada terminal).
      pasteEach(to, text, enter, $bracketedModes).forEach((r) => api.termWrite(r.sessionId, r.data).catch(() => {}))
      return
    }
    if (state !== 'ready' || !sessionId) return toast(tr('term.notConnected'), 'error')
    term.paste(text)
    if (enter) term.input('\r', true)
  }

  onMount(() => {
    const s = $settings.terminal
    term = new Terminal({
      fontFamily: s.font,
      fontSize,
      fontWeight: s.fontWeight,
      fontWeightBold: s.fontWeightBold,
      lineHeight: s.lineHeight,
      letterSpacing: s.letterSpacing,
      cursorStyle: s.cursor,
      cursorWidth: 2,
      cursorInactiveStyle: s.cursorInactive,
      cursorBlink: s.cursorBlink,
      // Los colores del servidor que quedarían ilegibles sobre el fondo (el
      // azul oscuro de ls, gris sobre gris) se aclaran hasta este contraste.
      minimumContrastRatio: s.minContrast,
      drawBoldTextInBrightColors: s.boldBright,
      scrollSensitivity: s.scrollSpeed,
      scrollOnUserInput: s.scrollOnInput,
      wordSeparator: s.wordSeparators,
      allowProposedApi: true,
      scrollback: s.scrollback,
      theme: palette,
    })
    fit = new FitAddon()
    term.loadAddon(fit)
    term.loadAddon(
      new WebLinksAddon((e, uri) => {
        // Con "Ctrl+clic", un clic simple no abre el link (así se puede seleccionar sin abrirlo).
        if ($settings.terminal.linkClick === 'ctrl' && !(e.ctrlKey || e.metaKey)) return
        window.runtime?.BrowserOpenURL?.(uri)
      }),
    )
    serializer = new SerializeAddon()
    term.loadAddon(serializer)
    search = new SearchAddon()
    term.loadAddon(search)
    search.onDidChangeResults((r) => {
      if (searchOpen && query) result = { index: r.resultIndex, count: r.resultCount }
    })
    term.open(host)
    // Copiar al seleccionar (como PuTTY): al soltar el mouse, lo seleccionado va al portapapeles.
    term.element?.addEventListener('mouseup', () => {
      if (!$settings.terminal.copyOnSelect || !term.hasSelection()) return
      clipboardSet(term.getSelection())
      toast(tr('term.copiedSelection'), 'success', 1500)
    })

    // Ctrl + rueda: zoom (en vez de scrollear o hacer zoom de toda la ventana).
    term.attachCustomWheelEventHandler((e) => {
      if (!(e.ctrlKey || e.metaKey)) return true
      e.preventDefault()
      if (e.deltaY) setFont(fontSize + (e.deltaY < 0 ? 1 : -1))
      return false
    })

    term.onData((d) => {
      d = withMods(d)
      if (state === 'ready' && sessionId) {
        // En un broadcast, lo que se escribe (o se pega) acá va a la sesión de cada terminal elegida.
        const to = recipients(tab.id, $tabs)
        if (to.length) to.forEach((r) => api.termWrite(r.sessionId, d).catch(() => {}))
        else api.termWrite(sessionId, d).catch(() => {})
      } else if (state === 'reconnecting') {
        if (d === '\r') retry?.retryNow()
        else if (d === '\x1b') retry?.cancel()
      } else if (state === 'closed' && d === '\r') {
        term.clear()
        connect(false)
      }
    })

    // Publica si la shell de esta terminal activó el pegado entre corchetes (xterm no avisa del cambio: se mira después de cada escritura).
    term.onWriteParsed(() => {
      if (!embedded) setBracketed(tab.id, term.modes.bracketedPasteMode)
      if ($settings.terminal.scrollOnOutput) term.scrollToBottom()
    })

    term.onBell(() => {
      const bell = $settings.terminal.bell
      if (bell === 'visual') flashBell()
      else if (bell === 'sound') beep()
    })

    term.attachCustomKeyEventHandler((e) => {
      if (e.type !== 'keydown') return true
      const mod = e.ctrlKey || e.metaKey
      if (mod && e.shiftKey && e.code === 'KeyC') {
        const sel = term.getSelection()
        if (sel) clipboardSet(sel)
        return false
      }
      if ((mod && e.shiftKey && e.code === 'KeyV') || (e.shiftKey && e.code === 'Insert')) {
        paste()
        return false
      }
      if (mod && e.shiftKey && e.code === 'KeyF') {
        openSearch()
        return false
      }
      if (mod && e.shiftKey && e.code === 'KeyS') {
        e.preventDefault()
        snippetsOpen = !snippetsOpen
        return false
      }
      if (mod && (e.key === '=' || e.key === '+')) return setFont(fontSize + 1), false
      if (mod && e.key === '-') return setFont(fontSize - 1), false
      if (mod && e.key === '0') return setFont($settings.terminal.fontSize), false
      return true
    })

    ro = new ResizeObserver(doResize)
    ro.observe(host)
    tick().then(() => connect(true))
  })

  // Campana visual: el borde de la terminal parpadea un momento.
  let bellFlash = false
  let bellTimer: ReturnType<typeof setTimeout>
  function flashBell() {
    bellFlash = true
    clearTimeout(bellTimer)
    bellTimer = setTimeout(() => (bellFlash = false), 150)
  }

  // Clic derecho: según los ajustes, copia si hay selección y si no pega (estilo PuTTY) o abre un menú.
  let menu: { x: number; y: number; items: MenuItem[] } | null = null

  function onContext(e: MouseEvent) {
    e.preventDefault()
    const sel = term.getSelection()
    if ($settings.terminal.rightClick === 'menu') {
      menu = {
        x: e.clientX,
        y: e.clientY,
        items: [
          ...(sel ? [{ label: tr('term.copy'), icon: 'copy', hint: keys('Ctrl+Shift+C'), action: () => clipboardSet(sel) }] : []),
          { label: tr('term.paste'), icon: 'paste', hint: keys('Ctrl+Shift+V'), action: paste },
          { label: tr('term.selectAll'), action: () => term.selectAll() },
          { separator: true, label: '' },
          { label: tr('term.find'), icon: 'search', hint: keys('Ctrl+Shift+F'), action: openSearch },
          { label: tr('term.clear'), action: () => term.clear() },
          { label: tr('term.saveText'), icon: 'download', action: saveText },
        ],
      }
    } else if (sel) {
      clipboardSet(sel)
      term.clearSelection()
    } else {
      paste()
    }
  }

  $: if (active && term) {
    tick().then(() => {
      doResize()
      if (!embedded && focused && !snippetsOpen) term.focus()
    })
  }

  onDestroy(() => {
    if (embedded && sessionId) api.closeSession(sessionId).catch(() => {})
    const r = retry
    retry = null
    state = 'closed'
    r?.cancel() // si un intento conecta después, la sesión se cierra (discard)
    if (!embedded) setBracketed(tab.id, false)
    cleanupSession()
    clearTimeout(bellTimer)
    ro?.disconnect()
    term?.dispose()
  })
</script>

<div class="wrap" role="presentation" style:padding={pad} style:background={palette.background} style:--thumb={palette.selectionInactiveBackground} style:--thumb-hover={palette.selectionBackground} class:bell={bellFlash} class:android class:broadcasting={!embedded && $broadcast.targets.includes(tab.id)} on:contextmenu={onContext}>
  <div class="term" bind:this={host} />
  {#if searchOpen}
    <div class="search" role="presentation" on:contextmenu|stopPropagation>
      <input
        bind:this={searchInput}
        bind:value={query}
        on:input={() => runSearch('incremental')}
        on:keydown={onSearchKey}
        placeholder={$t('sidebar.search')}
        spellcheck="false"
        autocomplete="off"
      />
      <span class="count" class:none={result?.count === 0}>{counter}</span>
      <button class="opt" class:on={caseSensitive} title={$t('term.matchCase')} aria-pressed={caseSensitive} on:click={() => toggle('case')}>Aa</button>
      <button class="opt" class:on={wholeWord} title={$t('term.wholeWord')} aria-pressed={wholeWord} on:click={() => toggle('word')}>ab</button>
      <button class="opt" class:on={regex} title={$t('term.regex')} aria-pressed={regex} on:click={() => toggle('regex')}>.*</button>
      <button class="opt" title={$t('term.prev')} on:click={() => runSearch('prev')}>↑</button>
      <button class="opt" title={$t('term.next')} on:click={() => runSearch('next')}>↓</button>
      <button class="opt" title={$t('term.closeSearch')} on:click={closeSearch}>×</button>
    </div>
  {/if}
  {#if snippetsOpen}
    <SnippetsPanel groupPath={snippetsGroup} on:send={(e) => sendSnippet(e.detail)} on:close={closeSnippets} />
  {/if}
  {#if menu}
    <ContextMenu {...menu} on:close={() => ((menu = null), term.focus())} />
  {/if}
  {#if android}
    <!-- pointerdown sin foco: que tocar una tecla no cierre el teclado del teléfono. -->
    <div class="keybar" role="toolbar">
      <div class="keyrow">
        <button class:on={ctrl} on:pointerdown|preventDefault={() => (ctrl = !ctrl)}>Ctrl</button>
        <button class:on={alt} on:pointerdown|preventDefault={() => (alt = !alt)}>Alt</button>
        {#each topKeys as k}
          <button on:pointerdown|preventDefault={() => term.input(k.send())}>{k.label}</button>
        {/each}
        <button on:pointerdown|preventDefault={paste}>{$t('mobile.paste')}</button>
      </div>
      <div class="keyrow">
        {#each extraKeys as k}
          <button on:pointerdown|preventDefault={() => term.input(k.send())}>{k.label}</button>
        {/each}
      </div>
    </div>
  {/if}
</div>

<style>
  .wrap {
    flex: 1;
    min-height: 0;
    display: flex;
    position: relative;
  }
  /* Campana visual (ver flashBell). */
  .wrap.bell::after {
    content: '';
    position: absolute;
    inset: 0;
    border: 2px solid var(--warning);
    pointer-events: none;
    z-index: 6;
  }
  /* Esta terminal recibe lo que se escribe en el broadcast (la barra de arriba del área principal lo explica). */
  .wrap.broadcasting {
    box-shadow: inset 0 0 0 3px var(--warning);
  }
  /* Barra de búsqueda superpuesta arriba a la derecha (deja libre la barra de scroll). */
  .search {
    position: absolute;
    top: 6px;
    right: 18px;
    z-index: 5;
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 4px;
    background: var(--rail);
    border: 1px solid var(--border);
  }
  .search input {
    width: 180px;
    padding: 2px 6px;
    background: var(--bg);
  }
  .count {
    min-width: 84px;
    padding: 0 6px;
    font-size: 11px;
    text-align: right;
    color: var(--text-3);
  }
  .count.none {
    color: var(--warning);
  }
  .opt {
    min-width: 24px;
    padding: 1px 5px;
    background: transparent;
    border-color: transparent;
    color: var(--text-2);
  }
  .opt:hover:not(:disabled) {
    background: var(--bg-3);
    color: var(--text);
  }
  .opt.on {
    background: var(--bg-4);
    border-color: var(--text-3);
    color: var(--accent-2);
  }
  .term {
    flex: 1;
    min-width: 0;
    min-height: 0;
  }
  .wrap.android .term {
    margin-bottom: 76px; /* lo que ocupa .keybar (dos filas) */
  }
  .keybar {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 5;
    background: var(--bg-2);
    border-top: 1px solid var(--border);
  }
  .keyrow {
    display: flex;
  }
  .keybar button {
    flex: 1;
    min-width: 0;
    padding: 10px 0;
    font-size: 13px;
  }
  .keybar button.on {
    background: var(--accent);
    color: var(--on-accent);
  }
  .term :global(.xterm) {
    height: 100%;
  }
  .term :global(.xterm-viewport) {
    background: transparent !important;
  }
  /* Barra de scroll fina y discreta (se nota al pasar el mouse). */
  .term :global(.xterm-viewport::-webkit-scrollbar) {
    width: 3px;
  }
  .term :global(.xterm-viewport::-webkit-scrollbar-track) {
    background: transparent;
  }
  .term :global(.xterm-viewport::-webkit-scrollbar-thumb) {
    background: var(--thumb);
    border-radius: 99px;
  }
  .term :global(.xterm-viewport:hover::-webkit-scrollbar-thumb) {
    background: var(--thumb-hover);
  }
</style>
