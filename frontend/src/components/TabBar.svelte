<script lang="ts">
  import { t as tl, tr } from '../lib/i18n'
  import { tabTitle } from '../lib/tabtitle'
  import { get } from 'svelte/store'
  import Icon from '../lib/Icon.svelte'
  import claudeIcon from '../assets/claude-code-color.svg'
  import ContextMenu, { type MenuItem } from './ContextMenu.svelte'
  import {
    tabs,
    activeTabId,
    closeTab,
    connections,
    isAppTab,
    isServerPinned,
    kindIcon,
    LOCAL,
    openLocal,
    openPorts,
    openTab,
    pinnedDirs,
    snippetsRequest,
    saveTextRequest,
    broadcastDialogOpen,
    toggleShare,
    togglePinnedDir,
    toggleServerPin,
    type Tab,
  } from '../lib/stores'
  import {
    applyDrop,
    focusedPane,
    paneRects,
    splitWith,
    tabDrag,
    zoneAt,
    type DropTarget,
    type PaneNode,
    type Side,
  } from '../lib/layout'
  import { keys } from '../lib/platform'
import { contextMenuAt, isMenuKey } from '../lib/treenav'

  // Barra de pestañas de un panel del mosaico.
  export let pane: PaneNode
  export let focused: boolean

  let menu: { x: number; y: number; items: MenuItem[] } | null = null

  // Un editor duplicado abriría el mismo archivo dos veces, y los reenvíos de un servidor son de una sola pestaña.
  // Las pestañas de osat (ajustes, importar…) tampoco se duplican, y no tienen servidor.
  const single = (t: Tab) => t.kind === 'editor' || t.kind === 'ports' || isAppTab(t)

  $: paneTabs = pane.tabs.map((id) => $tabs.find((t) => t.id === id)).filter((t): t is Tab => !!t)
  $: insertAt = $tabDrag?.target && 'bar' in $tabDrag.target && $tabDrag.target.paneId === pane.id ? $tabDrag.target.index : -1

  const isLocalTerm = (t: Tab) => t.kind === 'terminal' && t.connId === LOCAL

  function duplicate(t: Tab, kind = t.kind) {
    if (t.kind === 'local') return openLocal(t.cwd)
    // Terminal de este equipo: otra igual (misma shell y carpeta de arranque), sin sesión que reutilizar.
    if (isLocalTerm(t)) return openTab({ kind: 'terminal', connId: t.connId, title: t.title, shell: t.shell, initialDir: t.initialDir })
    // Repositorio, navegador y sitio web: otro igual, con la misma carpeta o dirección.
    if (t.kind === 'repo' || t.kind === 'browser' || t.kind === 'site') {
      return openTab({ kind, connId: t.connId, title: t.title, color: t.color, fromSession: t.sessionId, path: t.path, url: t.url, initialDir: t.initialDir })
    }
    return openTab({ kind, connId: t.connId, title: t.title, color: t.color, fromSession: t.sessionId })
  }

  // Dividir: si el panel tiene más pestañas, esta pasa al panel nuevo; si es
  // la única, se abre una igual (misma conexión) en el panel nuevo.
  function split(t: Tab, side: Side) {
    if (pane.tabs.length > 1) splitWith(t.id, pane.id, side)
    else if (!single(t)) splitWith(duplicate(t), pane.id, side)
  }

  function showMenu(e: MouseEvent, t: Tab) {
    e.preventDefault()
    const canSplit = pane.tabs.length > 1 || !single(t)
    menu = {
      x: e.clientX,
      y: e.clientY,
      items: [
        { label: tr('tabs.splitRight'), icon: 'splitRight', disabled: !canSplit, action: () => split(t, 'right') },
        { label: tr('tabs.splitDown'), icon: 'splitDown', disabled: !canSplit, action: () => split(t, 'bottom') },
        { separator: true, label: '' },
        ...(single(t) ? [] : [{ label: tr('tabs.duplicate'), icon: 'copy', action: () => duplicate(t) }]),
        ...(t.kind === 'terminal'
          ? [{ label: 'Snippets…', icon: 'file', hint: keys('Ctrl+Shift+S'), action: () => (activeTabId.set(t.id), snippetsRequest.set(t.id)) }]
          : []),
        ...(t.kind === 'terminal' ? [{ label: 'Broadcast…', icon: 'terminal', hint: keys('Ctrl+Shift+M'), action: () => broadcastDialogOpen.set(true) }] : []),
        ...(t.kind === 'terminal' ? [{ label: tr('term.saveText'), icon: 'download', action: () => saveTextRequest.set(t.id) }] : []),
        ...(t.kind === 'rdp' || t.kind === 'vnc' || t.kind === 'local' || t.kind === 'site' || t.connId === LOCAL || isAppTab(t)
          ? []
          : [
              {
                label: t.kind === 'terminal' ? tr('tabs.openSftpHere') : tr('tabs.openTerminalHere'),
                icon: t.kind === 'terminal' ? 'folder' : 'terminal',
                action: () => duplicate(t, t.kind === 'terminal' ? 'sftp' : 'terminal'),
              },
              ...(t.kind === 'ports'
                ? []
                : [{ label: tr('tabs.portsHere'), icon: 'ports', action: () => openPorts({ id: t.connId, name: t.title, color: t.color }, t.sessionId) }]),
            ]),
        { separator: true, label: '' },
        { label: tr('common.close'), icon: 'x', action: () => closeTab(t.id) },
        {
          label: tr('tabs.closeOthers'),
          action: () => pane.tabs.filter((id) => id !== t.id).forEach((id) => closeTab(id)),
        },
        { label: tr('tabs.closeAll'), danger: true, action: () => [...pane.tabs].forEach((id) => closeTab(id)) },
      ],
    }
  }

  // ---------- arrastrar ----------
  // Soltar sobre otra barra mueve la pestaña ahí (en esa posición); sobre el
  // cuerpo de un panel: en el centro la mueve a ese panel, en un borde lo divide.
  let pressed: { tabId: string; x: number; y: number } | null = null

  function down(e: MouseEvent, t: Tab) {
    if (e.button === 1) {
      e.preventDefault()
      closeTab(t.id)
      return
    }
    if (e.button !== 0) return
    activeTabId.set(t.id)
    pressed = { tabId: t.id, x: e.clientX, y: e.clientY }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  // Flechas izquierda/derecha, Inicio y Fin mueven el foco entre las pestañas del panel (Enter o Espacio activan);
  // Menú o Shift+F10 abren su menú contextual.
  function tabKey(e: KeyboardEvent, t: Tab, i: number) {
    if (e.target !== e.currentTarget) return
    if (isMenuKey(e)) {
      e.preventDefault()
      return contextMenuAt(e.currentTarget as HTMLElement)
    }
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      return activeTabId.set(t.id)
    }
    const to = e.key === 'ArrowLeft' ? i - 1 : e.key === 'ArrowRight' ? i + 1 : e.key === 'Home' ? 0 : e.key === 'End' ? paneTabs.length - 1 : -1
    if (to < 0 || to >= paneTabs.length) return
    e.preventDefault()
    ;(e.currentTarget as HTMLElement).parentElement?.querySelector<HTMLElement>(`[data-tab-index="${to}"]`)?.focus()
  }

  function move(e: MouseEvent) {
    if (!pressed) return
    if (!get(tabDrag) && Math.hypot(e.clientX - pressed.x, e.clientY - pressed.y) < 6) return
    tabDrag.set({ tabId: pressed.tabId, x: e.clientX, y: e.clientY, target: targetAt(e.clientX, e.clientY) })
  }

  function up() {
    const d = get(tabDrag)
    pressed = null
    tabDrag.set(null)
    window.removeEventListener('mousemove', move)
    window.removeEventListener('mouseup', up)
    if (d?.target) applyDrop(d.tabId, d.target)
  }

  function targetAt(x: number, y: number): DropTarget | null {
    const bar = (document.elementFromPoint(x, y) as HTMLElement | null)?.closest<HTMLElement>('[data-pane-bar]')
    if (bar) {
      const els = [...bar.querySelectorAll<HTMLElement>('[data-tab-index]')]
      const hit = els.find((el) => {
        const r = el.getBoundingClientRect()
        return x < r.left + r.width / 2
      })
      return { paneId: bar.dataset.paneBar!, bar: true, index: hit ? Number(hit.dataset.tabIndex) : els.length }
    }
    const host = document.querySelector('.views')?.getBoundingClientRect()
    if (!host) return null
    const rx = x - host.left
    const ry = y - host.top
    for (const [paneId, r] of Object.entries(get(paneRects))) {
      if (rx >= r.x && rx < r.x + r.w && ry >= r.y && ry < r.y + r.h) return { paneId, zone: zoneAt(r, rx, ry) }
    }
    return null
  }
</script>

<div
  class="bar"
  role="tablist"
  tabindex="-1"
  class:focused
  class:dragging={!!$tabDrag}
  data-pane-bar={pane.id}
  on:mousedown|self={() => focusedPane.set(pane.id)}
>
  {#each paneTabs as t, i (t.id)}
    <div
      class="tab"
      role="tab"
      aria-selected={t.id === pane.active}
      tabindex={t.id === pane.active || (i === 0 && !pane.tabs.includes(pane.active ?? '')) ? 0 : -1}
      on:keydown={(e) => tabKey(e, t, i)}
      class:active={t.id === pane.active}
      class:insert={i === insertAt}
      class:lifted={$tabDrag?.tabId === t.id}
      class:down={t.status === 'closed' || t.status === 'error'}
      data-tab-index={i}
      title={t.path ?? tabTitle($tl, t)}
      on:mousedown={(e) => down(e, t)}
      on:contextmenu={(e) => showMenu(e, t)}
    >
      <span class="accent" style="background:{t.color || 'transparent'}" />
      <span class="kind"><Icon name={kindIcon[t.kind]} size={13} /></span>
      <span class="title">{tabTitle($tl, t)}{t.dirty ? ' ●' : ''}</span>
      {#if t.sudo}<span class="root" title={$tl('tabs.rootTip')}>root</span>{/if}
      {#if t.kind === 'ports' && t.forwarding}
        <span class="live" title={$tl(t.forwarding === 1 ? 'tabs.forwards.one' : 'tabs.forwards.other', { count: t.forwarding })}>{t.forwarding}</span>
      {/if}
      {#if t.kind === 'local'}
        {@const dir = t.cwd && t.cwd !== '::drives' ? t.cwd : ''}
        {@const pinned = !!dir && $pinnedDirs.includes(dir)}
        <button
          class="icon pin"
          tabindex="-1"
          class:pinned
          disabled={!dir}
          title={pinned ? $tl('tabs.unpinLocal') : dir ? $tl('tabs.pinHere') : $tl('tabs.pinNeedFolder')}
          aria-label={pinned ? $tl('tabs.unpinLocal') : $tl('tabs.pinHere')}
          aria-pressed={pinned}
          on:mousedown|stopPropagation
          on:click|stopPropagation={() => togglePinnedDir(dir)}
        >
          <Icon name="pin" size={12} filled={pinned} />
        </button>
      {/if}
      {#if t.kind === 'sftp'}
        {@const dir = t.cwd ?? ''}
        {@const pinned = !!dir && $connections.some((c) => c.id === t.connId && c.pins?.some((x) => x.path === dir))}
        <button
          class="icon pin"
          tabindex="-1"
          class:pinned
          disabled={!dir}
          title={pinned ? $tl('tabs.unpinServer') : dir ? $tl('tabs.pinHere') : $tl('tabs.pinNeedFolder')}
          aria-label={pinned ? $tl('tabs.unpinServer') : $tl('tabs.pinHere')}
          aria-pressed={pinned}
          on:mousedown|stopPropagation
          on:click|stopPropagation={() => toggleServerPin(t.connId, dir)}
        >
          <Icon name="pin" size={12} filled={pinned} />
        </button>
      {/if}
      {#if t.kind === 'terminal'}
        <button
          class="icon share"
          tabindex="-1"
          aria-label={t.shared ? $tl('tabs.unshare') : $tl('tabs.share')}
          class:shared={t.shared}
          disabled={!t.sessionId}
          title={t.shared
            ? $tl('tabs.sharedTip')
            : isLocalTerm(t)
              ? $tl('tabs.shareLocalTip')
              : $tl('tabs.shareTip')}
          aria-pressed={!!t.shared}
          on:mousedown|stopPropagation
          on:click|stopPropagation={() => toggleShare(t)}
        >
          <img src={claudeIcon} alt="" width="16" height="16" />
        </button>
      {/if}
      <button
        class="icon close"
        tabindex="-1"
        title={$tl('tabs.closeTip', { keys: keys('Ctrl+Shift+W') })}
        aria-label={$tl('common.close')}
        on:mousedown|stopPropagation
        on:click|stopPropagation={() => closeTab(t.id)}
      >
        <Icon name="x" size={12} />
      </button>
    </div>
  {/each}
  {#if insertAt === paneTabs.length}<span class="insert-end" />{/if}
</div>

{#if menu}
  <ContextMenu {...menu} on:close={() => (menu = null)} />
{/if}

<style>
  /* Las pestañas viven en el marco; la activa se funde con la pantalla de abajo. */
  .bar {
    display: flex;
    gap: 2px;
    background: var(--rail);
    box-shadow: inset 0 -1px 0 var(--border);
    min-height: 36px;
    padding: 4px 6px 0;
    overflow-x: auto;
    overflow-y: hidden;
  }
  .bar::-webkit-scrollbar {
    height: 3px;
  }
  .bar.dragging {
    cursor: grabbing;
  }
  .tab {
    position: relative;
    display: flex;
    align-items: center;
    gap: 7px;
    padding: 0 6px 0 12px;
    min-width: 120px;
    max-width: 220px;
    overflow: hidden;
    color: var(--text-2);
    cursor: default;
    flex: none;
  }
  .tab:hover {
    background: var(--bg-2);
    color: var(--text);
  }
  .tab.active {
    background: var(--bg);
    color: var(--text);
  }
  .accent {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 2px;
  }
  .bar:not(.focused) .tab.active {
    color: var(--text-2);
  }
  .bar.focused .tab.active .accent {
    background: var(--signal) !important;
  }
  /* La pestaña que se está arrastrando queda atenuada en su lugar; la que sigue al mouse es la flotante. */
  .tab.lifted {
    opacity: 0.4;
  }
  .tab.insert {
    box-shadow: inset 2px 0 0 var(--text);
  }
  .insert-end {
    width: 2px;
    background: var(--text);
    flex: none;
  }
  .kind {
    display: inline-flex;
    color: var(--text-3);
  }
  .tab.active .kind {
    color: var(--text-2);
  }
  .title {
    flex: 1;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* Puertos: cuántos reenvíos andan (verde). */
  .live {
    flex: none;
    min-width: 16px;
    padding: 0 5px;
    font-size: 10px;
    line-height: 16px;
    text-align: center;
    color: var(--bg);
    background: var(--success);
    border-radius: 8px;
  }
  /* Editor abierto como root (sudo). Igual que en EditorView.svelte. */
  .root {
    flex: none;
    padding: 0 5px;
    font-size: 10px;
    font-weight: 600;
    line-height: 16px;
    color: var(--bg);
    background: var(--danger);
    border-radius: 3px;
  }
  /* Sesión cerrada o con error: sin punto de estado, el título en rojo. */
  .tab.down .title {
    color: var(--danger);
  }
  .tab button.icon {
    padding: 2px 4px;
  }
  .close,
  .pin,
  .share {
    opacity: 0;
  }
  .tab:hover .close,
  .tab.active .close,
  .tab:hover .pin,
  .tab.active .pin,
  .tab:hover .share,
  .tab.active .share,
  .pin.pinned,
  .share.shared {
    opacity: 1;
  }
  .pin.pinned {
    color: var(--text);
  }
  /* El logo de Claude Code: en gris mientras no se comparte, en color (y con fondo) cuando sí. */
  .share img {
    display: block;
    filter: grayscale(1) opacity(0.55);
    transition: filter 0.12s;
  }
  .share:hover:not(:disabled) img,
  .share.shared img {
    filter: none;
  }
  .share.shared {
    background: var(--bg-4);
  }
</style>
