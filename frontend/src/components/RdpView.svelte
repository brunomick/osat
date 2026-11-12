<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { get } from 'svelte/store'
  import type { IronError, UserInteraction } from '@devolutions/iron-remote-desktop'
  import { api, clipboardSet, codedText, errorText, parseError, type RdpSession } from '../lib/api'
  import { t, tr } from '../lib/i18n'
  import { askSecret, confirmDialog } from '../lib/dialog'
  import { resolveJumpError, type JumpSecrets } from '../lib/connect'
  import { loadIronRdp } from '../lib/ironrdp'
  import { connections, patchTab, reloadConnections, toast, type Tab } from '../lib/stores'
  import { failureText, rdpFailure, type RdpFailure } from '../lib/rdperror'

  // Escritorio remoto (RDP) dibujado por IronRDP (WASM) en un canvas de la
  // página. El backend solo hace de proxy: RdpOpen verifica el servidor y
  // devuelve un WebSocket local por el que IronRDP habla RDP.
  export let tab: Tab
  export let active: boolean

  type State = 'connecting' | 'connected' | 'disconnected'
  let state: State = 'connecting'
  let message = ''
  let failure: RdpFailure | null = null // paso y detalles técnicos del error, si los hay

  let area: HTMLDivElement // lo que ocupa la pestaña: de acá sale el tamaño del escritorio
  let host: HTMLDivElement // contiene el <iron-remote-desktop>
  let ui: UserInteraction | null = null
  let rdp: Awaited<ReturnType<typeof loadIronRdp>> | null = null
  let session: RdpSession | null = null
  let destroyed = false

  function setState(s: State, msg = '') {
    state = s
    message = msg
    failure = null
    patchTab(tab.id, { status: s === 'connected' ? 'ready' : s === 'connecting' ? 'connecting' : 'closed' })
  }

  // Tamaño del escritorio remoto = el del área, en píxeles CSS (así el canvas
  // va 1:1 y el clic cae justo bajo el puntero). Display Control pide ancho par
  // y entre 200 y 8192.
  function areaSize() {
    const r = area.getBoundingClientRect()
    const clamp = (v: number) => Math.min(8192, Math.max(200, Math.floor(v)))
    return { width: clamp(r.width) & ~1, height: clamp(r.height) }
  }

  /** Crea el elemento de IronRDP (una sola vez por pestaña) y espera a que esté listo. */
  async function ensureUi(): Promise<UserInteraction> {
    if (ui) return ui
    rdp = await loadIronRdp()
    const el = document.createElement('iron-remote-desktop') as HTMLElement & { module: unknown }
    el.module = rdp.Backend // tiene que estar antes de conectarlo al DOM: lo lee al crearse
    el.setAttribute('scale', 'real')
    el.setAttribute('flexcenter', 'true')
    // IronRDP toma el teclado apenas el mouse entra al escritorio; en osat se
    // toma con un clic (si no, pasar el mouse le saca el foco a una terminal vecina).
    el.addEventListener('mouseenter', (e) => e.stopPropagation(), true)
    el.addEventListener('mousedown', () => el.focus())
    const ready = new Promise<UserInteraction>((res) =>
      el.addEventListener('ready', (e) => res((e as CustomEvent).detail.irgUserInteraction), { once: true }),
    )
    host.appendChild(el)
    const u = await ready
    // IronRDP no rehace su diseño cuando el servidor aplica un tamaño nuevo
    // (quedaban las barras que puso mientras se arrastraba la ventana): se lo
    // pide apenas cambia el tamaño del canvas, que es justo ese momento.
    const canvas = el.shadowRoot?.querySelector('canvas')
    if (canvas) {
      canvasObs = new MutationObserver(() => u.setScale(3)) // ScreenScale.Real
      canvasObs.observe(canvas, { attributes: true, attributeFilter: ['width', 'height'] })
    }
    // IronRDP avisa "ready" antes de terminar de preparar el portapapeles
    // (initClipboard espera esta misma consulta de permisos): si se conecta
    // antes, la sesión arranca sin canal de portapapeles. Fuera de un contexto
    // seguro (Linux/macOS) no hay portapapeles ni nada que esperar.
    if (window.isSecureContext) {
      await navigator.permissions.query({ name: 'clipboard-read' as PermissionName }).catch(() => {})
      await new Promise((r) => setTimeout(r))
    }
    ui = u
    ui.setEnableClipboard(true)
    ui.setEnableAutoClipboard(true)
    return ui
  }

  // Contraseñas del salto (ProxyJump) pedidas en el momento, por id de conexión.
  const jumpSecrets: JumpSecrets = {}

  async function connect(secret = '', save = false): Promise<void> {
    // Por un RD Gateway puede haber que aprobar el ingreso en el celular: se avisa.
    const gateway = get(connections).find((x) => x.id === tab.connId)?.gateway
    setState('connecting', gateway ? tr('rdp.gatewayWaiting', { gateway }) : '')
    // IronRDP lee el portapapeles de Windows desde un temporizador, y WebView2
    // solo pide el permiso de lectura (que Wails concede) si viene de un gesto
    // del usuario: si no, lo rechaza e IronRDP apaga el portapapeles para toda
    // la sesión. connect siempre sale de un clic o una tecla (abrir la pestaña,
    // Reconectar, un diálogo), así que se pide acá; después queda concedido.
    if (window.isSecureContext) navigator.clipboard.read().catch(() => {})
    let s: RdpSession
    try {
      s = await api.rdpOpen({ connectionId: tab.connId, secret, jumpSecrets })
    } catch (e) {
      if (destroyed) return
      return handleOpenError(e, secret, save)
    }
    // El backend guardó las contraseñas del salto que se pidieron con "guardar".
    if (Object.values(jumpSecrets).some((j) => j.save)) reloadConnections()
    if (destroyed) {
      // La pestaña se cerró mientras se sondeaba el servidor.
      api.rdpClose(s.id).catch(() => {})
      return
    }
    session = s
    patchTab(tab.id, { sessionId: s.id })
    try {
      const u = await ensureUi()
      const cfg = u
        .configBuilder()
        .withUsername(s.user)
        .withPassword(s.password)
        .withServerDomain(s.domain)
        .withDestination(s.destination)
        .withProxyAddress(s.proxyUrl)
        .withAuthToken(s.token)
        .withDesktopSize(areaSize())
        .withExtension(rdp!.displayControl(true))
        .build()
      const info = await u.connect(cfg)
      if (destroyed) {
        u.shutdown()
        api.rdpClose(s.id).catch(() => {})
        return
      }
      u.setVisibility(true)
      setState('connected')
      // Recién ahora se sabe que el servidor aceptó la contraseña.
      if (secret && save) api.rdpSaveSecret(tab.connId, secret).catch(() => {})
      info
        .run()
        .then((t) => ended(s, t.reason()))
        .catch((e) => ended(s, ironText(e)))
    } catch (e) {
      api.rdpClose(s.id).catch(() => {})
      if (destroyed) return
      if (isIronError(e) && (e.kind() === 1 || e.kind() === 2)) {
        // Usuario o contraseña incorrectos: volver a pedirla.
        return askAndConnect(tr('rdp.badCreds'))
      }
      setState('disconnected', ironText(e))
      patchTab(tab.id, { status: 'error' })
    }
  }

  function ended(s: RdpSession, reason: string) {
    api.rdpClose(s.id).catch(() => {})
    if (destroyed || session !== s) return
    setState('disconnected', reason ? tr('rdp.sessionEnded', { reason }) : '')
  }

  // secret/save: lo que se tipeó para este intento, para no volver a pedirlo
  // después de confirmar el certificado.
  async function handleOpenError(e: unknown, secret: string, save: boolean): Promise<void> {
    const k = parseError(e)
    const d = k?.data ?? {}
    // Huella o contraseña del salto SSH por el que se llega al servidor.
    const jump = await resolveJumpError(e, jumpSecrets)
    if (destroyed) return
    if (jump === 'retry') return connect(secret, save)
    if (jump === 'cancel') return setState('disconnected', tr('term.cancelled'))
    if (k?.code === 'NEED_SECRET') return askAndConnect()
    if (k?.code === 'AUTH_FAILED') return askAndConnect(codedText(k)) // el gateway rechazó la contraseña
    if (k?.code === 'RDPCERT_UNKNOWN' || k?.code === 'RDPCERT_MISMATCH') {
      const changed = k.code === 'RDPCERT_MISMATCH'
      // El del RD Gateway se dice: si no, parecería un problema del equipo.
      const gw = d.gateway === '1'
      const ok = await confirmDialog({
        title: changed
          ? tr(gw ? 'rdp.gwCertChangedTitle' : 'rdp.certChangedTitle')
          : tr(gw ? 'rdp.gwCertUnknownTitle' : 'rdp.certUnknownTitle'),
        message: changed
          ? tr(gw ? 'rdp.gwCertChangedMsg' : 'rdp.certChangedMsg', { text: codedText(k), host: d.host })
          : tr(gw ? 'rdp.gwCertUnknownMsg' : 'rdp.certUnknownMsg', { host: d.host }),
        details: [
          [gw ? tr('rdp.gateway') : tr('rdp.server'), d.host],
          [tr('rdp.issuedTo'), d.subject],
          [tr('rdp.issuedBy'), d.issuer],
          [tr('rdp.expires'), d.notAfter],
          ...(changed ? ([[tr('rdp.expected'), d.expected]] as [string, string][]) : []),
          [changed ? tr('rdp.received') : tr('rdp.fingerprint'), d.fingerprint],
        ],
        okText: changed ? tr('rdp.replaceConnect') : tr('rdp.trustConnect'),
        danger: changed,
      })
      if (destroyed) return
      if (ok) {
        try {
          // Con la huella que se mostró: si el servidor presentó otra mientras tanto, falla.
          await api.rdpTrustCert(d.host, d.fingerprint)
          return connect(secret, save)
        } catch (e2) {
          e = e2
        }
      } else {
        return setState('disconnected', tr('term.cancelled'))
      }
    }
    setState('disconnected', errorText(e))
    failure = rdpFailure(parseError(e))
    patchTab(tab.id, { status: 'error' })
  }

  async function copyDetails() {
    if (!failure) return
    await clipboardSet(failureText(message, failure))
    toast(tr('rdp.detailsCopied'), 'success', 2000)
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

  function isIronError(e: unknown): e is IronError {
    return typeof e === 'object' && e !== null && typeof (e as IronError).kind === 'function'
  }

  // Mensajes de IronRDP (en inglés) traducidos a lo que el usuario puede hacer.
  function ironText(e: unknown): string {
    if (!isIronError(e)) return e instanceof Error ? e.message : String(e)
    const d = e.rdcleanpathDetails()
    switch (e.kind()) {
      case 1:
      case 2:
        return tr('rdp.badCreds')
      case 3:
        return tr('rdp.accessDenied')
      case 4:
        if (d?.wsaErrorCode === 10061) return tr('rdp.refused')
        if (d?.wsaErrorCode === 10060) return tr('rdp.noResponse')
        if (d?.httpStatusCode === 502) return tr('rdp.tlsFailed')
        return tr('rdp.proxyFailed')
      case 5:
        return tr('rdp.proxyUnreachable')
      case 6:
        return tr('rdp.negotiation', { detail: firstLine(e.backtrace()) })
    }
    return firstLine(e.backtrace()) || tr('rdp.unknownError')
  }

  const firstLine = (s: string) => (s ?? '').split('\n')[0].trim()

  function reconnect() {
    if (session) api.rdpClose(session.id).catch(() => {})
    session = null
    patchTab(tab.id, { sessionId: undefined })
    connect()
  }

  // El escritorio sigue el tamaño de la pestaña (Display Control), con una
  // espera para no mandar un cambio por cada píxel mientras se arrastra.
  // Se compara con el tamaño real (el canvas de IronRDP), no con el último
  // pedido: el servidor a veces ignora un pedido y el escritorio quedaba más
  // grande que la pestaña, con barras de desplazamiento. Por eso, después de
  // pedir un tamaño se verifica una vez más.
  let resizeTimer: ReturnType<typeof setTimeout>
  let ro: ResizeObserver
  let canvasObs: MutationObserver | undefined
  function onResize(recheck = false) {
    clearTimeout(resizeTimer)
    resizeTimer = setTimeout(() => {
      if (state !== 'connected' || !ui || !area.offsetParent) return // pestaña oculta: medidas en 0
      const s = areaSize()
      const c = host.querySelector('iron-remote-desktop')?.shadowRoot?.querySelector('canvas')
      if (c && c.width === s.width && c.height === s.height) return
      ui.resize(s.width, s.height)
      if (!recheck) onResize(true)
    }, recheck ? 2000 : 400)
  }

  onMount(() => {
    ro = new ResizeObserver(() => onResize())
    ro.observe(area)
    connect()
  })

  $: if (active) onResize() // al volver a la pestaña puede haber cambiado el tamaño

  // closeTab también cierra el proxy (está en tab.sessionId); cerrarlo acá
  // cubre cuando la vista se destruye sin cerrar la pestaña (p. ej. hot-reload).
  onDestroy(() => {
    destroyed = true
    ro?.disconnect()
    canvasObs?.disconnect()
    clearTimeout(resizeTimer)
    ui?.shutdown()
    if (session) api.rdpClose(session.id).catch(() => {})
  })
</script>

<div class="rdp" bind:this={area}>
  <div class="screen" class:hidden={state !== 'connected'} bind:this={host}></div>
  {#if state === 'disconnected'}
    <div class="msg">
      {#if failure?.step}<p class="step">{$t('rdp.failedAt', { step: failure.step })}</p>{/if}
      <p>{message || $t('rdp.closed')}</p>
      {#if failure}
        <details class="tech">
          <summary>{$t('rdp.details')}</summary>
          <dl>
            {#each failure.details as [k, v]}<dt>{k}</dt><dd>{v}</dd>{/each}
          </dl>
          <button on:click={copyDetails}>{$t('rdp.copyDetails')}</button>
        </details>
      {/if}
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
    display: flex;
  }
  .screen.hidden {
    visibility: hidden;
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
  }
  .msg p {
    max-width: 640px;
  }
  .step {
    margin: 0;
    font-weight: 600;
  }
  .tech {
    max-width: 640px;
    width: 100%;
    text-align: left;
    font-size: 12px;
  }
  .tech summary {
    cursor: pointer;
    color: var(--text-2);
  }
  .tech dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 4px 12px;
    margin: 8px 0;
    user-select: text;
  }
  .tech dt {
    color: var(--text-2);
  }
  .tech dd {
    margin: 0;
    font-family: var(--mono);
    word-break: break-word;
  }
  .hint {
    margin: 0;
    color: var(--text-2);
    font-size: 12px;
  }
</style>
