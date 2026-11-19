<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { get } from 'svelte/store'
  import { api, codedText, errorText, on, parseError, type BrowserBounds, type RdpNativeEvent } from '../lib/api'
  import { t, tr } from '../lib/i18n'
  import { askSecret } from '../lib/dialog'
  import { resolveJumpError, type JumpSecrets } from '../lib/connect'
  import { layout, paneRects } from '../lib/layout'
  import { overlayOpen } from '../lib/overlay'
  import { connections, patchTab, reloadConnections, type Tab } from '../lib/stores'

  // Escritorio remoto en Windows: lo muestra el cliente de Escritorio remoto de Windows (el control de
  // mstsc, internal/rdpwin) en una ventana nativa que Go pone encima de esta pestaña, como los sitios web
  // (SiteView). Acá hay un div vacío que marca dónde va, los mensajes mientras no hay sesión y el pedido de
  // contraseña. La ventana nativa se dibuja encima del HTML: se oculta si la pestaña no se ve o si hay algo
  // de osat encima (menús, diálogos…, lib/overlay.ts). En Linux y macOS va RdpView (IronRDP).
  export let tab: Tab
  export let active: boolean

  const id = tab.id

  type State = 'connecting' | 'connected' | 'disconnected'
  let state: State = 'connecting'
  let message = ''
  let host: HTMLDivElement
  let opened = false // hay control nativo para esta pestaña
  let hasSize = false
  let destroyed = false
  let pendingSave = '' // contraseña tipeada con "guardar": se guarda cuando el servidor la acepta

  function setState(s: State, msg = '') {
    state = s
    message = msg
    patchTab(id, { status: s === 'connected' ? 'ready' : s === 'connecting' ? 'connecting' : 'closed' })
  }

  // ---------- Posición y visibilidad de la ventana nativa ----------

  let frame = 0
  let last = ''

  function measure(): BrowserBounds {
    const r = host.getBoundingClientRect()
    const x = Math.round(r.left)
    const y = Math.round(r.top)
    return { x, y, w: Math.round(r.right) - x, h: Math.round(r.bottom) - y }
  }

  function sync() {
    frame = 0
    if (!opened) return
    const b = measure()
    hasSize = b.w > 0 && b.h > 0
    if (!hasSize) return
    const scale = window.devicePixelRatio || 1
    const key = `${b.x},${b.y},${b.w},${b.h},${scale}`
    if (key === last) return
    last = key
    api.rdpNativeSetBounds(id, b, scale).catch(() => {})
  }

  function schedule(..._cause: unknown[]) {
    if (!frame) frame = requestAnimationFrame(sync)
  }

  $: schedule($paneRects, $layout)

  // Mientras conecta no hay nada que mostrar del control: se ve el mensaje de osat.
  $: show = opened && state === 'connected' && active && hasSize && !$overlayOpen
  $: if (opened) api.rdpNativeSetVisible(id, show).catch(() => {})

  // ---------- Conectar ----------

  const jumpSecrets: JumpSecrets = {}

  async function connect(secret = '', save = false): Promise<void> {
    const gateway = get(connections).find((x) => x.id === tab.connId)?.gateway
    setState('connecting', gateway ? tr('rdp.gatewayWaiting', { gateway }) : '')
    const b = measure()
    const scale = window.devicePixelRatio || 1
    try {
      await api.rdpNativeOpen({ tabId: id, connectionId: tab.connId, secret, jumpSecrets, bounds: b, scale })
    } catch (e) {
      if (destroyed) return
      return handleOpenError(e, secret, save)
    }
    if (Object.values(jumpSecrets).some((j) => j.save)) reloadConnections()
    if (destroyed) return api.rdpNativeClose(id).catch(() => {})
    pendingSave = secret && save ? secret : ''
    last = `${b.x},${b.y},${b.w},${b.h},${scale}`
    hasSize = b.w > 0 && b.h > 0
    opened = true
    schedule() // por si la pestaña cambió de lugar mientras se abría
  }

  async function handleOpenError(e: unknown, secret: string, save: boolean): Promise<void> {
    const k = parseError(e)
    const jump = await resolveJumpError(e, jumpSecrets)
    if (destroyed) return
    if (jump === 'retry') return connect(secret, save)
    if (jump === 'cancel') return setState('disconnected', tr('term.cancelled'))
    if (k?.code === 'NEED_SECRET') return askAndConnect()
    if (k?.code === 'AUTH_FAILED') return askAndConnect(codedText(k))
    setState('disconnected', errorText(e))
    patchTab(id, { status: 'error' })
  }

  async function askAndConnect(problem = ''): Promise<void> {
    const c = get(connections).find((x) => x.id === tab.connId)
    const who = c ? `${c.domain ? c.domain + '\\' : ''}${c.user}@${c.host}:${c.port}` : ''
    const r = await askSecret({
      title: tr('rdp.connectTo', { title: tab.title }),
      message: problem ? `${problem} ${who}` : who,
      label: tr('import.password'),
    })
    if (destroyed) return
    if (r) return connect(r.secret, r.save)
    setState('disconnected', tr('term.cancelled'))
  }

  // Motivos de desconexión de Windows: 1 a 3 son un cierre normal (desde acá, desde la sesión o por el
  // servidor); 2055 (0x807) es usuario o contraseña incorrectos con NLA.
  const NORMAL = new Set([1, 2, 3])
  const BAD_CREDS = 2055

  function onEvent(ev: RdpNativeEvent) {
    if (ev.id !== id || destroyed) return
    if (ev.kind === 'connected') return setState('connected')
    if (ev.kind === 'loginComplete') {
      if (pendingSave) api.rdpSaveSecret(tab.connId, pendingSave).catch(() => {})
      pendingSave = ''
      if (active) api.rdpNativeFocus(id).catch(() => {})
      return
    }
    // Archivos soltados sobre el escritorio: el cliente de Windows no acepta arrastrarlos, así que quedan en el
    // portapapeles (internal/rdpwin/drop_windows.go) y se pegan allá con Ctrl+V.
    // El aviso va encima del escritorio: un toast de osat quedaría tapado por la ventana nativa.
    if (ev.kind === 'filesCopied') {
      const n = ev.files ?? 0
      return void api.rdpNativeNotice(id, tr(n === 1 ? 'rdp.filesCopied.one' : 'rdp.filesCopied.other', { count: n })).catch(() => {})
    }
    if (ev.kind === 'filesError') return void api.rdpNativeNotice(id, tr('rdp.filesError', { error: ev.text })).catch(() => {})
    // disconnected
    opened = false
    api.rdpNativeClose(id).catch(() => {})
    if (ev.reason === BAD_CREDS) return void askAndConnect(tr('rdp.badCreds'))
    if (NORMAL.has(ev.reason)) return setState('disconnected')
    setState('disconnected', ev.text || tr('rdp.unknownError'))
    patchTab(id, { status: 'error' })
  }

  function reconnect() {
    opened = false
    api.rdpNativeClose(id).catch(() => {})
    connect()
  }

  onMount(() => {
    const off = on('rdp:native', onEvent)
    const ro = new ResizeObserver(schedule)
    ro.observe(host)
    window.addEventListener('resize', schedule)
    connect()
    return () => {
      off()
      ro.disconnect()
      window.removeEventListener('resize', schedule)
    }
  })

  onDestroy(() => {
    destroyed = true
    cancelAnimationFrame(frame)
    api.rdpNativeClose(id).catch(() => {})
  })
</script>

<div class="rdp">
  <div class="screen" bind:this={host}></div>
  {#if state === 'disconnected'}
    <div class="msg">
      <p>{message || $t('rdp.closed')}</p>
      <button on:click={reconnect}>{$t('sftp.reconnect')}</button>
    </div>
  {:else if state === 'connecting'}
    <div class="msg">
      <p>{$t('term.connectingTo', { title: tab.title })}</p>
      {#if message}<p class="hint">{message}</p>{/if}
    </div>
  {/if}
</div>

<style>
  .rdp {
    flex: 1;
    min-height: 0;
    min-width: 0;
    display: flex;
    position: relative;
    overflow: hidden;
  }
  .screen {
    position: absolute;
    inset: 0;
  }
  .msg {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 12px;
    text-align: center;
    padding: 16px;
    white-space: pre-line; /* el texto de Windows trae saltos de línea */
  }
  .msg p {
    max-width: 640px;
  }
  .hint {
    margin: 0;
    color: var(--text-2);
    font-size: 12px;
  }
</style>
