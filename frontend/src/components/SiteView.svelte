<script lang="ts">
  import { t, tr } from '../lib/i18n'
  import { onDestroy, onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { api, errorText, on, type BrowserBounds, type BrowserEvent } from '../lib/api'
  import { layout, paneRects } from '../lib/layout'
  import { overlayOpen } from '../lib/overlay'
  import { normalizeSiteUrl, sameSiteUrl, siteHost } from '../lib/siteUrl'
  import { openBlankSite, openSite, patchTab, reloadSites, sites, type Tab } from '../lib/stores'

  // Sitio web de internet: la página va en una vista nativa del sistema (WebView2 / WebKitGTK, internal/browser)
  // que Go pone encima de esta pestaña. Acá está la barra y un div vacío que marca dónde va la vista; la
  // posición y el tamaño de ese div se le mandan a Go cada vez que cambian.
  // La vista nativa se dibuja siempre encima del HTML: se oculta si la pestaña no se ve o si hay algo de osat
  // encima (menús, diálogos…, ver lib/overlay.ts).
  export let tab: Tab
  /** La pestaña se ve (es la activa de su panel). */
  export let active: boolean

  const id = tab.id // lo único de `tab` que no cambia: así lo que depende de él no se recalcula en cada cambio de pestañas
  $: url = tab.url ?? ''
  $: title = tab.title

  let address = tab.url ?? ''
  let canBack = false
  let canFwd = false
  let error = '' // aviso bajo la barra (dirección rara, página que no cargó…)
  let openError = '' // no se pudo crear la vista nativa
  let host: HTMLDivElement
  let input: HTMLInputElement
  let opened = false
  let hasSize = false
  let destroyed = false

  // ---------- Posición y visibilidad de la vista nativa ----------

  let frame = 0
  let last = '' // lo último que se mandó a Go

  /** El rectángulo del div, en píxeles CSS de la ventana (se redondean los bordes, no el ancho: dos vistas pegadas no dejan línea). */
  function measure(): BrowserBounds {
    const r = host.getBoundingClientRect()
    const x = Math.round(r.left)
    const y = Math.round(r.top)
    return { x, y, w: Math.round(r.right) - x, h: Math.round(r.bottom) - y }
  }

  function sync() {
    frame = 0
    if (!opened) return
    const b = measure()
    hasSize = b.w > 0 && b.h > 0
    if (!hasSize) return
    const scale = window.devicePixelRatio || 1
    const key = `${b.x},${b.y},${b.w},${b.h},${scale}`
    if (key === last) return
    last = key
    api.browserSetBounds(id, b, scale).catch(() => {})
  }

  // Una vez por cuadro como mucho. Recibe (y ignora) lo que lo dispara.
  function schedule(..._cause: unknown[]) {
    if (!frame) frame = requestAnimationFrame(sync)
  }

  // El div cambia de lugar con el mosaico (divisores, pestañas que pasan de panel, barra lateral…) además de cambiar de tamaño.
  $: schedule($paneRects, $layout)

  $: show = opened && active && hasSize && !$overlayOpen
  $: if (opened) api.browserSetVisible(id, show).catch(() => {})

  // ---------- Abrir y eventos ----------

  async function open(target = url) {
    openError = ''
    patchTab(id, { status: 'connecting' })
    const b = measure()
    const scale = window.devicePixelRatio || 1
    try {
      await api.browserOpen(id, target, b, scale)
      if (destroyed) return
      last = `${b.x},${b.y},${b.w},${b.h},${scale}`
      hasSize = b.w > 0 && b.h > 0
      opened = true
      patchTab(id, { status: 'ready' })
      schedule() // por si el div cambió de lugar mientras se abría
    } catch (e) {
      if (destroyed) return
      patchTab(id, { status: 'error' })
      openError = errorText(e)
    }
  }

  function onEvent(ev: BrowserEvent) {
    if (ev.id !== id) return
    if (ev.kind === 'newWindow') {
      // La dirección la pide la página: solo http(s).
      const u = normalizeSiteUrl(ev.url)
      if (u) openSite(u)
      return
    }
    if (ev.kind === 'error') {
      error = tr('site.loadError', { url: ev.url })
      return
    }
    canBack = ev.canBack
    canFwd = ev.canFwd
    if (ev.kind === 'title') {
      if (ev.title) patchTab(id, { title: ev.title })
      return
    }
    error = ''
    if (ev.url) {
      patchTab(id, { url: ev.url })
      if (document.activeElement !== input) address = ev.url // si está escribiendo, no se lo pisa
    }
  }

  onMount(() => {
    const off = on('browser:event', onEvent)
    const ro = new ResizeObserver(schedule)
    ro.observe(host)
    window.addEventListener('resize', schedule)
    reloadSites().catch(() => {})
    // Pestaña nueva, sin dirección: la vista nativa se crea cuando se escribe una (go).
    if (url) open()
    else input.focus()
    return () => {
      off()
      ro.disconnect()
      window.removeEventListener('resize', schedule)
    }
  })

  // La vista nativa la cierra closeTab (stores.ts).
  onDestroy(() => {
    destroyed = true
    cancelAnimationFrame(frame)
  })

  // ---------- Barra ----------

  async function act(f: () => Promise<void>) {
    try {
      await f()
    } catch (e) {
      error = errorText(e)
    }
  }

  function go() {
    const u = normalizeSiteUrl(address)
    if (!u) {
      error = tr('site.badUrl')
      return
    }
    error = ''
    address = u
    input.blur()
    if (!opened) {
      patchTab(id, { url: u })
      open(u)
      return
    }
    act(() => api.browserNavigate(id, u))
  }

  $: saved = $sites.find((s) => sameSiteUrl(s.url, url))

  /** La estrella: agrega la página a los sitios guardados (con el título de la página) o la quita. */
  async function toggleSaved() {
    await act(async () => {
      if (saved) await api.siteDelete(saved.id)
      else await api.siteSave({ name: title || siteHost(url), url })
      await reloadSites()
    })
  }
</script>

<div class="site">
  <form class="bar" on:submit|preventDefault={go}>
    <button type="button" class="icon" title={$t('site.back')} aria-label={$t('site.back')} disabled={!canBack} on:click={() => act(() => api.browserBack(id))}>
      <Icon name="back" />
    </button>
    <button type="button" class="icon" title={$t('site.forward')} aria-label={$t('site.forward')} disabled={!canFwd} on:click={() => act(() => api.browserForward(id))}>
      <Icon name="forward" />
    </button>
    <button type="button" class="icon" title={$t('browser.reload')} aria-label={$t('browser.reload')} disabled={!opened} on:click={() => act(() => api.browserReload(id))}>
      <Icon name="refresh" />
    </button>
    <button type="button" class="icon" title={$t('site.newTabTip')} aria-label={$t('site.newTabTip')} on:click={openBlankSite}>
      <Icon name="plus" />
    </button>
    <input class="mono" bind:this={input} bind:value={address} spellcheck="false" aria-label={$t('browser.address')} on:focus={() => input.select()} />
    <button
      type="button"
      class="icon star"
      class:on={!!saved}
      title={saved ? $t('site.unsave') : $t('site.save')}
      aria-label={saved ? $t('site.unsave') : $t('site.save')}
      aria-pressed={!!saved}
      disabled={!opened}
      on:click={toggleSaved}
    >
      <Icon name="star" filled={!!saved} />
    </button>
  </form>
  {#if error}
    <div class="error" role="alert">{error}</div>
  {/if}

  <!-- Acá va la vista nativa (encima de este div). Lo que se ve en el div es el estado mientras no hay vista. -->
  <div class="host" bind:this={host}>
    {#if openError}
      <div class="note">
        <p>{openError}</p>
        <button class="primary" on:click={() => open()}>{$t('ports.retry')}</button>
      </div>
    {:else if !opened}
      <div class="note"><p>{url ? $t('ports.connecting') : $t('site.typeAddress')}</p></div>
    {/if}
  </div>
</div>

<style>
  .site {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 6px 8px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-2);
  }
  .bar input {
    flex: 1;
    min-width: 0;
  }
  .star.on {
    color: var(--warning);
  }
  .error {
    padding: 4px 10px;
    background: var(--bg-2);
    border-bottom: 1px solid var(--border);
    color: var(--danger);
    font-size: 12px;
  }
  .host {
    flex: 1;
    min-height: 0;
    position: relative;
    background: var(--bg);
  }
  .note {
    position: absolute;
    inset: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    padding: 24px;
    text-align: center;
    color: var(--text-3);
  }
  .note p {
    margin: 0;
  }
</style>
