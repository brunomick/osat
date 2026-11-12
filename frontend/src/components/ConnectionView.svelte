<script lang="ts">
  import { t, tr } from '../lib/i18n'
  import { onMount } from 'svelte'
  import { api, errorText, type AuthType, type Connection, type DefaultCreds, type Protocol } from '../lib/api'
  import { confirmDialog } from '../lib/dialog'
  import { closeTab, connections, groupPaths, openAppTab, patchTab, reloadConnections, toast, type Tab } from '../lib/stores'
  import { canBeJump, defaultPort, formFields } from '../lib/protocol'

  export let tab: Tab

  const initial: Partial<Connection> = tab.conn ?? {}
  const close = () => closeTab(tab.id)
  const colors = ['', '#4f8cff', '#3fb950', '#d29922', '#e5534b', '#b083f0', '#39c5cf', '#f778ba']

  let protocol: Protocol = initial.protocol === 'rdp' || initial.protocol === 'vnc' ? initial.protocol : 'ssh'
  let domain = initial.domain ?? ''
  let gateway = initial.gateway ?? ''
  let name = initial.name ?? ''
  let group = initial.group ?? ''
  let host = initial.host ?? ''
  let port = initial.port ?? 22
  let user = initial.user ?? 'root'
  let authType: AuthType = initial.authType ?? 'password'
  let keyPath = initial.keyPath ?? ''
  let jumpId = initial.jumpId ?? ''
  let color = initial.color ?? ''
  let forwardAgent = initial.forwardAgent ?? false
  let initialCommand = initial.initialCommand ?? ''
  let envText = (initial.env ?? []).join('\n') // una variable por línea
  const advancedOpen = forwardAgent || !!initialCommand || !!envText.trim() // abierta si ya hay algo cargado
  let secret = ''
  let saveSecret = initial.id ? !!initial.hasSecret : true
  // Credenciales por defecto (Configuración): con la casilla se usan en vez del usuario y la contraseña propios.
  let useDefaults = initial.useDefaultCreds ?? false
  let defaults: DefaultCreds = { user: '', hasSecret: false }
  let saving = false
  let hostInput: HTMLInputElement

  const isEdit = !!initial.id
  // Gateway y Proxy Jump no se combinan: con un gateway, el salto se quita.
  $: fields = formFields(protocol)
  $: ownUser = fields.user && !useDefaults
  $: ownAuth = !useDefaults
  $: hasGateway = fields.gateway && !!gateway.trim()
  $: if (hasGateway) jumpId = ''
  $: groups = [...new Set([...$connections.map((c) => c.group), ...$groupPaths].filter(Boolean))]

  // Saltos posibles: las conexiones SSH, menos esta y las que ya saltan a través de esta (sería un ciclo).
  function jumpsThrough(c: Connection, id: string): boolean {
    for (let n = 0, j = c; j.jumpId && n < 10; n++) {
      if (j.jumpId === id) return true
      const next = $connections.find((x) => x.id === j.jumpId)
      if (!next) return false
      j = next
    }
    return false
  }
  $: jumpOptions = $connections.filter(
    (c) => canBeJump(c) && c.id !== initial.id && !(initial.id && jumpsThrough(c, initial.id)),
  )

  // Pegar "usuario@host:puerto" en el campo host lo separa automáticamente.
  function parseHost() {
    const m = host.trim().match(/^(?:([^@\s]+)@)?([^:\s]+)(?::(\d+))?$/)
    if (!m) return
    if (m[1]) user = m[1]
    host = m[2]
    if (m[3]) port = Number(m[3])
  }

  function setProtocol(p: Protocol) {
    if (Number(port) === defaultPort[protocol]) port = defaultPort[p]
    protocol = p
    if (p !== 'ssh') authType = 'password'
  }

  async function pickKey() {
    try {
      const p = await api.pickKeyFile(keyPath)
      if (p) keyPath = p
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function save() {
    saving = true
    try {
      parseHost()
      await api.saveConnection({
        id: initial.id ?? '',
        name,
        group,
        protocol,
        host,
        port: Number(port) || defaultPort[protocol],
        user: ownUser ? user : '',
        useDefaultCreds: useDefaults,
        domain: fields.domain ? domain : '',
        gateway: fields.gateway ? gateway : '',
        authType: useDefaults ? 'password' : authType,
        keyPath: !useDefaults && authType === 'key' ? keyPath : '',
        jumpId,
        color,
        forwardAgent: protocol === 'ssh' && forwardAgent,
        initialCommand: protocol === 'ssh' ? initialCommand : '',
        env: protocol === 'ssh' ? envText.split('\n').map((l) => l.trim()).filter(Boolean) : [],
        hasSecret: false,
        // Con las por defecto, la contraseña propia (si había) queda guardada por si se desmarca.
        secret: !useDefaults && saveSecret && authType !== 'agent' ? secret : '',
        keepSecret: useDefaults || (saveSecret && authType !== 'agent'),
      })
      await reloadConnections()
      toast(isEdit ? tr('form.updated') : tr('form.created'), 'success')
      close()
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      saving = false
    }
  }

  async function remove() {
    if (!initial.id) return
    const ok = await confirmDialog({
      title: tr('sidebar.removeConn'),
      message: tr('sidebar.removeConnAsk', { name: initial.name ?? '' }),
      okText: tr('common.delete'),
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteConnection(initial.id)
    } catch (e) {
      toast(errorText(e), 'error') // p. ej. es el salto de otra conexión
      return
    }
    await reloadConnections()
    close()
  }

  onMount(() => {
    patchTab(tab.id, { status: 'ready' })
    hostInput?.focus()
    api.defaultCredsGet().then((d) => (defaults = d)).catch(() => {})
  })
</script>

<div class="page">
  <form class="body" on:submit|preventDefault={save} aria-labelledby="connform-title">
    <div class="title">
      <h2 id="connform-title">{isEdit ? $t('form.editTitle') : $t('form.newTitle')}</h2>
      {#if !isEdit}
        <button type="button" class="ghost" title={$t('form.importTip')} on:click={() => (close(), openAppTab('import'))}>
          {$t('form.importBtn')}
        </button>
      {/if}
    </div>

    <div class="field">
      <span class="lbl">{$t('form.protocol')}</span>
      <div class="seg">
        <button type="button" class:on={protocol === 'ssh'} on:click={() => setProtocol('ssh')}>SSH / SFTP</button>
        <button type="button" class:on={protocol === 'rdp'} on:click={() => setProtocol('rdp')}>{$t('form.rdpBtn')}</button>
        <button type="button" class:on={protocol === 'vnc'} on:click={() => setProtocol('vnc')}>{$t('form.vncBtn')}</button>
      </div>
    </div>

    <div class="grid">
      <label class="span2">
        {$t('form.host')}
        <input bind:this={hostInput} bind:value={host} on:blur={parseHost} placeholder={$t('form.hostPlaceholder')} required />
      </label>
      <label>
        {$t('form.port')}
        <input type="number" min="1" max="65535" bind:value={port} />
      </label>
      {#if ownUser}
        <label class="span2">
          {$t('form.user')}
          <input bind:value={user} required={!defaults.user} placeholder={defaults.user ? $t('form.userDefault', { user: defaults.user }) : ''} />
        </label>
      {/if}
      <label class:span3={!ownUser}>
        {$t('form.group')}
        <input list="osat-groups" bind:value={group} placeholder={$t('form.groupPlaceholder')} />
        <datalist id="osat-groups">
          {#each groups as g}<option value={g} />{/each}
        </datalist>
      </label>
      {#if fields.domain}
        <label class="span3">
          {$t('form.domain')}
          <input bind:value={domain} placeholder={$t('form.domainPlaceholder')} />
        </label>
        <label class="span3">
          {$t('form.gateway')}
          <input bind:value={gateway} placeholder={$t('form.gatewayPlaceholder')} />
          <span class="muted small">{$t('form.gatewayHelp')}</span>
        </label>
      {/if}
      <label class="span3">
        {$t('form.name')}
        <input bind:value={name} placeholder={host ? (fields.user ? `${user}@${host}` : host) : $t('form.namePlaceholder')} />
      </label>
    </div>

    <label class="check">
      <input type="checkbox" bind:checked={useDefaults} />
      {$t('form.useDefaults')}
    </label>
    {#if useDefaults}
      <p class="muted small">
        {defaults.user || defaults.hasSecret
          ? $t('form.useDefaultsHelp', { user: defaults.user || '—' })
          : $t('form.useDefaultsNone')}
      </p>
    {/if}

    {#if ownAuth && fields.auth}
      <div class="field">
        <span class="lbl">{$t('form.auth')}</span>
        <div class="seg">
          <button type="button" class:on={authType === 'password'} on:click={() => (authType = 'password')}>{$t('form.authPassword')}</button>
          <button type="button" class:on={authType === 'key'} on:click={() => (authType = 'key')}>{$t('form.authKey')}</button>
          <button type="button" class:on={authType === 'agent'} on:click={() => (authType = 'agent')}>{$t('form.authAgent')}</button>
        </div>
      </div>
    {/if}

    {#if ownAuth && authType === 'key'}
      <label class="field">
        {$t('form.keyFile')}
        <div class="row">
          <input class="mono" bind:value={keyPath} placeholder="~/.ssh/id_ed25519" required />
          <button type="button" on:click={pickKey}>{$t('form.browse')}</button>
        </div>
      </label>
    {/if}

    {#if ownAuth && authType !== 'agent'}
      <label class="field">
        {authType === 'key' ? $t('form.passphrase') : $t('form.authPassword')}
        <input
          type="password"
          bind:value={secret}
          placeholder={isEdit && initial.hasSecret ? $t('form.secretSaved') : ''}
          autocomplete="new-password"
        />
      </label>
      <label class="check">
        <input type="checkbox" bind:checked={saveSecret} />
        {$t('form.saveSecret')}
      </label>
    {:else if ownAuth}
      <p class="muted small">{$t('form.agentHelp')}</p>
    {/if}

    <label class="field">
      {$t('form.proxyJump')}
      <select bind:value={jumpId} disabled={hasGateway}>
        <option value="">{$t('form.noJump')}</option>
        {#each jumpOptions as c}
          <option value={c.id}>{c.name}{c.group ? ` — ${c.group}` : ''}</option>
        {/each}
      </select>
      <span class="muted small">{hasGateway ? $t('form.jumpGatewayHelp') : protocol === 'vnc' ? $t('form.jumpHelpVnc') : $t('form.jumpHelp')}</span>
    </label>

    {#if fields.advanced}
      <details class="adv" open={advancedOpen}>
        <summary>{$t('form.advanced')}</summary>
        <div class="adv-body">
          <label class="check">
            <input type="checkbox" bind:checked={forwardAgent} />
            {$t('form.forwardAgent')}
          </label>
          <p class="muted small warn">
            {$t('form.forwardAgentWarn')}
          </p>
          <label class="field">
            {$t('form.initialCommand')}
            <input class="mono" bind:value={initialCommand} placeholder="cd /var/www && ls" />
            <span class="muted small">{$t('form.initialCommandHelp')}</span>
          </label>
          <label class="field">
            {$t('form.env')}
            <textarea class="mono" rows="3" bind:value={envText} placeholder={'EDITOR=vim\nLC_FOO=bar'} spellcheck="false" />
            <span class="muted small">
              {$t('form.envHelp')}
            </span>
          </label>
        </div>
      </details>
    {/if}

    <div class="field">
      <span class="lbl">{$t('form.color')}</span>
      <div class="colors">
        {#each colors as c}
          <button
            type="button"
            class="sw"
            class:on={color === c}
            style="background:{c || 'transparent'}"
            title={c || $t('form.noColor')}
            on:click={() => (color = c)}
          />
        {/each}
      </div>
    </div>

    <footer>
      {#if isEdit}
        <button type="button" class="ghost del" on:click={remove}>{$t('common.delete')}</button>
      {/if}
      <span class="spacer" />
      <button type="button" on:click={close}>{$t('common.cancel')}</button>
      <button class="primary" type="submit" disabled={saving}>{saving ? $t('form.saving') : $t('common.save')}</button>
    </footer>
  </form>
</div>

<style>
  .page {
    height: 100%;
    overflow: auto;
    background: var(--bg-2);
  }
  .body {
    width: 560px;
    max-width: 100%;
    margin: 0 auto;
    padding: 24px 20px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  h2 {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
  }
  .title {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .grid {
    display: grid;
    grid-template-columns: 1fr 1fr 120px;
    gap: 12px;
  }
  .span2 {
    grid-column: span 2;
  }
  .span3 {
    grid-column: span 3;
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
  .row {
    display: flex;
    gap: 8px;
  }
  .row input {
    flex: 1;
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
  .check {
    flex-direction: row;
    align-items: center;
    gap: 8px;
    color: var(--text);
    font-size: 13px;
  }
  .colors {
    display: flex;
    gap: 8px;
  }
  .sw {
    width: 22px;
    height: 22px;
    padding: 0;
    border: 2px solid var(--border);
  }
  .sw.on {
    border-color: var(--text);
  }
  .small {
    font-size: 12px;
    margin: 0;
  }
  .adv summary {
    cursor: pointer;
    color: var(--text-2);
    font-size: 12px;
  }
  .adv-body {
    display: flex;
    flex-direction: column;
    gap: 12px;
    margin-top: 10px;
  }
  .warn {
    color: var(--warning);
  }
  label textarea {
    color: var(--text);
    font-size: 13px;
  }
  footer {
    display: flex;
    gap: 8px;
    margin-top: 4px;
  }
  .spacer {
    flex: 1;
  }
  .del {
    color: var(--danger);
  }
</style>
