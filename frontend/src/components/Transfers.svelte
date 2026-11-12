<script lang="ts">
  import { onDestroy } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { t as tl, tr } from '../lib/i18n'
  import { api, errorText, fmtSize, type Transfer } from '../lib/api'
  import { transfers, transfersOpen, clearFinishedTransfers, toast } from '../lib/stores'

  // Reintentar: un archivo grande cortado sigue desde lo que ya se había transferido, si es seguro.
  const retry = (t: Transfer) => api.retryTransfer(t.id).catch((e) => toast(errorText(e), 'error', 6000))
  const retryTitle = (t: Transfer, tf: typeof tr) => (t.resumable && t.resumeFrom ? tf('transfers.retryFrom', { size: fmtSize(t.resumeFrom) }) : tf('transfers.retry'))

  // Velocidad estimada por transferencia.
  const samples = new Map<string, { t: number; done: number; speed: number }>()

  function speed(t: Transfer) {
    const now = performance.now()
    const s = samples.get(t.id)
    if (!s) {
      samples.set(t.id, { t: now, done: t.done, speed: 0 })
      return 0
    }
    if (now - s.t > 500) {
      const sp = ((t.done - s.done) * 1000) / (now - s.t)
      s.speed = s.speed ? s.speed * 0.6 + sp * 0.4 : sp
      s.t = now
      s.done = t.done
    }
    return s.speed
  }

  $: list = Object.values($transfers).sort((a, b) => b.started - a.started)
  $: running = list.filter((t) => t.status === 'running' || t.status === 'queued')
  $: totalPct = (() => {
    const tot = running.reduce((a, t) => a + t.total, 0)
    return tot ? Math.round((running.reduce((a, t) => a + t.done, 0) / tot) * 100) : 0
  })()

  // Terminado todo bien (sin errores ni avisos), el panel se colapsa solo a los 2 segundos: queda la barra
  // de abajo para volver a abrirlo. Si en ese rato arranca otra, se espera a que termine también.
  let hideTimer: ReturnType<typeof setTimeout> | undefined
  $: allFine = list.length > 0 && list.every((t) => (t.status === 'done' || t.status === 'canceled') && !t.warning)
  $: {
    clearTimeout(hideTimer)
    hideTimer = allFine ? setTimeout(() => transfersOpen.set(false), 2000) : undefined
  }
  onDestroy(() => clearTimeout(hideTimer))

  const pct = (t: Transfer) => (t.total ? Math.min(100, Math.round((t.done / t.total) * 100)) : t.status === 'done' ? 100 : 0)
  $: statusText = {
    queued: $tl('transfers.queued'),
    running: '',
    done: $tl('transfers.done'),
    error: $tl('transfers.error'),
    canceled: $tl('transfers.canceled'),
  } as Record<Transfer['status'], string>
</script>

{#if list.length}
  <section class:open={$transfersOpen}>
    <button class="head" on:click={() => transfersOpen.update((v) => !v)}>
      <Icon name="swap" size={14} />
      {$tl('transfers.title')}
      {#if running.length}
        <span class="badge">{$tl('transfers.active', { count: running.length, pct: totalPct })}</span>
      {:else}
        <span class="muted">({list.length})</span>
      {/if}
      <span class="spacer" />
      {#if $transfersOpen && list.length > running.length}
        <span class="link" role="button" tabindex="0" on:click|stopPropagation={clearFinishedTransfers} on:keydown={() => {}}>
          {$tl('transfers.clear')}
        </span>
      {/if}
      <span class="chev" class:up={!$transfersOpen}><Icon name="chevron" size={12} /></span>
    </button>

    {#if $transfersOpen}
      <div class="items">
        {#each list as t (t.id)}
          <div class="item {t.status}">
            <span class="dir" title={t.direction === 'up' ? $tl('transfers.up') : t.direction === 'down' ? $tl('transfers.down') : $tl('transfers.sync')}>
              <Icon name={t.direction === 'up' ? 'upload' : t.direction === 'down' ? 'download' : 'swap'} size={14} />
            </span>
            <div class="info">
              <div class="line">
                <span class="name" title="{t.source} → {t.target}">{t.name}</span>
                <span class="meta">
                  {#if t.status === 'running'}
                    {fmtSize(t.done)} / {fmtSize(t.total)} · {fmtSize(speed(t))}/s
                  {:else if t.status === 'error'}
                    <span class="err" title={t.error}>{t.error}</span>
                  {:else}
                    {statusText[t.status]} · {fmtSize(t.total)}
                  {/if}
                </span>
              </div>
              <div class="bar"><div style="width:{pct(t)}%" /></div>
              {#if t.warning}<div class="warn" title={t.warning}>{t.warning}</div>{/if}
            </div>
            {#if t.status === 'running' || t.status === 'queued'}
              <button class="icon" title={$tl('transfers.cancel')} on:click={() => api.cancelTransfer(t.id)} aria-label={$tl('transfers.cancel')}><Icon name="x" size={13} /></button>
            {:else if t.status === 'error'}
              <button class="icon" title={retryTitle(t, $tl)} aria-label={retryTitle(t, $tl)} on:click={() => retry(t)}><Icon name="refresh" size={13} /></button>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </section>
{/if}

<style>
  section {
    border-top: 1px solid var(--border);
    background: var(--bg-2);
    flex: none;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    background: transparent;
    border: 0;
    padding: 6px 12px;
    font-size: 12px;
    color: var(--text-2);
  }
  .badge {
    background: var(--bg-4);
    color: var(--text);
    padding: 1px 8px;
    font-size: 11px;
  }
  .spacer {
    flex: 1;
  }
  .link {
    color: var(--accent);
    cursor: pointer;
  }
  .chev {
    display: inline-flex;
    transform: rotate(90deg);
  }
  .chev.up {
    transform: rotate(-90deg);
  }
  .items {
    max-height: 200px;
    overflow: auto;
    padding: 0 8px 8px;
  }
  .item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 4px;
  }
  .dir {
    display: inline-flex;
    color: var(--text-3);
  }
  .info {
    flex: 1;
    min-width: 0;
  }
  .line {
    display: flex;
    justify-content: space-between;
    gap: 12px;
    font-size: 12px;
  }
  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .meta {
    color: var(--text-3);
    white-space: nowrap;
    max-width: 50%;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .err {
    color: var(--danger);
  }
  .warn {
    margin-top: 3px;
    font-size: 11px;
    color: var(--warning);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .bar {
    height: 3px;
    background: var(--bg-4);
    margin-top: 4px;
    overflow: hidden;
  }
  .bar div {
    height: 100%;
    background: var(--accent);
  }
  .item.done .bar div {
    background: var(--success);
  }
  .item.error .bar div {
    background: var(--danger);
  }
  .item.canceled .bar div {
    background: var(--text-3);
  }
</style>
