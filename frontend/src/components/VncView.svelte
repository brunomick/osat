<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { get } from 'svelte/store'
  import { api, errorText, type VncSession } from '../lib/api'
  import { t, tr } from '../lib/i18n'
  import { askSecret } from '../lib/dialog'
  import { resolveJumpError, type JumpSecrets } from '../lib/connect'
  import { connections, patchTab, reloadConnections, type Tab } from '../lib/stores'
  import { classifyEnd, onlyPassword, type VncEnd, type VncFacts } from '../lib/vnc'

  // Escritorio remoto (VNC) dibujado por noVNC en un canvas de la página. El
  // backend solo hace de proxy: VncOpen abre el TCP al servidor (directo o por
  // el SSH de la conexión) y devuelve un WebSocket local por el que noVNC habla RFB.
  export let tab: Tab

  type RFBClass = typeof import('@novnc/novnc').default
  type RFB = InstanceType<RFBClass>
  type State = 'connecting' | 'connected' | 'disconnected'
  let state: State = 'connecting'
  let message = ''

  let host: HTMLDivElement // acá se dibuja el escritorio (noVNC agrega su propio contenedor adentro)
  let rfb: RFB | null = null
  let session: VncSession | null = null
  let destroyed = false

  function setState(s: State, msg = '') {
    state = s
    message = msg
    patchTab(tab.id, { status: s === 'connected' ? 'ready' : s === 'connecting' ? 'connecting' : 'closed' })
  }

  function fail(msg: string) {
    setState('disconnected', msg)
    patchTab(tab.id, { status: 'error' })
  }

  // Contraseñas del salto (ProxyJump) pedidas en el momento, por id de conexión.
  const jumpSecrets: JumpSecrets = {}

  async function connect(secret = '', save = false): Promise<void> {
    setState('connecting')
    let s: VncSession
    try {
      s = await api.vncOpen({ connectionId: tab.connId, secret, jumpSecrets })
    } catch (e) {
      if (destroyed) return
      return handleOpenError(e, secret, save)
    }
    // El backend guardó las contraseñas del salto que se pidieron con "guardar".
    if (Object.values(jumpSecrets).some((j) => j.save)) reloadConnections()
    if (destroyed) {
      // La pestaña se cerró mientras se abría la conexión.
      api.vncClose(s.id).catch(() => {})
      return
    }
    session = s
    patchTab(tab.id, { sessionId: s.id })
    try {
      const { default: RFB } = await import('@novnc/novnc') // recién acá se carga: no demora el arranque de osat
      if (destroyed) return // onDestroy ya cerró la sesión
      start(RFB, s, secret, save)
    } catch (e) {
      api.vncClose(s.id).catch(() => {})
      if (!destroyed) fail(errorText(e))
    }
  }

  /** Conecta noVNC con el proxy local y atiende lo que pasa hasta que termina. */
  function start(RFB: RFBClass, s: VncSession, secret: string, save: boolean) {
    // Lo que se sabe al terminar (lib/vnc.ts decide qué decirle al usuario).
    const facts: VncFacts = { connected: false, clean: true, cancelled: false, unsupportedAuth: false, securityFailure: null, passwordSent: !!s.password }
    // La contraseña tipeada en este intento: se guarda recién cuando el servidor la acepta.
    let typed = secret
    let typedSave = save

    const r = new RFB(host, s.url, { credentials: s.password ? { password: s.password } : {} })
    r.scaleViewport = true // la imagen se ajusta a la pestaña
    r.resizeSession = false // sin cambiar la resolución del servidor
    rfb = r
    const mine = () => rfb === r
    function on<T = undefined>(name: string, fn: (detail: T) => void) {
      r.addEventListener(name, (e) => mine() && fn((e as CustomEvent<T>).detail))
    }

    on('connect', () => {
      facts.connected = true
      setState('connected')
      if (typed && typedSave) api.vncSaveSecret(tab.connId, typed).catch(() => {})
    })

    // Llega solo si el servidor pide algo que no se mandó de entrada.
    on<{ types: string[] }>('credentialsrequired', async ({ types }) => {
      if (!onlyPassword(types)) {
        facts.unsupportedAuth = true
        return r.disconnect()
      }
      const a = await askSecret({ title: tr('rdp.connectTo', { title: tab.title }), message: target(), label: tr('import.password') })
      if (!mine()) return
      if (!a) {
        facts.cancelled = true
        return r.disconnect()
      }
      typed = a.secret
      typedSave = a.save
      facts.passwordSent = true
      r.sendCredentials({ password: a.secret })
    })

    on<{ reason?: string }>('securityfailure', (d) => (facts.securityFailure = { reason: d.reason ?? '' }))

    // Verificación de la identidad del servidor: solo la piden las autenticaciones que osat no soporta (RealVNC).
    on('serververification', () => {
      facts.unsupportedAuth = true
      r.disconnect()
    })

    on<{ text: string }>('clipboard', ({ text }) => {
      lastClip = text
      if (canClip()) navigator.clipboard.writeText(text).catch(() => {})
    })

    on<{ clean: boolean }>('disconnect', ({ clean }) => {
      rfb = null
      api.vncClose(s.id).catch(() => {})
      facts.clean = clean
      ended(classifyEnd(facts))
    })
  }

  function ended(end: VncEnd) {
    const why = (base: string, reason: string) => (reason ? `${base} ${tr('vnc.serverSays', { reason })}` : base)
    switch (end.kind) {
      case 'cancelled':
        return setState('disconnected', tr('term.cancelled'))
      case 'badPassword':
        return askAndConnect(why(tr('vnc.badPassword'), end.reason)) // volver a pedirla
      case 'refused':
        return fail(why(tr('vnc.refused'), end.reason))
      case 'unsupportedAuth':
        return fail(tr('vnc.unsupportedAuth'))
      case 'handshake':
        return fail(tr('vnc.handshake'))
      case 'lost':
        return setState('disconnected', tr('vnc.lost'))
      case 'closed':
        return setState('disconnected')
    }
  }

  // Errores de VncOpen: el servidor apagado o el puerto cerrado llegan acá, ya traducidos por el backend.
  async function handleOpenError(e: unknown, secret: string, save: boolean): Promise<void> {
    // Huella o contraseña del salto SSH por el que se llega al servidor.
    const jump = await resolveJumpError(e, jumpSecrets)
    if (destroyed) return
    if (jump === 'retry') return connect(secret, save)
    if (jump === 'cancel') return setState('disconnected', tr('term.cancelled'))
    fail(errorText(e))
  }

  /** "host:puerto" de la conexión, para los diálogos. */
  function target(): string {
    const c = get(connections).find((x) => x.id === tab.connId)
    return c ? `${c.host}:${c.port}` : ''
  }

  async function askAndConnect(problem = ''): Promise<void> {
    const r = await askSecret({
      title: tr('rdp.connectTo', { title: tab.title }),
      message: problem ? `${problem} ${target()}` : target(),
      label: tr('import.password'),
    })
    if (destroyed) return
    if (r) return connect(r.secret, r.save)
    setState('disconnected', tr('term.cancelled'))
  }

  function reconnect() {
    if (session) api.vncClose(session.id).catch(() => {})
    session = null
    patchTab(tab.id, { sessionId: undefined })
    connect()
  }

  function ctrlAltDel() {
    rfb?.sendCtrlAltDel()
    rfb?.focus()
  }

  // ---------- Portapapeles (texto) ----------
  // Como en RDP, solo hay portapapeles en un contexto seguro (Windows): en Linux y macOS no.
  const canClip = () => window.isSecureContext && !!navigator.clipboard
  let lastClip = '' // lo último que pasó de una punta a la otra, para no repetirlo

  // noVNC no mira el portapapeles local: se lo manda al servidor cuando el escritorio toma el foco.
  function toServer() {
    if (!rfb || state !== 'connected' || !canClip()) return
    navigator.clipboard
      .readText()
      .then((text) => {
        if (!text || text === lastClip || !rfb) return
        lastClip = text
        rfb.clipboardPasteFrom(text)
      })
      .catch(() => {})
  }

  // Volver a osat desde otra aplicación no pasa por focusin (el foco sigue en el escritorio).
  const onWindowFocus = () => host?.contains(document.activeElement) && toServer()

  onMount(() => {
    window.addEventListener('focus', onWindowFocus)
    connect()
  })

  // closeTab también cierra el proxy (está en tab.sessionId); cerrarlo acá
  // cubre cuando la vista se destruye sin cerrar la pestaña (p. ej. hot-reload).
  onDestroy(() => {
    destroyed = true
    window.removeEventListener('focus', onWindowFocus)
    const r = rfb
    rfb = null // así su evento "disconnect" no toca el estado
    r?.disconnect()
    if (session) api.vncClose(session.id).catch(() => {})
  })
</script>

<div class="vnc">
  {#if state === 'connected'}
    <div class="bar">
      <button class="ghost" title={$t('vnc.ctrlAltDelTip')} on:click={ctrlAltDel}>{$t('vnc.ctrlAltDel')}</button>
    </div>
  {/if}
  <div class="stage">
    <div class="vnc-screen" class:hidden={state !== 'connected'} bind:this={host} on:focusin={toServer}></div>
    {#if state === 'disconnected'}
      <div class="msg">
        <p>{message || $t('vnc.closed')}</p>
        <button on:click={reconnect}>{$t('sftp.reconnect')}</button>
      </div>
    {:else if state === 'connecting'}
      <div class="msg">
        <p>{$t('term.connectingTo', { title: tab.title })}</p>
      </div>
    {/if}
  </div>
</div>

<style>
  .vnc {
    flex: 1;
    min-height: 0;
    min-width: 0;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .bar {
    display: flex;
    justify-content: flex-end;
    padding: 2px 6px;
    background: var(--bg-2);
    border-bottom: 1px solid var(--border);
  }
  .stage {
    flex: 1;
    min-height: 0;
    min-width: 0;
    display: flex;
    position: relative;
  }
  .vnc-screen {
    position: absolute;
    inset: 0;
  }
  .vnc-screen.hidden {
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
</style>
