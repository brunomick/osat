<script lang="ts">
  import { t, tr } from '../lib/i18n'
  import { onMount, tick } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { api, errorText } from '../lib/api'
  import { connectTab, CancelledError } from '../lib/connect'
  import { LOCAL, patchTab, toast, type Tab } from '../lib/stores'

  // Navegador integrado: muestra en la pestaña páginas HTML y servicios web sin salir de osat.
  //  · Con un servidor (connId): todo se ve desde ese servidor. "localhost:3000" es el puerto 3000
  //    del servidor, que llega por la conexión SSH (nada se abre en la red), y una ruta como
  //    /var/www/index.html es un archivo del servidor, servido con su carpeta para que carguen
  //    sus estilos, scripts e imágenes.
  //  · Con connId LOCAL: direcciones de esta PC (localhost:5173...).
  // La página va en un iframe sin permiso para navegar la ventana de osat. Desde el iframe no se
  // puede saber a qué página llegó el usuario con clics, así que la barra muestra lo que se pidió.
  export let tab: Tab

  const remote = tab.connId !== LOCAL

  let sessionId: string | null = null
  let address = ''
  let src = ''
  let nonce = 0 // cambiarlo recarga el iframe
  let error = ''
  let busy = false
  const sites = new Map<string, string>() // destino → dirección local ya abierta

  const within = (p: string, dir: string) => p === dir || p.startsWith(dir === '/' ? '/' : dir + '/')
  const dirOf = (p: string) => p.slice(0, p.lastIndexOf('/')) || '/'
  const isLoopback = (h: string) => h === 'localhost' || h === '::1' || /^127(\.\d{1,3}){3}$/.test(h)

  async function connect() {
    error = ''
    busy = true
    patchTab(tab.id, { status: 'connecting', sessionId: undefined })
    try {
      const info = await connectTab(tab)
      sessionId = info.id
      patchTab(tab.id, { status: 'ready', sessionId: info.id })
    } catch (e) {
      patchTab(tab.id, { status: 'error' })
      error = e instanceof CancelledError ? tr('term.cancelled') : errorText(e)
    } finally {
      busy = false
    }
  }

  function show(url: string, shown: string) {
    src = url
    address = shown
    nonce++
  }

  async function serveFolder(root: string) {
    const key = `dir:${root}`
    let base = sites.get(key)
    if (!base) {
      base = (await api.webServeFolder(tab.id, sessionId!, root)).url
      sites.set(key, base)
    }
    return base
  }

  /** Archivo del servidor; root = carpeta que se sirve (por defecto, la del archivo). */
  async function openFile(file: string, root?: string) {
    const dir = root && within(file, root) ? root : dirOf(file)
    const base = await serveFolder(dir)
    const rel = dir === '/' ? file : file.slice(dir.length)
    show(base + rel.split('/').map(encodeURIComponent).join('/'), file)
  }

  async function openRoot(dir: string) {
    show((await serveFolder(dir)) + '/', dir)
  }

  /** "3000", ":3000/ruta", "localhost:3000", "http://..." → URL (o null si no se entiende). */
  function parse(input: string): URL | null {
    let s = input.trim().replace(/^:?(\d{1,5})(?=$|[/?#])/, 'localhost:$1')
    if (!s) return null
    if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(s)) s = 'http://' + s
    try {
      const u = new URL(s)
      return u.protocol === 'http:' || u.protocol === 'https:' ? u : null
    } catch {
      return null
    }
  }

  async function go(input: string) {
    const s = input.trim()
    if (!s) return
    error = ''
    busy = true
    try {
      if (remote && s.startsWith('/')) {
        if (!sessionId) await connect()
        if (sessionId) await openFile(s)
        return
      }
      const u = parse(s)
      if (!u) {
        toast(tr(remote ? 'browser.needAddressRemote' : 'browser.needAddress'), 'error')
        return
      }
      const host = u.hostname.replace(/^\[|\]$/g, '')
      // Un sitio de internet se carga directo (puede negarse a mostrarse dentro de otra página: ahí sirve el botón de
      // abrirlo en el navegador del sistema). Lo de esta PC o del servidor pasa por un proxy local que saca esa negativa.
      if (!remote && !isLoopback(host)) return show(u.href, u.href)
      if (remote && !sessionId) await connect()
      if (remote && !sessionId) return
      const scheme = u.protocol.slice(0, -1)
      const port = u.port ? Number(u.port) : scheme === 'https' ? 443 : 80
      const key = `${scheme}://${host}:${port}`
      let base = sites.get(key)
      if (!base) {
        base = (remote ? await api.webTunnel(tab.id, sessionId!, scheme, host, port) : await api.webLocal(tab.id, scheme, host, port)).url
        sites.set(key, base)
      }
      show(base + u.pathname + u.search + u.hash, u.href)
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      busy = false
    }
  }

  const reload = () => nonce++

  function openOutside() {
    if (!src) return
    api.webOpenExternal(src).catch((e) => toast(errorText(e), 'error'))
  }

  async function start() {
    if (remote) {
      await connect()
      if (!sessionId) return
    } else {
      patchTab(tab.id, { status: 'ready' })
    }
    try {
      busy = true
      if (tab.path && remote) await openFile(tab.path, tab.initialDir)
      else if (tab.initialDir && remote) await openRoot(tab.initialDir)
      else if (tab.url) await go(tab.url)
    } catch (e) {
      error = errorText(e)
    } finally {
      busy = false
    }
    if (!src) {
      await tick()
      input?.focus()
    }
  }

  let input: HTMLInputElement
  onMount(start)
</script>

<div class="browser">
  <form class="bar" on:submit|preventDefault={() => go(address)}>
    <button type="button" class="icon" title={$t('browser.reload')} disabled={!src} on:click={reload} aria-label={$t('browser.reload')}><Icon name="refresh" /></button>
    <input
      class="mono"
      bind:this={input}
      bind:value={address}
      spellcheck="false"
      placeholder={remote ? $t('browser.placeholderRemote') : 'localhost:3000'}
      aria-label={$t('browser.address')}
    />
    <button type="button" class="icon" title={$t('browser.openSystem')} disabled={!src} on:click={openOutside} aria-label={$t('browser.openSystem')}>
      <Icon name="external" />
    </button>
  </form>

  {#if error}
    <div class="note">
      <p>{error}</p>
      <button class="primary" on:click={start}>{$t('ports.retry')}</button>
    </div>
  {:else if !src}
    <div class="note">
      <Icon name="globe" size={34} />
      <p>{busy ? $t('ports.connecting') : $t('browser.typeAbove')}</p>
      {#if !busy}
        <p class="small">
          {#if remote}
            <span class="mono">localhost:3000</span> {$t('browser.helpA')}
            <span class="mono">/var/www/index.html</span> {$t('browser.helpB')}
          {:else}
            {$t('browser.example')} <span class="mono">localhost:5173</span>.
          {/if}
        </p>
      {/if}
    </div>
  {:else}
    {#key nonce}
      <iframe
        title={address}
        {src}
        sandbox="allow-scripts allow-same-origin allow-forms allow-modals allow-downloads"
        referrerpolicy="no-referrer"
      />
    {/key}
  {/if}
</div>

<style>
  .browser {
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
  iframe {
    flex: 1;
    min-height: 0;
    border: 0;
    background: #fff; /* como un navegador: una página sin fondo propio se ve blanca */
  }
  .note {
    flex: 1;
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
  .small {
    font-size: 12px;
  }
</style>
