<script lang="ts">
  import { t, tr } from '../lib/i18n'
  import { onDestroy, onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { api, errorText, on, type BrowserBounds, type BrowserEvent } from '../lib/api'
  import { layout, paneRects } from '../lib/layout'
  import { overlayOpen } from '../lib/overlay'
  import { credMatches, siteForTab } from '../lib/sites'
  import { normalizeSiteUrl, siteHost } from '../lib/siteUrl'
  import { creds, openBlankSite, openSite, patchTab, reloadCreds, reloadSites, sites, toast, type Tab } from '../lib/stores'
  import { locked } from '../lib/lock'
  import SiteCredsPanel from './SiteCredsPanel.svelte'

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
    if (ev.kind === 'loaded') offerCreds(), refreshIcon()
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
    reloadCreds().catch(() => {})
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

  // El sitio guardado de esta pestaña: el que la abrió aunque la página haya navegado (tras el login, por ejemplo).
  $: saved = siteForTab($sites, tab.siteId, url)
  /** Hay credenciales para el dominio de esta página: la llave se marca (el relleno lo pide la persona). */
  $: hasCreds = $creds.some((c) => credMatches(c, url))

  // ---------- Panel lateral de credenciales (la llave, o solo al cargar un login) ----------

  /** El panel lateral está abierto. */
  let offer = false
  /** Lo abrió offerCreds (no la llave): se cierra solo si la página siguiente no es un login. */
  let auto = false
  /** Dominios donde la persona cerró el panel: en esta pestaña no se vuelve a abrir solo. */
  const dismissed = new Set<string>()
  let offerSeq = 0

  /**
   * Al terminar de cargar una página: si hay credenciales para su dominio y la página tiene un login, la rellena
   * (si hay una sola) o abre el panel (si hay varias); si no, cierra el que se había abierto solo. Reintenta un par de veces porque muchas páginas dibujan el
   * login después de cargar.
   */
  async function offerCreds() {
    const my = ++offerSeq
    for (const wait of [0, 1500, 4000]) {
      if (wait) await new Promise((r) => setTimeout(r, wait))
      if (my !== offerSeq || destroyed) return
      if ($locked || dismissed.has(siteHost(url)) || !$creds.some((c) => credMatches(c, url))) break
      const found = await api.browserHasLogin(id).catch(() => false)
      if (my !== offerSeq || destroyed) return
      if (found) {
        // Una sola credencial para el dominio: no hay nada que elegir, se rellena sin preguntar (sin pisar lo
        // que la persona ya escribió). Si no se puede, queda la llave.
        const mine = $creds.filter((c) => credMatches(c, url))
        if (mine.length === 1) {
          try {
            await api.browserFill(id, mine[0].id, true)
            toast(tr('site.credAutoFilled', { label: mine[0].label }), 'success')
          } catch {
            // nada que rellenar (ya estaba escrito) o la página cambió: sin aviso
          }
          return
        }
        if (!offer) (offer = true), (auto = true)
        return
      }
    }
    if (auto) offer = false
  }

  function dismissOffer() {
    dismissed.add(siteHost(url))
    offer = false
  }

  /** La llave: abre el panel o lo cierra (y entonces no se vuelve a abrir solo en este dominio). */
  function toggleCreds() {
    if (offer) dismissOffer()
    else (offer = true), (auto = false)
  }

  $: if ($locked) offer = false

  /** El ícono de un sitio guardado (el de esta pestaña, si no se dice): Go lo busca en la página si hace falta (no tiene,
   * o es viejo). */
  function refreshIcon(siteId = saved?.id) {
    if (!siteId) return
    api
      .siteIconRefresh(id, siteId)
      .then((changed) => {
        if (changed) return reloadSites()
      })
      .catch(() => {})
  }

  /** La estrella: agrega la página a los sitios guardados (con el título de la página) o la quita. */
  async function toggleSaved() {
    const s = saved
    await act(async () => {
      if (s) {
        await api.siteDelete(s.id)
        patchTab(id, { siteId: undefined })
      } else {
        const n = await api.siteSave({ name: title || siteHost(url), url })
        patchTab(id, { siteId: n.id })
        refreshIcon(n.id)
      }
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
      class="icon"
      class:on={offer}
      class:has={hasCreds}
      title={hasCreds ? $t('site.credsHere') : $t('site.creds')}
      aria-label={hasCreds ? $t('site.credsHere') : $t('site.creds')}
      aria-pressed={offer}
      on:click={toggleCreds}
    >
      <Icon name="key" />
    </button>
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

  <div class="main">
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
    {#if offer}
      <SiteCredsPanel tabId={id} {url} on:dismiss={dismissOffer} on:filled={() => (offer = false)} />
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
  .icon.on:not(.star) {
    color: var(--accent);
  }
  /* Credenciales para esta página: un punto en la llave. */
  .icon.has {
    position: relative;
  }
  .icon.has::after {
    content: '';
    position: absolute;
    top: 3px;
    right: 3px;
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--accent);
  }
  .error {
    padding: 4px 10px;
    background: var(--bg-2);
    border-bottom: 1px solid var(--border);
    color: var(--danger);
    font-size: 12px;
  }
  /* La página y, al costado, el panel de credenciales (que le quita ancho: no puede ir encima). */
  .main {
    flex: 1;
    min-height: 0;
    display: flex;
  }
  .host {
    flex: 1;
    min-width: 0;
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
