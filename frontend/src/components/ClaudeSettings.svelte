<script lang="ts">
  import { t, tr } from '../lib/i18n'
  import { onMount } from 'svelte'
  import { api, clipboardSet, errorText, type BridgeMode } from '../lib/api'
  import { confirmDialog } from '../lib/dialog'
  import { bridge, loadBridge, toast } from '../lib/stores'

  // Contenido de la sección "Claude MCP" de Configuración.

  let busy = false

  async function run(job: () => Promise<unknown>) {
    busy = true
    try {
      await job()
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      await loadBridge().catch(() => {})
      busy = false
    }
  }

  const toggle = () => run(() => api.bridgeSetEnabled(!$bridge.enabled))

  const setMode = (mode: BridgeMode) => $bridge.mode !== mode && run(() => api.bridgeSetMode(mode))

  async function newToken() {
    const ok = await confirmDialog({
      title: tr('bridge.tokenTitle'),
      message: tr('bridge.tokenMsg'),
      okText: tr('bridge.change'),
    })
    if (ok) run(() => api.bridgeNewToken())
  }

  function copy() {
    clipboardSet($bridge.command)
    toast(tr('bridge.cmdCopied'), 'success')
  }

  function copyPermissions() {
    clipboardSet($bridge.permissions)
    toast(tr('bridge.permCopied'), 'success')
  }

  onMount(() => {
    loadBridge().catch(() => {})
  })
</script>

<div class="body">
    <p class="muted">{$t('bridge.intro')}</p>

    <div class="state">
      <span>
        {#if $bridge.enabled}{$t('bridge.onAt')} <span class="mono">127.0.0.1:{$bridge.port}</span>{:else}{$t('bridge.off')}{/if}
      </span>
      <button class:primary={!$bridge.enabled} disabled={busy} on:click={toggle}>
        {$bridge.enabled ? $t('bridge.turnOff') : $t('bridge.turnOn')}
      </button>
    </div>

    {#if $bridge.enabled}
      {#if $bridge.preferredPort && $bridge.port !== $bridge.preferredPort}
        <p class="warn small">
          {$t('bridge.portBusy', { preferred: $bridge.preferredPort, port: $bridge.port })}
        </p>
      {/if}
      <div class="field">
        <span class="lbl">{$t('bridge.addIt')}</span>
        <pre class="cmd mono">{$bridge.command}</pre>
        <div class="row">
          <button on:click={copy}>{$t('bridge.copyCmd')}</button>
          <button class="ghost" disabled={busy} on:click={newToken}>{$t('bridge.changeToken')}</button>
        </div>
      </div>
      <p class="muted small">
        {$t('bridge.afterwards')}
      </p>

      <div class="field" role="radiogroup" aria-label={$t('bridge.modeGroup')}>
        <span class="lbl">{$t('bridge.modeLabel')}</span>
        <label class="mode" class:on={$bridge.mode === 'ask'}>
          <input type="radio" name="mode" checked={$bridge.mode === 'ask'} disabled={busy} on:change={() => setMode('ask')} />
          <span><b>{$t('bridge.modeAskBold')}</b>{$t('bridge.modeAskRest')}</span>
        </label>
        <label class="mode" class:on={$bridge.mode === 'auto'} class:danger={$bridge.mode === 'auto'}>
          <input type="radio" name="mode" checked={$bridge.mode === 'auto'} disabled={busy} on:change={() => setMode('auto')} />
          <span><b>{$t('bridge.modeAutoBold')}</b>{$t('bridge.modeAutoRest')}</span>
        </label>
      </div>
      {#if $bridge.mode === 'auto'}
        <p class="warn small">
          {$t('bridge.autoWarn')}
        </p>
      {/if}

      <div class="field">
        <span class="lbl">
          {#if $bridge.mode === 'auto'}{$t('bridge.permsAuto')}
          {:else}{$t('bridge.permsAsk')}{/if}
        </span>
        <pre class="cmd mono">{$bridge.permissions}</pre>
        <div class="row"><button on:click={copyPermissions}>{$t('bridge.copyPerms')}</button></div>
      </div>
      <p class="muted small">
        {$t('bridge.pasteA')} <span class="mono">~/.claude/settings.json</span> {$t('bridge.pasteB')} <span class="mono">.claude/settings.json</span> {$t('bridge.pasteC')}
        <b>{$t('bridge.pasteBold')}</b>{$t('bridge.pasteD')}
        {#if $bridge.mode === 'ask'}
          {$t('bridge.askNote')}
        {:else}
          {$t('bridge.autoNote')}
        {/if}
      </p>
    {:else}
      <p class="muted small">{$t('bridge.offNote')}</p>
    {/if}
</div>

<style>
  .body {
    max-width: 760px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  p {
    margin: 0;
    line-height: 1.5;
  }
  .small {
    font-size: 12px;
  }
  .warn {
    padding: 8px 10px;
    border: 1px solid var(--warning);
    color: var(--warning);
  }
  .mode {
    display: flex;
    align-items: flex-start;
    gap: 8px;
    padding: 8px 10px;
    border: 1px solid var(--border);
    color: var(--text-2);
    cursor: pointer;
  }
  .mode.on {
    border-color: var(--text-3);
    color: var(--text);
  }
  .mode.danger {
    border-color: var(--warning);
  }
  .state {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 10px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: 6px;
    font-size: 12px;
    color: var(--text-2);
  }
  .cmd {
    margin: 0;
    padding: 10px 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    color: var(--text);
    font-size: 12px;
    white-space: pre-wrap;
    word-break: break-all;
    user-select: text;
  }
  .row {
    display: flex;
    gap: 8px;
  }
</style>
