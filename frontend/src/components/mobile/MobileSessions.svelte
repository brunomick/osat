<script lang="ts">
  // Android: Sesiones. Las pestañas abiertas como tarjetas (sin las de osat, como ajustes, que se
  // cierran al dejarlas); tocar una la muestra a pantalla completa.
  import { t } from '../../lib/i18n'
  import Icon from '../../lib/Icon.svelte'
  import { mobileScreen } from '../../lib/mobile'
  import { tabTitle } from '../../lib/tabtitle'
  import { activeTabId, closeTab, isAppTab, kindIcon, tabs, type Tab } from '../../lib/stores'

  function show(tab: Tab) {
    activeTabId.set(tab.id)
    mobileScreen.set('tab')
  }
</script>

<div class="sessions">
  <header>
    <span class="title">{$t('mobile.openSessions')}</span>
  </header>
  <div class="list">
    {#each $tabs.filter((t) => !isAppTab(t)) as tab (tab.id)}
      <div class="card" class:warn={tab.status === 'error' || tab.status === 'closed'} role="button" tabindex="0" on:click={() => show(tab)} on:keydown={(e) => e.key === 'Enter' && show(tab)}>
        <Icon name={kindIcon[tab.kind]} size={18} />
        <div class="main">
          <span class="name">{tab.path ?? tabTitle($t, tab)}{tab.dirty ? ' ●' : ''}</span>
          <small>{$t(`mobile.status.${tab.status}`)}</small>
        </div>
        <button class="icon" aria-label={$t('common.close')} on:click|stopPropagation={() => closeTab(tab.id)}>
          <Icon name="x" size={18} />
        </button>
      </div>
    {:else}
      <div class="empty">
        <p>{$t('mobile.noSessions')}</p>
        <button on:click={() => mobileScreen.set('home')}>{$t('mobile.goHome')}</button>
      </div>
    {/each}
  </div>
</div>

<style>
  .sessions {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }
  header {
    padding: 14px 16px 10px;
  }
  .title {
    font-size: 18px;
    font-weight: 600;
  }
  .list {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 0 12px 12px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .card {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 4px 12px 14px;
    border-radius: 10px;
    background: var(--bg-2);
  }
  .card.warn small {
    color: var(--warning);
  }
  .main {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .name {
    font-size: 15px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  small {
    color: var(--text-3);
    font-size: 12px;
  }
  .icon {
    padding: 8px 10px;
    border: 0;
    background: none;
  }
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 10px;
    padding: 48px 24px;
    color: var(--text-2);
  }
  .empty button {
    min-width: 200px;
    padding: 12px;
  }
</style>
