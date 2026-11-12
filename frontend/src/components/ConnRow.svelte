<script lang="ts">
  import { createEventDispatcher } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import type { Connection } from '../lib/api'
  import { openForConnection, openPorts } from '../lib/stores'
  import { t } from '../lib/i18n'
  import { defaultPort, formFields, protocolOf } from '../lib/protocol'

  // Fila de una conexión de la barra lateral: la usan los grupos y Favoritos.
  export let c: Connection
  export let depth = 0
  export let connected = false
  export let dragging = false
  export let dropBefore = false
  export let dropAfter = false
  /** Fila de Favoritos: no es destino de un grupo, tiene su propio atributo de destino. */
  export let fav = false

  const dispatch = createEventDispatcher<{ edit: Connection }>()

  function onKey(e: KeyboardEvent) {
    if (e.key === 'Enter') openForConnection(c, e.shiftKey ? 'sftp' : 'terminal')
  }
</script>

<div
  class="item"
  class:dragging
  class:drop-before={dropBefore}
  class:drop-after={dropAfter}
  role="treeitem"
  aria-level={fav ? 2 : depth + 1}
  aria-selected="false"
  tabindex="-1"
  style="--d: {depth}"
  title="{formFields(protocolOf(c)).user && c.user ? `${c.user}@` : ''}{c.host}{c.port !== defaultPort[protocolOf(c)] ? `:${c.port}` : ''} · {c.protocol === 'rdp' || c.protocol === 'vnc'
    ? $t('conn.tipRdp')
    : $t('conn.tipSsh')}"
  data-drop-group={fav ? undefined : c.group}
  data-fav-row={fav ? c.id : undefined}
  data-conn-row={c.id}
  on:mousedown
  on:dblclick={() => openForConnection(c, 'terminal')}
  on:keydown={onKey}
>
  <span class="tag" style="background:{connected ? 'var(--success)' : c.color || 'var(--bg)'}" title={connected ? $t('conn.connected') : ''} />
  <span class="guides">{#each Array(depth) as _}<span class="guide" />{/each}</span>
  <span class="name">{c.name}</span>
  {#if c.useDefaultCreds}
    <span class="defaults" title={$t('conn.usesDefaults')}><Icon name="key" size={12} /></span>
  {/if}
  <span class="host">{c.host}{c.protocol !== 'rdp' && c.port !== defaultPort[protocolOf(c)] ? `:${c.port}` : ''}</span>
  <div class="actions">
    {#if c.protocol === 'rdp'}
      <button class="icon" tabindex="-1" title={$t('conn.openRdp')} on:click|stopPropagation={() => openForConnection(c, 'rdp')} aria-label={$t('conn.openRdp')}>
        <Icon name="monitor" size={14} />
      </button>
    {:else if c.protocol === 'vnc'}
      <button class="icon" tabindex="-1" title={$t('conn.openRdp')} on:click|stopPropagation={() => openForConnection(c, 'vnc')} aria-label={$t('conn.openRdp')}>
        <Icon name="vnc" size={14} />
      </button>
    {:else}
      <button class="icon" tabindex="-1" title={$t('conn.openTerminal')} on:click|stopPropagation={() => openForConnection(c, 'terminal')} aria-label={$t('conn.openTerminal')}>
        <Icon name="terminal" size={14} />
      </button>
      <button class="icon" tabindex="-1" title={$t('conn.openSftp')} on:click|stopPropagation={() => openForConnection(c, 'sftp')} aria-label={$t('conn.openSftp')}>
        <Icon name="folder" size={14} />
      </button>
      <button class="icon" tabindex="-1" title={$t('conn.portsTip')} on:click|stopPropagation={() => openPorts(c)} aria-label={$t('conn.ports')}>
        <Icon name="ports" size={14} />
      </button>
    {/if}
    <button class="icon" tabindex="-1" title={$t('conn.edit')} on:click|stopPropagation={() => dispatch('edit', c)} aria-label={$t('conn.edit')}>
      <Icon name="edit" size={14} />
    </button>
  </div>
</div>

<style>
  .item {
    position: relative;
    display: flex;
    align-items: center;
    height: 24px;
    /* El nombre queda alineado con el de su grupo; las guías marcan el nivel. */
    padding: 0 10px 0 7px;
    cursor: default;
    outline: none;
  }
  .item:hover,
  .item:focus-visible {
    background: var(--bg-3);
  }
  .guides {
    display: flex;
    align-self: stretch;
    flex: none;
  }
  .guide {
    width: 10px;
    background: linear-gradient(var(--guide), var(--guide)) 4px 0 / 1px 100% no-repeat;
  }
  .dragging {
    opacity: 0.45;
  }
  .drop-before {
    box-shadow: inset 0 2px 0 var(--accent);
  }
  .drop-after {
    box-shadow: inset 0 -2px 0 var(--accent);
  }
  .tag {
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    width: 3px;
  }
  .name {
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .defaults {
    display: inline-flex;
    flex-shrink: 0;
    color: var(--text-3);
  }
  .host {
    flex: none;
    margin-left: 8px;
    max-width: 50%;
    font-size: 11px;
    color: var(--text-3);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* Al pasar el mouse, las acciones ocupan el lugar de la IP. */
  .actions {
    display: none;
    gap: 2px;
    margin-left: 8px;
  }
  .actions button.icon {
    padding: 0 4px;
  }
  .item:hover .actions,
  .item:focus-within .actions {
    display: flex;
  }
  .item:hover .host,
  .item:focus-within .host {
    display: none;
  }
</style>
