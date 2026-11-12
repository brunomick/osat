<script lang="ts">
  import { focusTrap } from '../lib/focustrap'
  import { t, tr } from '../lib/i18n'
  // Exportar conexiones a un archivo .osat para importarlas en otro osat. Se
  // eligen en un árbol de grupos como el de la barra lateral (todo, grupos o
  // conexiones sueltas). Va siempre cifrado con una contraseña; opcionalmente
  // lleva las contraseñas y las claves privadas completas.
  import Icon from '../lib/Icon.svelte'
  import { api, clipboardSet, errorText } from '../lib/api'
  import { buildRows, isWithin } from '../lib/groups'
  import { connections, exportGroup, sites, toast } from '../lib/stores'
  import { holdOverlay } from '../lib/overlay'
  import { onDestroy, onMount } from 'svelte'

  onDestroy(holdOverlay())

  export let group: string // el que arranca marcado ("" = todas)

  let selected = new Set($connections.filter((c) => !group || isWithin(c.group, group)).map((c) => c.id))
  let collapsed: Record<string, boolean> = {}
  let password = ''
  let confirm = ''
  let withSecrets = true
  // Snippets y sitios web: van todos (no dependen de qué conexiones se eligen).
  let withSnippets = true
  let withSites = true
  let snippetCount = 0
  // Credenciales por defecto (con su contraseña): solo si se elige, apagado de entrada.
  let withDefaults = false
  let defaultsUser = ''
  let hasDefaults = false
  onMount(async () => {
    try {
      snippetCount = ((await api.snippetsList()) ?? []).length
    } catch {
      // sin la cantidad, la casilla queda deshabilitada
    }
    try {
      const d = await api.defaultCredsGet()
      defaultsUser = d.user
      hasDefaults = !!d.user || d.hasSecret
    } catch {
      // sin credenciales por defecto, la casilla queda deshabilitada
    }
  })
  let saving = false
  let generated = '' // la última contraseña generada, para mostrarla y copiarla

  $: rows = buildRows($connections, [], collapsed)
  $: chosen = $connections.filter((c) => selected.has(c.id)).map((c) => c.id)
  $: allOn = chosen.length === $connections.length
  $: extras = (withSnippets && snippetCount > 0) || (withSites && $sites.length > 0) || (withDefaults && hasDefaults)
  $: problem =
    password.length < 8 ? $t('export.pwShort') : password !== confirm ? $t('export.pwMismatch') : ''

  const close = () => exportGroup.set(null)
  const idsIn = (path: string) => $connections.filter((c) => isWithin(c.group, path)).map((c) => c.id)

  function stateOf(path: string, sel: Set<string>) {
    const ids = idsIn(path)
    const n = ids.filter((id) => sel.has(id)).length
    return { n, total: ids.length, all: n === ids.length, some: n > 0 && n < ids.length }
  }

  function setMany(ids: string[], on: boolean) {
    const s = new Set(selected)
    for (const id of ids) on ? s.add(id) : s.delete(id)
    selected = s
  }

  const toggleGroup = (path: string) => setMany(idsIn(path), !stateOf(path, selected).all)
  const toggleConn = (id: string) => setMany([id], !selected.has(id))
  const toggleAll = () => setMany($connections.map((c) => c.id), !allOn)
  const toggleOpen = (path: string) => (collapsed = { ...collapsed, [path]: !collapsed[path] })

  // 6 bloques de 5 caracteres al azar (crypto.getRandomValues), sin los que
  // se confunden al dictarla o copiarla a mano (0/O, 1/l/I): ~175 bits.
  const ALPHABET = 'abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'
  function generate() {
    const out: string[] = []
    const buf = new Uint8Array(1)
    while (out.length < 30) {
      crypto.getRandomValues(buf)
      // Descarta los valores que harían más probable algún carácter.
      if (buf[0] < 256 - (256 % ALPHABET.length)) out.push(ALPHABET[buf[0] % ALPHABET.length])
    }
    generated = password = confirm = out.join('').replace(/(.{5})(?=.)/g, '$1-')
  }

  async function copyGenerated() {
    await clipboardSet(generated)
    toast(tr('export.copied'), 'success')
  }

  // El checkbox de un grupo a medio marcar.
  function indeterminate(el: HTMLInputElement, on: boolean) {
    el.indeterminate = on
    return { update: (v: boolean) => (el.indeterminate = v) }
  }

  async function save() {
    if (problem || saving || (!chosen.length && !extras)) return
    saving = true
    try {
      const path = await api.exportConnections(chosen, password, withSecrets, withSnippets, withSites, withDefaults && hasDefaults)
      if (!path) return
      if (chosen.length) toast(tr(chosen.length === 1 ? 'export.done.one' : 'export.done.other', { count: chosen.length, path }), 'success', 6000)
      else toast(tr('export.doneFile', { path }), 'success', 6000)
      close()
    } catch (e) {
      toast(errorText(e), 'error', 8000)
    } finally {
      saving = false
    }
  }
</script>

<div class="backdrop" role="presentation" on:mousedown|self={close} on:keydown={(e) => e.key === 'Escape' && close()}>
  <form class="modal" on:submit|preventDefault={save} role="dialog" aria-modal="true" use:focusTrap aria-labelledby="export-title">
    <h2 id="export-title">{$t('export.title')}</h2>
    <p class="muted small">
      {$t('export.intro')}
    </p>

    <div class="list">
      <label class="row head">
        <input type="checkbox" checked={allOn} use:indeterminate={chosen.length > 0 && !allOn} on:change={toggleAll} />
        <span>{$t('import.selected', { selected: chosen.length, total: $connections.length })}</span>
      </label>
      {#each rows as r (r.key)}
        {#if r.kind === 'group'}
          {@const st = stateOf(r.path, selected)}
          <div class="row" style="--d: {r.depth}">
            <button type="button" class="chev" class:open={r.open} aria-label={r.open ? $t('export.collapse') : $t('export.expand')} on:click={() => toggleOpen(r.path)}>
              <Icon name="chevron" size={12} />
            </button>
            <label class="item">
              <input type="checkbox" checked={st.all} use:indeterminate={st.some} on:change={() => toggleGroup(r.path)} />
              <Icon name="folder" size={13} />
              <span class="name grp" title={r.path}>{r.name}</span>
              <span class="muted count">{st.n}/{st.total}</span>
            </label>
          </div>
        {:else}
          {@const c = r.c}
          <div class="row" style="--d: {r.depth}">
            <span class="chev" />
            <label class="item">
              <input type="checkbox" checked={selected.has(c.id)} on:change={() => toggleConn(c.id)} />
              <span class="tag" style="background:{c.color || 'var(--bg)'}" />
              <span class="name" title={c.name}>{c.name}</span>
              <span class="muted mono host" title={c.host}>{c.host}</span>
              {#if c.protocol === 'rdp'}<span class="proto">RDP</span>{:else if c.protocol === 'vnc'}<span class="proto">VNC</span>{/if}
            </label>
          </div>
        {/if}
      {/each}
    </div>

    <div class="pw">
      <label class="field">
        <span class="lbl">{$t('export.filePassword')}</span>
        <input type="password" autocomplete="new-password" bind:value={password} />
      </label>
      <label class="field">
        <span class="lbl">{$t('export.repeat')}</span>
        <input type="password" autocomplete="new-password" bind:value={confirm} />
      </label>
    </div>
    <div class="gen">
      <button type="button" class="ghost" on:click={generate}>
        <Icon name="key" size={13} />
        {$t('export.generate')}
      </button>
      {#if generated && password === generated && confirm === generated}
        <span class="mono genpw" title={generated}>{generated}</span>
        <button type="button" class="ghost" on:click={copyGenerated}>{$t('export.copy')}</button>
      {/if}
    </div>

    <label class="check">
      <input type="checkbox" bind:checked={withSnippets} disabled={!snippetCount} />
      <span>{$t('export.withSnippets', { count: snippetCount })}</span>
    </label>
    <label class="check">
      <input type="checkbox" bind:checked={withSites} disabled={!$sites.length} />
      <span>{$t('export.withSites', { count: $sites.length })}</span>
    </label>
    <label class="check">
      <input type="checkbox" bind:checked={withDefaults} disabled={!hasDefaults} />
      <span>{$t('export.withDefaults', { user: defaultsUser || '—' })}</span>
    </label>

    <label class="check">
      <input type="checkbox" bind:checked={withSecrets} />
      <span>{$t('export.withSecrets')}</span>
    </label>
    {#if withSecrets}
      <p class="warn small">
        {$t('export.secretsWarn')}
      </p>
    {:else}
      <p class="muted small">{$t('export.noSecrets')}</p>
    {/if}

    {#if (password || confirm) && problem}
      <p class="err small">{problem}</p>
    {/if}

    <footer>
      <span class="spacer" />
      <button type="button" on:click={close}>{$t('common.cancel')}</button>
      <button type="submit" class="primary" disabled={!!problem || saving || (!chosen.length && !extras)}>
        {saving ? $t('export.exporting') : `${$t('export.run')}${chosen.length ? ` (${chosen.length})` : ''}…`}
      </button>
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
    background: var(--overlay);
  }
  .modal {
    width: 600px;
    max-width: 94vw;
    max-height: 90vh;
    background: var(--bg-2);
    border: 1px solid var(--border);
    padding: 20px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  h2 {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
  }
  .small {
    font-size: 12px;
    margin: 0;
    line-height: 1.5;
  }
  .list {
    flex: 1;
    min-height: 120px;
    max-height: 46vh;
    overflow: auto;
    border: 1px solid var(--border);
  }
  .row {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 3px 8px 3px calc(8px + var(--d, 0) * 16px);
    font-size: 13px;
    white-space: nowrap;
  }
  .row + .row {
    border-top: 1px solid var(--bg-3);
  }
  .row.head {
    gap: 8px;
    position: sticky;
    top: 0;
    z-index: 1;
    padding-left: 8px;
    background: var(--bg-3);
    color: var(--text-2);
    font-size: 12px;
  }
  .chev {
    flex: none;
    width: 18px;
    height: 18px;
    display: grid;
    place-items: center;
    padding: 0;
    border: 0;
    background: none;
    color: var(--text-3);
  }
  button.chev {
    cursor: pointer;
  }
  button.chev :global(svg) {
    transition: transform 0.1s;
  }
  button.chev.open :global(svg) {
    transform: rotate(90deg);
  }
  .item {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 8px;
    cursor: pointer;
  }
  .name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .grp {
    font-weight: 600;
  }
  .count,
  .host {
    font-size: 12px;
    margin-left: auto;
  }
  .host {
    max-width: 45%;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .tag {
    flex: none;
    width: 8px;
    height: 8px;
    border: 1px solid var(--border);
  }
  .proto {
    font-size: 10px;
    padding: 0 4px;
    border: 1px solid var(--border);
    color: var(--text-2);
  }
  .pw {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 10px;
  }
  .gen {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: -4px;
  }
  .gen button {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: none;
  }
  .genpw {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 12px;
    padding: 3px 6px;
    background: var(--bg);
    border: 1px solid var(--border);
    user-select: all;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .lbl {
    font-size: 12px;
    color: var(--text-2);
  }
  .check {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 13px;
  }
  .warn {
    color: var(--warning);
  }
  .err {
    color: var(--danger);
  }
  footer {
    display: flex;
    gap: 8px;
  }
  .spacer {
    flex: 1;
  }
</style>
