<script lang="ts">
  import { onMount } from 'svelte'
  import { api, fmtSize, type SessionStats } from '../lib/api'
  import { t } from '../lib/i18n'
  import { resourceBarMinimized } from '../lib/stores'

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

  // Resumen para el tooltip del botón plegado (y para lectores de pantalla).
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
    <!-- Plegada: un botón flotante abajo a la derecha, donde estaba la barra; un clic la muestra. -->
    <button class="mini" title={summary} aria-label={$t('term.resourceShow')} on:click={() => resourceBarMinimized.set(false)}>
      <span class:warn={cpuLevel === 'warn'} class:crit={cpuLevel === 'crit'}>
        <span class="label">{$t('term.resourceCpu')}</span>
        {cpu == null ? '—' : `${Math.round(cpu)}%`}
      </span>
      {#if stats.memTotal > 0}
        <span class="sep">|</span>
        <span class:warn={memLevel === 'warn'} class:crit={memLevel === 'crit'}>
          <span class="label">{$t('term.resourceRam')}</span>
          {Math.round(memPct)}%
        </span>
      {/if}
    </button>
  {:else}
    <!-- Barra de estado al pie de la terminal. Un clic en cualquier parte la pliega. -->
    <button type="button" class="resbar" title={$t('term.resourceHide')} aria-label={`${$t('term.resourceHide')}: ${summary}`} on:click={() => resourceBarMinimized.set(true)}>
      <span class="item" class:warn={cpuLevel === 'warn'} class:crit={cpuLevel === 'crit'}>
        <span class="label">{$t('term.resourceCpu')}</span>
        <span class="val">{cpu == null ? '—' : `${Math.round(cpu)}%`}</span>
      </span>
      {#if stats.memTotal > 0}
        <span class="sep">|</span>
        <span class="item" class:warn={memLevel === 'warn'} class:crit={memLevel === 'crit'}>
          <span class="label">{$t('term.resourceRam')}</span>
          <span class="val">{fmtSize(stats.memUsed)} / {fmtSize(stats.memTotal)}</span>
        </span>
      {/if}
      {#if stats.diskTotal > 0}
        <span class="sep">|</span>
        <span class="item" class:warn={diskLevel === 'warn'} class:crit={diskLevel === 'crit'}>
          <span class="label">{$t('term.resourceDisk')}</span>
          <span class="val">{fmtSize(stats.diskUsed)} / {fmtSize(stats.diskTotal)}</span>
        </span>
      {/if}
      <span class="sep">|</span>
      <span class="item">
        <span class="label" title={$t('term.resourceNetDown')}>↓</span>
        <span class="val">{fmtSize(stats.netRxBps)}/s</span>
        <span class="label" title={$t('term.resourceNetUp')}>↑</span>
        <span class="val">{fmtSize(stats.netTxBps)}/s</span>
      </span>
    </button>
  {/if}
{/if}

<style>
  /* Barra de estado al pie, como la del panel de Repositorio: fondo de la barra de pestañas y un borde arriba. */
  .resbar {
    flex: none;
    display: flex;
    align-items: center;
    gap: 10px;
    height: 24px;
    padding: 0 12px;
    overflow: hidden;
    background: var(--rail);
    border: 0;
    border-top: 1px solid var(--border);
    font-size: 12px;
    white-space: nowrap;
    user-select: none;
    text-align: left;
  }
  .resbar:hover {
    background: var(--bg-3);
  }
  .item {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
  }
  /* Rótulos en gris tenue y valores un poco más claros, como las IPs de la barra lateral. */
  .label {
    color: var(--text-3);
  }
  .val {
    color: var(--text-2);
    font-variant-numeric: tabular-nums;
  }
  .sep {
    color: var(--text-3);
    opacity: 0.5;
  }
  /* Lo único con color: aviso (80%) y crítico (90%). */
  .warn .val,
  .mini .warn {
    color: var(--warning);
  }
  .crit .val,
  .mini .crit {
    color: var(--danger);
  }
  /* Plegada: botón flotante abajo a la derecha (deja libre la barra de scroll). */
  .mini {
    position: absolute;
    bottom: 6px;
    right: 18px;
    z-index: 4;
    display: inline-flex;
    align-items: center;
    gap: 8px;
    padding: 3px 8px;
    background: var(--rail);
    border: 1px solid var(--border);
    color: var(--text-2);
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    user-select: none;
  }
  .mini:hover {
    background: var(--bg-3);
    color: var(--text);
  }
  .mini .label {
    margin-right: 4px;
  }
</style>
