<script lang="ts">
  import { focusTrap } from '../lib/focustrap'
  import { t } from '../lib/i18n'
  import { createEventDispatcher, onDestroy, onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { api, errorText, type Forward, type K8sItem } from '../lib/api'
  import { toast } from '../lib/stores'
  import { holdOverlay } from '../lib/overlay'

  onDestroy(holdOverlay())

  // Crear o editar un reenvío: un puerto de esta PC que llega a un puerto del servidor o a un pod/servicio
  // de Kubernetes (con el kubectl del servidor), o al revés (remoto): un puerto del servidor que llega a
  // un puerto de esta PC. El dinámico es un proxy SOCKS en un puerto de esta PC que sale por el servidor.
  // Lo importante va arriba; lo raro, escondido.
  export let connId: string
  /** Sesión del servidor (para listar Kubernetes); null mientras no hay conexión. */
  export let sessionId: string | null
  export let initial: Partial<Forward> = {}

  const dispatch = createEventDispatcher<{ close: void; saved: { forward: Forward; isNew: boolean } }>()

  const presets = [
    { name: 'Redis', port: 6379 },
    { name: 'RabbitMQ', port: 5672 },
    { name: 'RabbitMQ (web)', port: 15672 },
    { name: 'PostgreSQL', port: 5432 },
    { name: 'MySQL', port: 3306 },
    { name: 'MongoDB', port: 27017 },
    { name: 'Elasticsearch', port: 9200 },
    { name: 'Web', port: 8080 },
  ]

  const isNew = !initial.id
  let mode: 'port' | 'remote' | 'socks' | 'k8s' =
    initial.kind === 'remote' ? 'remote' : initial.kind === 'dynamic' ? 'socks' : initial.resource ? 'k8s' : 'port'
  let name = initial.name ?? ''
  let port: number | null = initial.port ?? null // en el servidor, o en el pod/servicio
  let localPort: number | null = initial.localPort ?? null
  let localTouched = !!initial.localPort // el usuario eligió su puerto: no se lo cambiamos
  let host = initial.host ?? ''
  let otherHost = !!initial.host && initial.host !== 'localhost'
  let bindAll = initial.bindAll ?? false // remoto: en todas las interfaces del servidor
  let autoStart = initial.autoStart ?? false
  let saving = false

  // ---- puerto libre en esta PC ----
  let suggestion: number | null = null // si el puerto elegido está ocupado, el siguiente libre
  let checkTimer: ReturnType<typeof setTimeout>

  function checkLocal() {
    clearTimeout(checkTimer)
    if (mode === 'remote') return (suggestion = null) // en el remoto el puerto de esta PC es el destino: tiene que estar en uso
    checkTimer = setTimeout(async () => {
      const want = Number(localPort)
      // Al editar, el puerto que ya tiene el reenvío no cuenta como ocupado (lo usa él mismo, si está en marcha).
      if (!want || want < 1 || want > 65535 || want === initial.localPort) return (suggestion = null)
      const free = await api.forwardFreePort(want).catch(() => want)
      if (Number(localPort) === want) suggestion = free && free !== want ? free : null
    }, 250)
  }

  /** Al elegir el puerto del servidor, el de esta PC arranca igual (o el siguiente libre). */
  async function pickPort(p: number) {
    port = p
    if (!localTouched) localPort = mode === 'remote' ? p : await api.forwardFreePort(p).catch(() => p)
    checkLocal()
  }

  function preset(p: (typeof presets)[number]) {
    name = p.name
    pickPort(p.port)
  }

  onMount(() => {
    if (mode === 'k8s') loadNamespaces()
    return () => clearTimeout(checkTimer)
  })

  // ---- Kubernetes ----
  let namespace = initial.namespace ?? ''
  let namespaces: string[] = []
  let nsLoading = false
  let nsError = ''
  let items: K8sItem[] = []
  let itemsLoading = false
  let itemsError = ''
  let filter = ''
  let selected: string | null = initial.resource ?? null // "svc/redis"
  let k8sLoaded = false

  async function loadNamespaces() {
    if (!sessionId) return
    k8sLoaded = true
    nsLoading = true
    nsError = ''
    try {
      const r = await api.k8sNamespaces(sessionId)
      namespaces = r.names
      if (!namespace) namespace = r.names.includes(r.current) ? r.current : (r.names[0] ?? r.current)
      loadItems()
    } catch (e) {
      nsError = errorText(e)
      if (namespace) loadItems() // sin permiso para listar namespaces: se puede escribir el nombre
    } finally {
      nsLoading = false
    }
  }

  async function loadItems() {
    if (!sessionId || !namespace.trim()) return
    itemsLoading = true
    itemsError = ''
    items = []
    try {
      items = (await api.k8sResources(sessionId, namespace.trim())) ?? []
    } catch (e) {
      itemsError = errorText(e)
    } finally {
      itemsLoading = false
    }
  }

  /** El 1080 es el puerto de siempre de un SOCKS: se propone ese, o el siguiente libre. */
  async function suggestSocksPort() {
    const p = await api.forwardFreePort(1080).catch(() => 1080)
    if (!localTouched && !localPort) {
      localPort = p || 1080
      checkLocal()
    }
  }

  function setMode(m: typeof mode) {
    mode = m
    if (m === 'k8s' && !k8sLoaded) loadNamespaces()
    if (m === 'socks' && !localTouched && !localPort) suggestSocksPort()
    checkLocal()
  }

  const resOf = (it: K8sItem) => `${it.kind}/${it.name}`

  function select(it: K8sItem, p?: number) {
    selected = resOf(it)
    const chosen = p ?? it.ports[0]?.port
    if (chosen) pickPort(chosen)
    if (!name.trim() || autoName) {
      name = it.name
      autoName = true
    }
  }
  let autoName = !initial.name // el nombre lo puso la elección, no el usuario

  $: visible = items.filter((it) => !filter.trim() || it.name.toLowerCase().includes(filter.trim().toLowerCase()))
  $: current = items.find((it) => resOf(it) === selected)

  const badge = (s: string) => (s === 'Running' || s === 'ClusterIP' || s === 'NodePort' || s === 'LoadBalancer' ? '' : 'bad')

  // ---- guardar ----
  $: canSave =
    !!localPort &&
    (mode === 'socks' || (!!port && (mode === 'k8s' ? !!selected && !!namespace.trim() : true)))

  async function save() {
    saving = true
    try {
      const forward = await api.forwardSave({
        id: initial.id,
        connId,
        name: name.trim(),
        kind: mode === 'remote' ? 'remote' : mode === 'socks' ? 'dynamic' : '',
        localPort: Number(localPort),
        host: (mode === 'port' || mode === 'remote') && otherHost ? host.trim() : '',
        port: mode === 'socks' ? 0 : Number(port),
        bindAll: mode === 'remote' && bindAll,
        namespace: mode === 'k8s' ? namespace.trim() : '',
        resource: mode === 'k8s' ? (selected ?? '') : '',
        autoStart,
      })
      dispatch('saved', { forward, isNew })
      dispatch('close')
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      saving = false
    }
  }

  checkLocal()
</script>

<div class="backdrop" role="presentation" on:mousedown|self={() => dispatch('close')} on:keydown={(e) => e.key === 'Escape' && dispatch('close')}>
  <form class="modal" on:submit|preventDefault={save} role="dialog" aria-modal="true" use:focusTrap aria-labelledby="forward-title">
    <h2 id="forward-title">{isNew ? $t('forward.newTitle') : $t('forward.editTitle')}</h2>

    <div class="seg">
      <button type="button" class:on={mode === 'port'} on:click={() => setMode('port')}>{$t('forward.modeLocal')}</button>
      <button type="button" class:on={mode === 'remote'} on:click={() => setMode('remote')}>{$t('forward.modeRemote')}</button>
      <button type="button" class:on={mode === 'socks'} on:click={() => setMode('socks')}>{$t('forward.modeSocks')}</button>
      <button type="button" class:on={mode === 'k8s'} on:click={() => setMode('k8s')}>Kubernetes</button>
    </div>

    {#if mode === 'port'}
      <div class="field">
        <span class="lbl">{$t('forward.service')}</span>
        <div class="chips">
          {#each presets as p}
            <button type="button" class="chip" class:on={port === p.port && name === p.name} on:click={() => preset(p)}>
              {p.name} <span class="mono dim">{p.port}</span>
            </button>
          {/each}
        </div>
      </div>
      <label>
        {$t('forward.serverPort')}
        <input class="mono" type="number" min="1" max="65535" bind:value={port} on:input={() => port && pickPort(Number(port))} placeholder="6379" required />
      </label>
      {#if otherHost}
        <label>
          {$t('forward.hostFromServer')}
          <input class="mono" bind:value={host} placeholder={$t('forward.hostPlaceholder')} />
        </label>
      {:else}
        <button type="button" class="link" on:click={() => (otherHost = true)}>{$t('forward.otherMachine')}</button>
      {/if}
    {:else if mode === 'socks'}
      <p class="muted">
        {$t('forward.socksA')} <span class="mono">-D</span> {$t('forward.socksB')} <span class="mono">localhost</span>.
      </p>
    {:else if mode === 'remote'}
      <p class="muted">{$t('forward.remoteA')} <span class="mono">-R</span> {$t('forward.remoteB')}</p>
      <label>
        {$t('forward.serverPort')}
        <input class="mono" type="number" min="1" max="65535" bind:value={port} on:input={() => port && pickPort(Number(port))} placeholder="8080" required />
      </label>
      <label class="check">
        <input type="checkbox" bind:checked={bindAll} />
        {$t('forward.bindAll')}
      </label>
      {#if bindAll}
        <span class="warn">{$t('forward.bindAllWarn')}</span>
      {/if}
      <label>
        {$t('forward.localPort')}
        <input class="mono" type="number" min="1" max="65535" bind:value={localPort} on:input={() => (localTouched = true)} placeholder="3000" required />
        {#if port && localPort}
          <span class="dim">{$t('forward.remoteMapA')} <span class="mono">localhost:{port}</span> {$t('forward.remoteMapB')} <span class="mono">{otherHost && host.trim() ? host.trim() : 'localhost'}:{localPort}</span> {$t('forward.remoteMapC')}</span>
        {/if}
      </label>
      {#if otherHost}
        <label>
          {$t('forward.hostFromPc')}
          <input class="mono" bind:value={host} placeholder="192.168.1.20" />
        </label>
      {:else}
        <button type="button" class="link" on:click={() => (otherHost = true)}>{$t('forward.otherMachinePc')}</button>
      {/if}
    {:else}
      <div class="k8s">
        {#if !sessionId}
          <p class="muted">{$t('forward.connectingServer')}</p>
        {:else}
          <label>
            Namespace
            {#if nsError && !namespaces.length}
              <input class="mono" bind:value={namespace} on:change={loadItems} placeholder="default" />
              <span class="warn">{$t('forward.nsListError', { error: nsError })}</span>
            {:else}
              <select bind:value={namespace} on:change={loadItems} disabled={nsLoading}>
                {#if !namespaces.includes(namespace) && namespace}<option value={namespace}>{namespace}</option>{/if}
                {#each namespaces as n}<option value={n}>{n}</option>{/each}
              </select>
            {/if}
          </label>
          {#if itemsError}
            <p class="warn">{itemsError}</p>
          {:else if itemsLoading || nsLoading}
            <p class="muted">{$t('forward.queryCluster')}</p>
          {:else}
            <div class="search">
              <Icon name="search" size={13} />
              <input bind:value={filter} placeholder={$t('forward.filter')} aria-label={$t('forward.filterLabel')} />
            </div>
            <div class="list">
              {#each visible as it (it.kind + it.name)}
                <div
                  class="res"
                  class:sel={selected === resOf(it)}
                  role="button"
                  tabindex="0"
                  on:click={() => select(it)}
                  on:keydown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), select(it))}
                >
                  <span class="kind">{it.kind === 'svc' ? $t('forward.kindSvc') : $t('forward.kindPod')}</span>
                  <span class="rname">{it.name}</span>
                  {#if it.status}<span class="st {badge(it.status)}">{it.status}</span>{/if}
                  <span class="ports">
                    {#each it.ports as p}
                      <button
                        type="button"
                        class="chip small"
                        class:on={selected === resOf(it) && port === p.port}
                        title={p.name || ''}
                        on:click|stopPropagation={() => select(it, p.port)}>{p.port}</button
                      >
                    {:else}
                      <span class="dim">{$t('forward.noPorts')}</span>
                    {/each}
                  </span>
                </div>
              {:else}
                <div class="muted pad">{items.length ? $t('forward.noMatch') : $t('forward.noItems')}</div>
              {/each}
            </div>
            {#if selected && !current?.ports.length}
              <label>
                {selected.startsWith('pod') ? $t('forward.portOfPod') : $t('forward.portOfSvc')}
                <input class="mono" type="number" min="1" max="65535" bind:value={port} on:input={() => port && pickPort(Number(port))} />
              </label>
            {/if}
          {/if}
        {/if}
      </div>
    {/if}

    {#if mode !== 'remote'}
      <label>
        {$t('forward.localPort')}
        <input
          class="mono"
          type="number"
          min="1"
          max="65535"
          bind:value={localPort}
          on:input={() => ((localTouched = true), checkLocal())}
          placeholder="6379"
          required
        />
        {#if suggestion}
          <span class="warn">
            {$t('forward.portBusy')}
            <button type="button" class="link" on:click={() => ((localPort = suggestion), (localTouched = true), (suggestion = null))}>{$t('forward.useSuggestion', { port: suggestion })}</button>
          </span>
        {:else if localPort}
          <span class="ok"><Icon name="check" size={12} /> {$t('forward.free')} <span class="mono">localhost:{localPort}</span></span>
        {/if}
      </label>
    {/if}

    <label>
      {$t('forward.nameOpt')}
      <input
        bind:value={name}
        on:input={() => (autoName = false)}
        placeholder={mode === 'socks' ? (localPort ? `SOCKS ${localPort}` : $t('forward.nameSocksPh')) : port ? $t('forward.namePortPh', { port }) : $t('forward.nameDefaultPh')}
      />
    </label>
    <label class="check">
      <input type="checkbox" bind:checked={autoStart} />
      {$t('forward.autoStart')}
    </label>

    <footer>
      <span class="spacer" />
      <button type="button" on:click={() => dispatch('close')}>{$t('common.cancel')}</button>
      <button class="primary" type="submit" disabled={saving || !canSave}>{saving ? $t('form.saving') : isNew ? $t('forward.createStart') : $t('common.save')}</button>
    </footer>
  </form>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    z-index: 400;
  }
  .modal {
    width: 540px;
    max-width: 94vw;
    max-height: 92vh;
    overflow: auto;
    background: var(--bg-2);
    border: 1px solid var(--border);
    padding: 20px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  h2 {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
  }
  label,
  .field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    color: var(--text-2);
    font-size: 12px;
  }
  label input,
  label select {
    color: var(--text);
    font-size: 13px;
  }
  .seg {
    display: flex;
    border: 1px solid var(--border);
    overflow: hidden;
  }
  .seg button {
    flex: 1;
    border: 0;
    background: var(--bg);
  }
  .seg button + button {
    border-left: 1px solid var(--border);
  }
  .seg button.on {
    background: var(--accent);
    color: var(--on-accent);
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .chip {
    padding: 3px 9px;
    background: var(--bg);
    border: 1px solid var(--border);
    color: var(--text);
    font-size: 12px;
  }
  .chip.small {
    padding: 0 6px;
    font-family: var(--mono);
    font-size: 11px;
  }
  .chip.on {
    background: var(--accent);
    border-color: var(--accent);
    color: var(--on-accent);
  }
  .chip.on .dim {
    color: inherit;
  }
  .dim {
    color: var(--text-3);
    font-size: 11px;
  }
  .link {
    align-self: flex-start;
    background: none;
    border: 0;
    padding: 0;
    color: var(--text-2);
    text-decoration: underline;
    font-size: 12px;
  }
  .link:hover {
    color: var(--text);
  }
  .check {
    flex-direction: row;
    align-items: center;
    gap: 8px;
    color: var(--text);
    font-size: 13px;
  }
  .k8s {
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  .search {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 0 8px;
    background: var(--bg);
    border: 1px solid var(--border);
    color: var(--text-3);
  }
  .search input {
    flex: 1;
    border: 0;
    background: transparent;
    padding: 6px 0;
  }
  .list {
    max-height: 210px;
    overflow: auto;
    border: 1px solid var(--border);
    background: var(--bg);
  }
  .res {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 5px 8px;
    cursor: default;
  }
  .res:hover {
    background: var(--bg-3);
  }
  .res.sel {
    background: var(--bg-4);
  }
  .kind {
    flex: none;
    width: 52px;
    font-size: 10px;
    text-transform: uppercase;
    color: var(--text-3);
  }
  .rname {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text);
    font-size: 13px;
  }
  .st {
    flex: none;
    font-size: 10px;
    color: var(--text-3);
  }
  .st.bad {
    color: var(--warning);
  }
  .ports {
    flex: none;
    display: flex;
    gap: 4px;
  }
  .muted {
    margin: 0;
    color: var(--text-3);
    font-size: 12px;
  }
  .pad {
    padding: 12px;
    text-align: center;
  }
  .warn {
    margin: 0;
    color: var(--warning);
    font-size: 12px;
  }
  .ok {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    color: var(--success);
    font-size: 12px;
  }
  footer {
    display: flex;
    gap: 8px;
    margin-top: 4px;
  }
  .spacer {
    flex: 1;
  }
</style>
