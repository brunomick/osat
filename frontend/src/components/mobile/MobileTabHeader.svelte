<script lang="ts">
  // Android: la barra de arriba de una pestaña a pantalla completa (volver, título, pasar entre
  // terminal y archivos del mismo servidor, cerrar).
  import { t, tr } from '../../lib/i18n'
  import Icon from '../../lib/Icon.svelte'
  import { backFrom, mobileScreen } from '../../lib/mobile'
  import { tabTitle } from '../../lib/tabtitle'
  import { closeTab, LOCAL, openTab, tabs, type Tab, type TabKind } from '../../lib/stores'

  export let tab: Tab

  // Terminal ↔ SFTP del mismo servidor, reutilizando la conexión (como "Abrir SFTP en esta conexión").
  let other: TabKind | null
  $: other = tab.connId && tab.connId !== LOCAL && (tab.kind === 'terminal' || tab.kind === 'sftp') ? (tab.kind === 'terminal' ? 'sftp' : 'terminal') : null

  function switchKind() {
    if (!other) return
    openTab({ kind: other, connId: tab.connId, title: tab.title, color: tab.color, fromSession: tab.sessionId })
  }
</script>

<header style="--c: {tab.color || 'var(--accent)'}">
  <button class="icon" aria-label={$t('mobile.back')} on:click={() => mobileScreen.set(backFrom(tab, $tabs))}>
    <Icon name="back" size={20} />
  </button>
  <span class="title">{tab.path ?? tabTitle($t, tab)}{tab.dirty ? ' ●' : ''}</span>
  {#if other}
    <button class="icon" aria-label={other === 'sftp' ? tr('tabs.openSftpHere') : tr('tabs.openTerminalHere')} title={other === 'sftp' ? $t('tabs.openSftpHere') : $t('tabs.openTerminalHere')} on:click={switchKind}>
      <Icon name={other === 'sftp' ? 'folder' : 'terminal'} size={20} />
    </button>
  {/if}
  <button class="icon" aria-label={$t('common.close')} on:click={() => closeTab(tab.id)}>
    <Icon name="x" size={20} />
  </button>
</header>

<style>
  header {
    flex: none;
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 4px 4px;
    background: var(--bg-2);
    border-bottom: 2px solid var(--c);
  }
  .title {
    flex: 1;
    min-width: 0;
    font-size: 15px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .icon {
    padding: 10px 12px;
    border: 0;
    background: none;
  }
</style>
