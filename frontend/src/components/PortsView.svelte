<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { t, tr } from '../lib/i18n'
  import Icon from '../lib/Icon.svelte'
  import ForwardDialog from './ForwardDialog.svelte'
  import { api, clipboardSet, errorText, fmtSize, isServerGone, type Forward, type ForwardState } from '../lib/api'
  import { connectTab, CancelledError } from '../lib/connect'
  import { confirmDialog } from '../lib/dialog'
  import { connections, LOCAL, openBrowser, patchTab, toast, type Tab } from '../lib/stores'

  // Reenvío de puertos de un servidor: cada fila es un puerto de esta PC (solo loopback) que llega, por la
  // conexión SSH, a un puerto del servidor o a un pod/servicio de Kubernetes (con el kubectl del servidor).
  // Los programas de esta PC (DBeaver, redis-cli, el navegador...) se conectan a localhost:PUERTO.
  // Los remotos son al revés: un puerto del servidor que llega a un puerto de esta PC. Los dinámicos son
  // un proxy SOCKS5 en un puerto de esta PC que sale por el servidor.
  // Cerrar la pestaña los detiene.
  export let tab: Tab

  let sessionId: string | null = null
  let connecting = false
  let error = ''
  let loaded = false
  let rules: Forward[] = []
  let states: Record<string, ForwardState> = {}
  let working: Record<string, boolean> = {} // arrancando / deteniendo (el clic ya se hizo)
  let dialog: { initial: Partial<Forward> } | null = null
  let timer: ReturnType<typeof setInterval>

  $: conn = $connections.find((c) => c.id === tab.connId)

  const presets = [
    { name: 'Redis', port: 6379 },
    { name: 'RabbitMQ', port: 5672 },
    { name: 'PostgreSQL', port: 5432 },
  ]

  async function connect() {
    error = ''
    connecting = true
    patchTab(tab.id, { status: 'connecting', sessionId: undefined })
    try {
      const info = await connectTab(tab)
      sessionId = info.id
      patchTab(tab.id, { status: 'ready', sessionId: info.id })
    } catch (e) {
      sessionId = null
      patchTab(tab.id, { status: 'error' })
      error = e instanceof CancelledError ? tr('term.cancelled') : errorText(e)
    } finally {
      connecting = false
    }
  }

  async function loadRules() {
    rules = (await api.forwardList(tab.connId)) ?? []
  }

  async function refresh() {
    try {
      const list = (await api.forwardStates(tab.connId)) ?? []
      states = Object.fromEntries(list.map((s) => [s.id, s]))
      const n = list.filter((s) => s.status !== 'error').length
      if ((tab.forwarding ?? 0) !== n) patchTab(tab.id, { forwarding: n })
    } catch {
      // la app se está cerrando
    }
  }

  async function start(r: Forward) {
    if (!sessionId) await connect()
    if (!sessionId) return
    working = { ...working, [r.id]: true }
    try {
      try {
        await api.forwardStart(tab.id, sessionId, r.id)
      } catch (e) {
        // La conexión con el servidor se cortó: se vuelve a abrir y se reintenta una vez.
        if (!isServerGone(e)) throw e
        await connect()
        if (!sessionId) return
        await api.forwardStart(tab.id, sessionId, r.id)
      }
    } catch (e) {
      await refresh()
      // Si el motivo quedó en la fila, ahí se lee; si no (ya estaba en marcha, puerto repetido...), un aviso.
      if (!states[r.id]?.error) toast(errorText(e), 'error')
    } finally {
      working = { ...working, [r.id]: false }
      refresh()
    }
  }

  async function stop(r: Forward) {
    working = { ...working, [r.id]: true }
    try {
      await api.forwardStop(r.id)
    } finally {
      working = { ...working, [r.id]: false }
      refresh()
    }
  }

  const toggle = (r: Forward) => (states[r.id] && states[r.id].status !== 'error' ? stop(r) : start(r))

  async function remove(r: Forward) {
    const ok = await confirmDialog({
      title: tr('ports.removeTitle'),
      message: tr('ports.removeMsg', { name: r.name }),
      okText: tr('common.delete'),
      danger: true,
    })
    if (!ok) return
    try {
      await api.forwardDelete(r.id)
      await loadRules()
      refresh()
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function saved(e: CustomEvent<{ forward: Forward; isNew: boolean }>) {
    const { forward, isNew } = e.detail
    const wasRunning = !isNew && !!states[forward.id] && states[forward.id].status !== 'error'
    await loadRules()
    if (isNew || wasRunning) start(forward) // un reenvío nuevo arranca solo; uno editado que andaba, vuelve a andar
    else refresh()
  }

  const stopAll = () => rules.forEach((r) => states[r.id] && api.forwardStop(r.id).then(refresh))
  const startAll = () => rules.forEach((r) => (!states[r.id] || states[r.id].status === 'error') && start(r))

  const isSocks = (r: Forward) => r.kind === 'dynamic'
  const copyText = (r: Forward) => (isSocks(r) ? `socks5h://localhost:${r.localPort}` : `localhost:${r.localPort}`)

  async function copy(r: Forward) {
    await clipboardSet(copyText(r))
    toast(tr('ports.copied', { text: copyText(r) }), 'success', 2000)
  }

  const openInBrowser = (r: Forward) => openBrowser({ connId: LOCAL, color: tab.color }, undefined, { url: `localhost:${r.localPort}` })

  const isRemote = (r: Forward) => r.kind === 'remote'
  const source = (r: Forward, tf: typeof tr) => (isRemote(r) ? tf('ports.sourceRemote', { port: r.port }) : isSocks(r) ? tf('ports.socksSource', { port: r.localPort }) : `localhost:${r.localPort}`)
  const target = (r: Forward) =>
    isRemote(r)
      ? `${r.host || 'localhost'}:${r.localPort}`
      : r.resource
        ? `${r.namespace} · ${r.resource}:${r.port}`
        : `${r.host || 'localhost'}:${r.port}`
  const targetTitle = (r: Forward, tf: typeof tr) => (isRemote(r) ? tf('ports.targetRemote') : r.resource ? tf('ports.targetK8s') : tf('ports.targetServer'))

  function stats(r: Forward, s: ForwardState, tf: typeof tr) {
    if (!s.total)
      return isRemote(r)
        ? tf('ports.waitRemote', { port: r.port })
        : isSocks(r)
          ? tf('ports.waitSocks')
          : tf('ports.waitLocal')
    const act = tf(s.active === 1 ? 'ports.active.one' : 'ports.active.other', { count: s.active })
    return tf('ports.stats', { active: act, total: s.total, down: fmtSize(s.bytesIn), up: fmtSize(s.bytesOut) })
  }

  onMount(async () => {
    await connect()
    await loadRules()
    await refresh()
    loaded = true
    timer = setInterval(refresh, 1500)
    if (sessionId) for (const r of rules) if (r.autoStart && !states[r.id]) start(r)
  })
  onDestroy(() => clearInterval(timer))
</script>

<div class="ports">
  <header>
    <div>
      <h1>{$t('ports.title')} <span class="dim">· {conn?.name ?? tab.title}</span></h1>
      <p>{$t('ports.intro')}</p>
    </div>
    <div class="head-actions">
      {#if rules.length > 1}
        <button class="ghost" on:click={startAll}>{$t('ports.startAll')}</button>
        <button class="ghost" on:click={stopAll}>{$t('ports.stopAll')}</button>
      {/if}
      <button class="primary" on:click={() => (dialog = { initial: {} })}><Icon name="plus" size={14} /> {$t('ports.new')}</button>
    </div>
  </header>

  {#if error}
    <div class="note">
      <p>{error}</p>
      <button class="primary" on:click={connect}>{$t('ports.retry')}</button>
    </div>
  {:else if !loaded}
    <div class="note"><p>{connecting ? $t('ports.connecting') : $t('snippets.loading')}</p></div>
  {:else if !rules.length}
    <div class="note">
      <Icon name="ports" size={34} />
      <p class="lead">{$t('ports.empty')}</p>
      <p class="small">
        {$t('ports.emptyHelp')}
      </p>
      <div class="chips">
        {#each presets as p}
          <button class="chip" on:click={() => (dialog = { initial: { name: p.name, port: p.port } })}>{p.name} <span class="mono dim">{p.port}</span></button>
        {/each}
        <button class="chip" on:click={() => (dialog = { initial: {} })}>{$t('ports.other')}</button>
      </div>
    </div>
  {:else}
    <ul>
      {#each rules as r (r.id)}
        {@const s = states[r.id]}
        <li class:err={s?.status === 'error'}>
          <span class="dot {s?.status ?? 'stopped'}" title={s ? (s.status === 'running' ? $t('ports.running') : s.status === 'starting' ? $t('ports.starting') : $t('ports.error')) : $t('ports.stopped')} />
          <div class="main">
            <div class="top">
              <strong>{r.name}</strong>
              <span class="addr mono">{source(r, $t)}</span>
              {#if !isSocks(r)}
                <span class="arrow"><Icon name="ports" size={13} /></span>
                <span class="dest mono" title={targetTitle(r, $t)}>{target(r)}</span>
              {/if}
              {#if isRemote(r)}<span class="tag" title={$t('ports.remoteTagTip')}>{$t('ports.remoteTag')}</span>{/if}
              {#if isRemote(r) && r.bindAll}<span class="tag" title={$t('ports.allIfacesTip')}>{$t('ports.allIfaces')}</span>{/if}
              {#if r.autoStart}<span class="tag" title={$t('ports.autoTip')}>{$t('ports.auto')}</span>{/if}
            </div>
            {#if s?.status === 'error'}
              <div class="line bad">{s.error}</div>
            {:else if s?.status === 'starting' || working[r.id]}
              <div class="line">{$t('ports.starting')}</div>
            {:else if s?.status === 'running'}
              <div class="line">{stats(r, s, $t)}</div>
              {#if s.warning}<div class="line warn">⚠ {s.warning}</div>{/if}
              {#if isSocks(r)}
                <div class="line">
                  {$t('ports.socksA')} <span class="mono">localhost</span> {$t('ports.socksB')} <span class="mono">{r.localPort}</span> {$t('ports.socksC')}
                  <span class="mono">curl --socks5-hostname localhost:{r.localPort} {$t('ports.socksUrl')}</span>. {$t('ports.socksD')}
                </div>
              {/if}
            {:else}
              <div class="line">{$t('ports.stopped')}</div>
            {/if}
          </div>
          <div class="acts">
            <button
              class="switch"
              class:on={s && s.status !== 'error'}
              role="switch"
              aria-checked={!!s && s.status !== 'error'}
              aria-label={s && s.status !== 'error' ? $t('ports.stop') : $t('ports.start')}
              title={s && s.status !== 'error' ? $t('ports.stop') : s?.status === 'error' ? $t('ports.retry') : $t('ports.start')}
              disabled={working[r.id]}
              on:click={() => toggle(r)}><span /></button
            >
            {#if !isRemote(r)}
              <button class="icon" title={$t('ports.copyTip', { text: copyText(r) })} aria-label={$t('ports.copyTip', { text: copyText(r) })} on:click={() => copy(r)}><Icon name="copy" size={14} /></button>
            {/if}
            {#if !isRemote(r) && !isSocks(r)}
              <button class="icon" title={$t('ports.openBrowser')} disabled={s?.status !== 'running'} on:click={() => openInBrowser(r)} aria-label={$t('ports.openBrowser')}><Icon name="globe" size={14} /></button>
            {/if}
            <button class="icon" title={$t('ports.edit')} on:click={() => (dialog = { initial: r })} aria-label={$t('ports.edit')}><Icon name="edit" size={14} /></button>
            <button class="icon" title={$t('ports.delete')} on:click={() => remove(r)} aria-label={$t('ports.delete')}><Icon name="trash" size={14} /></button>
          </div>
        </li>
      {/each}
    </ul>
  {/if}
</div>

{#if dialog}
  <ForwardDialog connId={tab.connId} {sessionId} initial={dialog.initial} on:saved={saved} on:close={() => (dialog = null)} />
{/if}

<style>
  .ports {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
    overflow: auto;
  }
  header {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 16px;
    padding: 16px 20px 12px;
    border-bottom: 1px solid var(--border);
  }
  h1 {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
  }
  .dim {
    color: var(--text-3);
    font-weight: 400;
    font-size: 12px;
  }
  header p {
    margin: 4px 0 0;
    color: var(--text-3);
    font-size: 12px;
  }
  .head-actions {
    display: flex;
    gap: 6px;
    flex: none;
  }
  .head-actions button {
    display: inline-flex;
    align-items: center;
    gap: 5px;
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 6px 12px 16px;
  }
  li {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 10px 8px;
    border-bottom: 1px solid var(--border);
  }
  li:hover {
    background: var(--bg-2);
  }
  .dot {
    flex: none;
    width: 9px;
    height: 9px;
    border-radius: 50%;
    background: var(--border);
  }
  .dot.running {
    background: var(--success);
  }
  .dot.starting {
    background: var(--warning);
    animation: pulse 1s infinite;
  }
  .dot.error {
    background: var(--danger);
  }
  @keyframes pulse {
    50% {
      opacity: 0.35;
    }
  }
  .main {
    flex: 1;
    min-width: 0;
  }
  .top {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .addr {
    color: var(--text);
  }
  .arrow {
    display: inline-flex;
    color: var(--text-3);
  }
  .dest {
    color: var(--text-2);
    font-size: 12px;
  }
  .tag {
    padding: 0 6px;
    border: 1px solid var(--border);
    color: var(--text-3);
    font-size: 10px;
  }
  .line {
    margin-top: 3px;
    color: var(--text-3);
    font-size: 12px;
    overflow-wrap: anywhere;
  }
  .line.bad {
    color: var(--danger);
  }
  .line.warn {
    color: var(--warning);
  }
  .acts {
    flex: none;
    display: flex;
    align-items: center;
    gap: 2px;
  }
  /* Interruptor: arrancar / detener. */
  .switch {
    position: relative;
    width: 34px;
    height: 18px;
    margin-right: 8px;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 9px;
    background: var(--bg);
  }
  .switch span {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 12px;
    height: 12px;
    border-radius: 50%;
    background: var(--text-3);
    transition: transform 0.12s, background 0.12s;
  }
  .switch.on {
    background: var(--success);
    border-color: var(--success);
  }
  .switch.on span {
    transform: translateX(16px);
    background: var(--bg);
  }
  .note {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    padding: 24px;
    text-align: center;
    color: var(--text-3);
  }
  .note p {
    margin: 0;
  }
  .lead {
    color: var(--text);
    font-size: 14px;
  }
  .small {
    max-width: 460px;
    font-size: 12px;
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
    gap: 6px;
    margin-top: 4px;
  }
  .chip {
    padding: 4px 10px;
    background: var(--bg-2);
    border: 1px solid var(--border);
    color: var(--text);
  }
</style>
