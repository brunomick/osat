<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import Sidebar from './components/Sidebar.svelte'
  import LayoutView from './components/LayoutView.svelte'
  import TerminalView from './components/TerminalView.svelte'
  import SftpView from './components/SftpView.svelte'
  import EditorView from './components/EditorView.svelte'
  import RdpView from './components/RdpView.svelte'
  import VncView from './components/VncView.svelte'
  import RepoView from './components/RepoView.svelte'
  import BrowserView from './components/BrowserView.svelte'
  import SiteView from './components/SiteView.svelte'
  import PortsView from './components/PortsView.svelte'
  import Icon from './lib/Icon.svelte'
  import LocalView from './components/LocalView.svelte'
  import Transfers from './components/Transfers.svelte'
  import Dialogs from './components/Dialogs.svelte'
  import ConnectionView from './components/ConnectionView.svelte'
  import AboutDialog from './components/AboutDialog.svelte'
  import UpdateDialog from './components/UpdateDialog.svelte'
  import WhatsNewView from './components/WhatsNewView.svelte'
  import { checkForUpdates, showWhatsNew, updateUi } from './lib/updates'
  import SettingsView from './components/SettingsView.svelte'
  import SnippetsView from './components/SnippetsView.svelte'
  import LockScreen from './components/LockScreen.svelte'
  import ImportView from './components/ImportView.svelte'
  import BroadcastDialog from './components/BroadcastDialog.svelte'
  import ExportDialog from './components/ExportDialog.svelte'
  import Toasts from './components/Toasts.svelte'
  import TitleBar from './components/TitleBar.svelte'
  import { t as tl, tr } from './lib/i18n'
  import { api, errorText, on, onFileDrop, type AuthPrompt, type Transfer } from './lib/api'
  import { questionsDialog } from './lib/dialog'
  import { focusedTabs, focusedPane, layout, paneRects, panes, tabDrag } from './lib/layout'
  import { broadcast, isActive as broadcastActive, prune as pruneBroadcast, stop as stopBroadcast } from './lib/broadcast'
  import { fileDrag } from './lib/filedrag'
  import { overlayHolder } from './lib/overlay'
  import { mountable } from './lib/workspace'
  import { startSession } from './lib/workspaces'
  import { startIdleTimer } from './lib/idle'
  import { lockNow, lockState, locked, refreshLock, untilUnlocked } from './lib/lock'
  import { isAndroid, keys } from './lib/platform'
  import { backFrom, mobileScreen } from './lib/mobile'
  import MobileHome from './components/mobile/MobileHome.svelte'
  import MobileSessions from './components/mobile/MobileSessions.svelte'
  import MobileTabHeader from './components/mobile/MobileTabHeader.svelte'
  import {
    tabs,
    activeTab,
    activeTabId,
    isAppTab,
    aboutOpen,
    exportGroup,
    broadcastDialogOpen,
    loadBridge,
    loadSettings,
    openAppTab,
    openConnForm,
    patchTab,
    reloadConnections,
    reloadSites,
    browserSupported,
    openBlankSite,
    upsertTransfer,
    uploadFinished,
    downloadFinished,
    transfersOpen,
    closeTab,
    cycleTab,
    dropHandlers,
    kindIcon,
    sidebarOpen,
    settings,
    toast,
    type Tab,
  } from './lib/stores'

  // El ancho de la barra lateral se recuerda entre aperturas.
  const SIDEBAR_W_KEY = 'osat.sidebarWidth'
  let sidebarWidth = (() => {
    try {
      const w = Number(localStorage.getItem(SIDEBAR_W_KEY))
      return w ? Math.min(480, Math.max(180, w)) : 260
    } catch {
      return 260
    }
  })()
  $: try {
    localStorage.setItem(SIDEBAR_W_KEY, String(sidebarWidth))
  } catch {
    // sin almacenamiento: vale hasta cerrar la app
  }

  // Dónde está cada pestaña: en qué panel, si es la visible de ese panel y si
  // además ese panel es el enfocado (el que recibe el teclado).
  // En Android solo se ve (y tiene el foco) la pestaña elegida, y solo cuando se la muestra.
  $: placement = (
    android
      ? Object.fromEntries($tabs.map((t) => [t.id, { pane: '', visible: $mobileScreen === 'tab' && t.id === $activeTabId, focused: $mobileScreen === 'tab' && t.id === $activeTabId }]))
      : Object.fromEntries(
          panes($layout).flatMap((p) =>
            p.tabs.map((id) => [id, { pane: p.id, visible: p.active === id, focused: p.active === id && p.id === $focusedPane }]),
          ),
        )
  ) as Record<string, { pane: string; visible: boolean; focused: boolean }>

  // Pestañas restauradas de un espacio de trabajo: su vista se monta (y conecta) recién la primera vez que
  // se ve, así abrir uno con 8 servidores no pide 8 contraseñas juntas. Ver mountable en lib/workspace.ts.
  const starting = new Set<string>() // montadas que todavía hacen su primera conexión
  $: {
    for (const id of starting) if ($tabs.find((t) => t.id === id)?.status !== 'connecting') starting.delete(id)
    const visible = new Set(Object.keys(placement).filter((id) => placement[id].visible))
    for (const m of mountable($tabs, visible, starting)) {
      if (!m.fromSession) starting.add(m.id)
      patchTab(m.id, { deferConnect: undefined, ...(m.fromSession ? { fromSession: m.fromSession } : {}) })
    }
  }

  // Mientras se arrastra una pestaña o un archivo, la zona de soltar y la pestaña flotante van en HTML: la vista
  // nativa de un sitio (que se dibuja encima) se oculta.
  const holdDrag = overlayHolder()
  $: holdDrag(!!$tabDrag || !!$fileDrag)
  onDestroy(() => holdDrag(false))

  // Una terminal del broadcast que se cierra o se cae sale sola (y con menos de dos se detiene).
  $: pruneBroadcast($tabs)

  // Zona resaltada mientras se arrastra una pestaña sobre un panel.
  $: dropRect = (() => {
    const t = $tabDrag?.target
    if (!t || 'bar' in t) return null
    const r = $paneRects[t.paneId]
    if (!r) return null
    const half = { x: r.x, y: r.y, w: r.w, h: r.h }
    if (t.zone === 'left' || t.zone === 'right') half.w = r.w / 2
    if (t.zone === 'top' || t.zone === 'bottom') half.h = r.h / 2
    if (t.zone === 'right') half.x = r.x + r.w / 2
    if (t.zone === 'bottom') half.y = r.y + r.h / 2
    return half
  })()

  // Bloqueo por inactividad: solo corre con el bloqueo activado y osat desbloqueado, y se reinicia al cambiar el ajuste.
  let stopIdle: (() => void) | undefined
  let idleKey = ''
  function restartIdle(active: boolean, minutes: number) {
    const key = `${active}:${minutes}`
    if (key === idleKey) return
    idleKey = key
    stopIdle?.()
    stopIdle = active ? startIdleTimer(minutes, () => lockNow().catch(() => {})) : undefined
  }
  $: restartIdle(!!$lockState?.enabled && !$locked, $settings.general.lockIdleMinutes)

  onMount(() => {
    // Antes que nada: si osat está bloqueado (Go lo sabe aunque se recargue el frontend), startSession espera al desbloqueo.
    refreshLock().catch((e) => toast(tr('lock.statusError', { error: errorText(e) }), 'error'))
    const conns = reloadConnections().then(
      () => true,
      (e) => (toast(String(e), 'error'), false),
    )
    loadBridge().catch(() => {})
    api.browserSupported().then((v) => browserSupported.set(v), () => {})
    reloadSites().catch(() => {}) // la barra lateral los muestra aunque no se haya abierto ningún sitio
    const cfg = loadSettings().catch((e) => toast(tr('app.settingsError', { error: errorText(e) }), 'error'))
    // Con las conexiones y los ajustes leídos: la última sesión (si el ajuste está activo) y empezar a guardarla.
    const session = Promise.all([conns, cfg]).then(([ok]) => startSession(ok))
    // Con osat desbloqueado y la sesión ya restaurada (si se abre antes una pestaña, la sesión no se restaura):
    // las novedades si se acaba de actualizar y, si no, buscar una versión nueva (si el ajuste está activo).
    if (!isAndroid())
      session
        .then(untilUnlocked)
        .then(async () => {
          if (!(await showWhatsNew()) && $settings.general.autoUpdate) checkForUpdates(false)
        })

    // Al apagar el puente se deja de compartir toda terminal.
    const offUnshared = on('bridge:unshared', () => tabs.update((ts) => ts.map((t) => (t.shared ? { ...t, shared: false } : t))))

    const off = on('transfer:update', (t: Transfer) => {
      upsertTransfer(t)
      if (t.status === 'queued') transfersOpen.set(true)
      if (t.status === 'done' && (t.direction === 'up' || t.direction === 'both')) uploadFinished.set({ sessionId: t.sessionId, at: Date.now() })
      if (t.status === 'done' && t.direction === 'down') downloadFinished.set({ target: t.target, at: Date.now() })
      if (t.status === 'error') toast(tr('app.transferError', { name: t.name, error: t.error ?? '' }), 'error', 6000)
    })

    // Preguntas keyboard-interactive del servidor (OTP, 2FA...) durante la conexión.
    const offPrompt = on('auth:prompt', async (p: AuthPrompt) => {
      const answers = await questionsDialog({
        title: tr('app.authTitle', { host: p.host }),
        message: [p.name, p.instruction].filter(Boolean).join('\n'),
        questions: p.questions,
      })
      api.answerAuthPrompt(p.id, answers).catch(() => {})
    })

    onFileDrop((_x, _y, paths) => {
      if ($locked) return
      const id = $activeTabId
      const h = id ? dropHandlers.get(id) : undefined
      if (h) h(paths)
    })

    // Atajos globales (fase de captura para que xterm no se los coma).
    const onKey = (e: KeyboardEvent) => {
      if ($locked) return // bloqueado: ningún atajo (LockScreen frena el resto de las teclas)
      // Con el foco en un escritorio remoto las combinaciones son del servidor (como en mstsc).
      if (document.activeElement?.tagName === 'IRON-REMOTE-DESKTOP' || document.activeElement?.closest('.vnc-screen')) return
      const mod = e.ctrlKey || e.metaKey
      if (mod && e.key === 'Tab') {
        cycleTab(e.shiftKey ? -1 : 1)
      } else if (mod && e.shiftKey && e.key.toLowerCase() === 'w') {
        if ($activeTabId) closeTab($activeTabId)
      } else if (mod && e.shiftKey && e.key.toLowerCase() === 'n') {
        openConnForm()
      } else if (mod && e.shiftKey && e.key.toLowerCase() === 'b') {
        sidebarOpen.update((v) => !v) // (Ctrl+B solo sería el prefijo de tmux)
      } else if (mod && e.shiftKey && e.key.toLowerCase() === 't' && $browserSupported && $settings.general.webBrowser) {
        openBlankSite() // (Ctrl+T solo es de la shell: transponer caracteres)
      } else if (mod && e.shiftKey && e.code === 'KeyM') {
        if (broadcastActive()) stopBroadcast()
        else broadcastDialogOpen.set(true)
      } else if (mod && !e.shiftKey && !e.altKey && e.key === ',') {
        openAppTab('settings')
      } else if (e.altKey && !mod && /^Digit[1-9]$/.test(e.code)) {
        const id = focusedTabs()[Number(e.code.slice(5)) - 1] // pestañas del panel enfocado (e.code: en macOS Option+número cambia e.key)
        if (id) activeTabId.set(id)
      } else {
        return
      }
      e.preventDefault()
      e.stopPropagation()
    }
    window.addEventListener('keydown', onKey, true)

    return () => {
      stopIdle?.()
      off()
      offUnshared()
      offPrompt()
      window.removeEventListener('keydown', onKey, true)
    }
  })

  // Flechas izquierda/derecha: mueven el divisor de a 16 px.
  function resizeKey(e: KeyboardEvent) {
    const d = e.key === 'ArrowLeft' ? -16 : e.key === 'ArrowRight' ? 16 : 0
    if (!d || !$sidebarOpen) return
    e.preventDefault()
    sidebarWidth = Math.min(480, Math.max(180, sidebarWidth + d))
  }

  function startResize(e: MouseEvent) {
    if (!$sidebarOpen) return
    const startX = e.clientX
    const startW = sidebarWidth
    const move = (ev: MouseEvent) => {
      sidebarWidth = Math.min(480, Math.max(180, startW + ev.clientX - startX))
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  // Android: una pantalla a la vez (lib/mobile.ts). Una pestaña nueva se muestra sola (no las que
  // vuelven de la última sesión, que esperan en Sesiones). Ajustes, formularios y demás pestañas de
  // osat (isAppTab) no son sesiones: se cierran al salir de ellas. Si se cierra la que se estaba
  // viendo, se vuelve a Sesiones (o a Inicio si era de osat o no queda ninguna).
  const android = isAndroid()
  let knownTabs = new Set<string>()
  let shown: Tab | null = null
  $: if (android) {
    if ($tabs.some((t) => !knownTabs.has(t.id) && !t.deferConnect)) mobileScreen.set('tab')
    knownTabs = new Set($tabs.map((t) => t.id))
    const cur = $mobileScreen === 'tab' ? $activeTab : null
    const gone = !!shown && !knownTabs.has(shown.id)
    if (shown && shown.id !== cur?.id) {
      if (gone) {
        if ($mobileScreen === 'tab') mobileScreen.set(backFrom(shown, $tabs))
      } else if (isAppTab(shown)) closeTab(shown.id)
    }
    shown = gone ? null : cur
    if ($mobileScreen === 'tab' && !$activeTab) mobileScreen.set('home')
  }
  // La notificación de Android que mantiene vivas las sesiones mientras haya alguna abierta.
  $: openSessions = $tabs.filter((t) => !isAppTab(t)).length
  $: if (android) (window as any).OsatAndroid?.sessions?.(openSessions)

  // Atrás de Android (MainActivity.kt): si hay un diálogo, menú u hoja abierta la cierra (como Esc, que
  // llega a donde está el foco); si no, de una pestaña a Sesiones (o a Inicio si es de osat) y de ahí
  // a Inicio; desde Inicio, la app va a segundo plano.
  if (android)
    (window as any).__osatBack = () => {
      if (document.querySelector('[aria-modal="true"], [role="menu"]')) {
        ;(document.activeElement ?? document.body).dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
        return true
      }
      if ($mobileScreen === 'home') return false
      mobileScreen.set($mobileScreen === 'tab' && $activeTab ? backFrom($activeTab, $tabs) : 'home')
      return true
    }
</script>

<div class="app" class:android>
  {#if !android}
    <TitleBar />
  {/if}
  <div class="shell" style="--sidebar-w: {$sidebarOpen ? sidebarWidth : 43}px; --resizer-w: {$sidebarOpen ? 4 : 0}px">
    {#if !android}
      <!-- Oculta (display: none) en vez de desmontada: conserva grupos abiertos, búsqueda y menús. -->
      <div class="side" class:off={!$sidebarOpen}>
        <Sidebar on:new={() => openConnForm()} on:edit={(e) => openConnForm(e.detail)} />
      </div>
      {#if !$sidebarOpen}
        <div class="rail">
          <button class="icon" title={$tl('app.showSidebarTip', { keys: keys('Ctrl+Shift+B') })} aria-label={$tl('app.showSidebar')} on:click={() => sidebarOpen.set(true)}>
            <Icon name="menu" size={15} />
          </button>
        </div>
      {/if}
      <div
        class="resizer"
        role="slider"
        aria-orientation="vertical"
        aria-label={$tl('app.sidebarWidth')}
        aria-valuenow={sidebarWidth}
        aria-valuemin={180}
        aria-valuemax={480}
        tabindex={$sidebarOpen ? 0 : -1}
        on:mousedown={startResize}
        on:keydown={resizeKey}
      />
    {/if}

    <main>
      {#if android && $mobileScreen === 'tab' && $activeTab}
        <MobileTabHeader tab={$activeTab} />
      {/if}
      {#if $broadcast.targets.length}
        <div class="bcbar" role="status">
          <span>{$tl('app.broadcasting', { count: $broadcast.targets.length })}</span>
          <span class="hint">{$tl('app.broadcastStop', { keys: keys('Ctrl+Shift+M') })}</span>
          <button on:click={stopBroadcast}>{$tl('app.stop')}</button>
        </div>
      {/if}
      <div class="views">
        {#if !android}
          <LayoutView node={$layout} />
        {/if}
        <!-- Las vistas se montan una sola vez (por id) y se posicionan sobre el
             cuerpo de su panel: así mover una pestaña de panel no corta su sesión.
             En Android no hay paneles: la visible ocupa todo. -->
        {#each $tabs as tab (tab.id)}
          {@const place = placement[tab.id]}
          {@const rect = android ? { x: 0, y: 0, w: 0, h: 0 } : place ? $paneRects[place.pane] : undefined}
          <div
            class="view"
            role="presentation"
            class:hidden={!place?.visible || !rect}
            class:full={android}
            style={rect && !android ? `left:${rect.x}px;top:${rect.y}px;width:${rect.w}px;height:${rect.h}px` : ''}
            on:mousedown|capture={() => place?.visible && activeTabId.set(tab.id)}
          >
            {#if tab.deferConnect}
              {#if place?.visible}
                <div class="waiting">{$tl('app.waitingOther', { title: tab.title })}</div>
              {/if}
            {:else if tab.kind === 'terminal'}
              <TerminalView {tab} active={!!place?.visible} focused={!!place?.focused} />
            {:else if tab.kind === 'rdp'}
              <RdpView {tab} active={!!place?.visible} />
            {:else if tab.kind === 'vnc'}
              <VncView {tab} />
            {:else if tab.kind === 'local'}
              <LocalView {tab} focused={!!place?.focused} />
            {:else if tab.kind === 'editor'}
              <EditorView {tab} focused={!!place?.focused} />
            {:else if tab.kind === 'repo'}
              <RepoView {tab} focused={!!place?.focused} />
            {:else if tab.kind === 'browser'}
              <BrowserView {tab} />
            {:else if tab.kind === 'site'}
              <SiteView {tab} active={!!place?.visible} />
            {:else if tab.kind === 'ports'}
              <PortsView {tab} />
            {:else if tab.kind === 'settings'}
              <SettingsView {tab} />
            {:else if tab.kind === 'import'}
              <ImportView {tab} />
            {:else if tab.kind === 'connection'}
              <ConnectionView {tab} />
            {:else if tab.kind === 'whatsnew'}
              <WhatsNewView {tab} />
            {:else if tab.kind === 'snippets'}
              <SnippetsView {tab} />
            {:else}
              <SftpView {tab} active={!!place?.visible} focused={!!place?.focused} />
            {/if}
          </div>
        {/each}
        {#if dropRect}
          <div class="drop" style="left:{dropRect.x}px;top:{dropRect.y}px;width:{dropRect.w}px;height:{dropRect.h}px" />
        {/if}
        {#if android && $mobileScreen !== 'tab'}
          <div class="mscreen">
            {#if $mobileScreen === 'home'}
              <MobileHome />
            {:else}
              <MobileSessions />
            {/if}
          </div>
        {/if}
      </div>
      <Transfers />
      {#if android && $mobileScreen !== 'tab'}
        <nav class="mnav">
          <button class:on={$mobileScreen === 'home'} on:click={() => mobileScreen.set('home')}>
            <Icon name="home" size={20} />{$tl('mobile.home')}
          </button>
          <button class:on={$mobileScreen === 'sessions'} on:click={() => mobileScreen.set('sessions')}>
            <Icon name="server" size={20} />{$tl('mobile.sessions')}{openSessions ? ` · ${openSessions}` : ''}
          </button>
          <button on:click={() => (openAppTab('settings'), mobileScreen.set('tab'))}>
            <Icon name="gear" size={20} />{$tl('settings.title')}
          </button>
        </nav>
      {/if}
    </main>
  </div>
</div>

{#if $aboutOpen}
  <AboutDialog />
{/if}
{#if $updateUi}
  <UpdateDialog />
{/if}
{#if $broadcastDialogOpen}
  <BroadcastDialog />
{/if}
{#if $exportGroup !== null}
  <ExportDialog group={$exportGroup} />
{/if}
{#if $tabDrag}
  <!-- La pestaña flotante: una réplica chica de la pestaña que sigue al mouse mientras se la arrastra. -->
  {@const dragged = $tabs.find((t) => t.id === $tabDrag.tabId)}
  {#if dragged}
    <div class="tab-ghost" style="left: {$tabDrag.x + 14}px; top: {$tabDrag.y + 12}px; --c: {dragged.color || 'var(--accent)'}">
      <Icon name={kindIcon[dragged.kind]} size={14} />
      <span>{dragged.title}{dragged.dirty ? ' ●' : ''}</span>
    </div>
  {/if}
{/if}
{#if $fileDrag}
  <div class="drag-ghost" style="left: {$fileDrag.x + 14}px; top: {$fileDrag.y + 14}px">{$fileDrag.label}</div>
{/if}
<Dialogs />
<Toasts />
{#if $locked}
  <LockScreen top={30} />
{/if}

<style>
  .app {
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  .shell {
    flex: 1;
    min-height: 0;
    display: grid;
    grid-template-columns: var(--sidebar-w) var(--resizer-w) 1fr;
  }
  .side {
    display: contents;
  }
  .side.off {
    display: none;
  }
  /* Android (lib/mobile.ts): sin barra lateral ni paneles; Inicio y Sesiones van sobre las vistas, con la barra de abajo. */
  .android .shell {
    grid-template-columns: 1fr;
  }
  .view.full {
    inset: 0;
  }
  .mscreen {
    position: absolute;
    inset: 0;
    z-index: 20;
  }
  .mnav {
    flex: none;
    display: flex;
    background: var(--bg-2);
    border-top: 1px solid var(--border);
  }
  .mnav button {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    padding: 8px 0 10px;
    border: 0;
    background: none;
    color: var(--text-3);
    font-size: 12px;
  }
  .mnav button.on {
    color: var(--signal);
  }
  .rail {
    background: var(--bg-2);
    border-right: 1px solid var(--border);
    /* El botón queda donde está en el encabezado de la barra abierta (mismo tope y mismo borde izquierdo), con el mismo margen del lado derecho. */
    padding: 8px 6px 0;
    display: flex;
    flex-direction: column;
    align-items: flex-start;
  }
  .rail .icon {
    padding: 4px 5px; /* igual que en el encabezado de Sidebar */
  }
  .resizer {
    cursor: col-resize;
    background: var(--border);
    opacity: 0.4;
  }
  .resizer:hover {
    opacity: 1;
    background: var(--accent);
  }
  main {
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
  }
  /* Aviso de que lo que se escribe va a varias terminales (mismo color que el borde de cada una). */
  .bcbar {
    flex: none;
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 3px 12px;
    background: var(--warning);
    color: var(--on-accent);
    font-weight: 600;
  }
  .bcbar .hint {
    flex: 1;
    font-weight: 400;
    opacity: 0.75;
  }
  .bcbar button {
    padding: 0 10px;
    background: var(--on-accent);
    border-color: var(--on-accent);
    color: var(--warning);
  }
  .views {
    flex: 1;
    position: relative;
    min-height: 0;
    display: flex;
  }
  .view {
    position: absolute;
    display: flex;
    flex-direction: column;
  }
  /* Pestaña restaurada que espera a que otra del mismo servidor termine de conectar (comparten la conexión). */
  .waiting {
    flex: 1;
    display: grid;
    place-items: center;
    background: var(--bg);
    color: var(--text-3);
    font-size: 12px;
  }
  .view.hidden {
    visibility: hidden;
    pointer-events: none;
  }
  .drag-ghost {
    position: fixed;
    z-index: 1200;
    pointer-events: none;
    padding: 2px 6px;
    background: var(--bg-3);
    border: 1px solid var(--border);
    white-space: nowrap;
  }
  .tab-ghost {
    position: fixed;
    z-index: 1200;
    pointer-events: none;
    display: flex;
    align-items: center;
    gap: 7px;
    max-width: 260px;
    padding: 8px 14px;
    background: var(--bg-3);
    color: var(--text);
    border: 1px solid var(--border);
    border-top: 2px solid var(--c);
    box-shadow: 0 8px 24px var(--shadow-lg);
    transform: rotate(-1.5deg);
  }
  .tab-ghost span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .drop {
    position: absolute;
    z-index: 50;
    pointer-events: none;
    background: var(--drop-tint);
    outline: 1px solid var(--text-2);
    outline-offset: -1px;
  }
</style>
