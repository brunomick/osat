<script lang="ts">
  import { onMount } from 'svelte'
  import { api, fmtSize, type SessionStats } from '../lib/api'
  import { t } from '../lib/i18n'
  import { resourceBarMinimized } from '../lib/stores'
  import Icon from '../lib/Icon.svelte'

  export let sessionId: string | null = null
  /** La pestaña está visible: solo entonces se consulta. */
  export let active: boolean

  const POLL_MS = 5000
  const MAX_FAILURES = 3
  const MAX_BACKOFF = 60000

  let stats: SessionStats | null = null
  let ready = false
  let watch = ''
  let watchedSession = ''
  let timer: ReturnType<typeof setTimeout> | undefined
  let failures = 0
  let gen = 0

  function cancel() {
    gen++
    if (timer) {
      clearTimeout(timer)
      timer = undefined
    }
  }

  function start() {
    cancel()
    failures = 0
    if (!active || !sessionId) return
    const mine = gen
    void poll(mine)
  }

  async function poll(mine: number) {
    if (mine !== gen || !active) return
    const id = sessionId
    if (!id) return
    try {
      const next = await api.sessionStats(id)
      if (mine !== gen) return
      stats = next
      failures = 0
      if (!next.supported) return
    } catch {
      if (mine !== gen) return
      failures++
      if (failures >= MAX_FAILURES) {
        stats = null
        return
      }
      timer = setTimeout(() => poll(mine), Math.min(MAX_BACKOFF, POLL_MS * 2 ** failures))
      return
    }
    if (mine !== gen) return
    timer = setTimeout(() => poll(mine), POLL_MS)
  }

  $: if (ready) {
    const key = `${sessionId ?? ''}|${active ? 1 : 0}`
    if (key !== watch) {
      watch = key
      if ((sessionId ?? '') !== watchedSession) {
        watchedSession = sessionId ?? ''
        stats = null
      }
      start()
    }
  }

  onMount(() => {
    ready = true
    return cancel
  })

  const level = (p: number) => (p >= 90 ? 'crit' : p >= 80 ? 'warn' : '')

  $: cpu = stats?.cpuPercent ?? null
  $: memPct = stats && stats.memTotal > 0 ? (stats.memUsed / stats.memTotal) * 100 : 0
  $: diskPct = stats && stats.diskTotal > 0 ? (stats.diskUsed / stats.diskTotal) * 100 : 0
  $: cpuLevel = cpu == null ? '' : level(cpu)
  $: memLevel = level(memPct)
  $: diskLevel = level(diskPct)

  // Resumen para el tooltip del chip plegado (y para lectores de pantalla).
  $: summary = stats
    ? [
        `${$t('term.resourceCpu')} ${cpu == null ? '—' : `${Math.round(cpu)}%`}`,
        stats.memTotal > 0 ? `${$t('term.resourceRam')} ${Math.round(memPct)}%` : '',
        stats.diskTotal > 0 ? `${$t('term.resourceDisk')} ${Math.round(diskPct)}%` : '',
        `${$t('term.resourceNet')} ↓ ${fmtSize(stats.netRxBps)}/s ↑ ${fmtSize(stats.netTxBps)}/s`,
      ]
        .filter(Boolean)
        .join(' · ')
    : ''
</script>

{#if stats?.supported}
  {#if $resourceBarMinimized}
    <button class="mini" title={summary} aria-label={$t('term.resourceShow')} on:click={() => resourceBarMinimized.set(false)}>
      <Icon name="eye" size={12} />
      <span>{cpu == null ? '—' : `${Math.round(cpu)}%`}</span>
      {#if stats.memTotal > 0}
        <span class="dim">·</span>
        <span>{Math.round(memPct)}%</span>
      {/if}
    </button>
  {:else}
    <button
      type="button"
      class="resbar"
      aria-label={$t('term.resourceHide')}
      title={$t('term.resourceHide')}
      on:click={() => resourceBarMinimized.set(true)}
    >
      <span class="item" class:warn={cpuLevel === 'warn'} class:crit={cpuLevel === 'crit'}>
        <span class="label">{$t('term.resourceCpu')}</span>
        <span class="val">{cpu == null ? '—' : `${Math.round(cpu)}%`}</span>
      </span>
      {#if stats.memTotal > 0}
        <span class="item" class:warn={memLevel === 'warn'} class:crit={memLevel === 'crit'}>
          <span class="label">{$t('term.resourceRam')}</span>
          <span class="val">{fmtSize(stats.memUsed)} / {fmtSize(stats.memTotal)}</span>
        </span>
      {/if}
      {#if stats.diskTotal > 0}
        <span class="item" class:warn={diskLevel === 'warn'} class:crit={diskLevel === 'crit'}>
          <span class="label">{$t('term.resourceDisk')}</span>
          <span class="val">{fmtSize(stats.diskUsed)} / {fmtSize(stats.diskTotal)}</span>
        </span>
      {/if}
      <span class="item net">
        <span class="label">{$t('term.resourceNet')}</span>
        <span class="val" title={$t('term.resourceNetDown')}>↓ {fmtSize(stats.netRxBps)}/s</span>
        <span class="val" title={$t('term.resourceNetUp')}>↑ {fmtSize(stats.netTxBps)}/s</span>
      </span>
    </button>
  {/if}
{/if}

<style>
  .resbar {
    flex: none;
    display: flex;
    align-items: center;
    gap: 14px;
    height: 28px;
    padding: 0 10px;
    overflow: hidden;
    background: var(--bg-2);
    border: 0;
    border-bottom: 1px solid var(--border);
    color: var(--text-3);
    font-size: 11px;
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
    user-select: none;
    text-align: left;
    cursor: pointer;
  }
  .resbar:hover {
    background: var(--bg-3);
  }
  .item {
    display: inline-flex;
    align-items: baseline;
    gap: 5px;
    min-width: 0;
  }
  .item + .item {
    padding-left: 14px;
    border-left: 1px solid var(--border);
  }
  .label {
    font-size: 10px;
    color: var(--text-3);
  }
  .val {
    color: var(--text-2);
  }
  .item.warn .val {
    color: var(--warning);
  }
  .item.crit .val {
    color: var(--danger);
  }
  .net {
    overflow: hidden;
  }
  /* Plegada: un chip discreto en la esquina; un clic la muestra de nuevo. */
  .mini {
    position: absolute;
    top: 6px;
    right: 18px;
    z-index: 4;
    display: inline-flex;
    align-items: center;
    gap: 5px;
    height: 20px;
    padding: 0 7px;
    background: var(--rail);
    border: 1px solid var(--border);
    color: var(--text-3);
    font-size: 10px;
    font-variant-numeric: tabular-nums;
    opacity: 0.72;
  }
  .mini:hover {
    opacity: 1;
    color: var(--text-2);
    border-color: var(--text-3);
  }
  .dim {
    opacity: 0.55;
  }
</style>
