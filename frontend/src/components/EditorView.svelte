<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { api, errorText } from '../lib/api'
  import { t, tr } from '../lib/i18n'
  import { connectTab, CancelledError } from '../lib/connect'
  import { handoffs, LOCAL, patchTab, toast, type Tab } from '../lib/stores'
  import { highlightCode, languageFor, MAX_HIGHLIGHT } from '../lib/syntax'
  import { keys } from '../lib/platform'
  import { askRoot, isPermissionError, withSudoPassword } from '../lib/sudo'

  // Editor de un archivo remoto, como pestaña o embebido abajo del explorador.
  // Abre su propia sesión SFTP sobre la conexión del explorador (fromSession),
  // así sigue funcionando aunque se cierre la pestaña del explorador.
  // Con connId LOCAL edita un archivo de esta PC (sin sesión).
  export let tab: Tab
  /** Visible y en el panel enfocado: es la que recibe el teclado. */
  export let focused = false
  /** Embebido en el explorador: cierra su propia sesión y no roba el foco. */
  export let embedded = false
  /** Embebido, pero con el foco en cuanto se muestra (el editor de un repositorio). */
  export let autofocus = false
  /** Hay cambios sin guardar (bindable, lo usa el explorador cuando está embebido). */
  export let dirty = false
  /** Embebido en el explorador: pasar a root (tras un permiso denegado) lleva el archivo a su propia pestaña. */
  export let toRoot: (() => void) | null = null

  /** Entrega sesión y texto (con cambios sin guardar) para que una pestaña
   * los adopte. Después de esto esta vista ya no cierra la sesión. Si la
   * sesión todavía se está abriendo, sessionId es '' y la pestaña abre otra. */
  export function detach(): { sessionId: string; text: string; saved: string } | null {
    if (loading || error) return null
    const out = { sessionId: sessionId ?? '', text, saved }
    sessionId = null
    return out
  }

  let sessionId: string | null = null
  let pendingSession: Promise<string> | null = null
  let destroyed = false
  let saved = '' // contenido que hay en el servidor, tal cual (con sus saltos de línea)
  let text = '' // lo que se edita: un <textarea> siempre usa LF
  let savedLF = '' // `saved` con LF, para comparar con `text`
  let loading = true
  let saving = false
  let error = ''
  let area: HTMLTextAreaElement
  let fontSize = 13
  const local = tab.connId === LOCAL
  // Como root: lee y guarda con sudo en el servidor (ver lib/sudo.ts).
  let sudo = !!tab.sudo

  // Tras un permiso denegado, pasa a root. Embebido en el explorador, eso lo hace toRoot (el archivo
  // se va a su propia pestaña) y devuelve false: esta vista ya no sigue.
  function becomeRoot() {
    if (toRoot) {
      toRoot()
      return false
    }
    sudo = true
    patchTab(tab.id, { sudo: true })
    return true
  }

  // El <textarea> devuelve siempre saltos LF, aunque el archivo tenga CRLF (Windows):
  // se edita con LF y al guardar se vuelve al estilo que tenía el archivo (el que
  // predomina), para no reescribir todos sus saltos de línea.
  const toLF = (s: string) => s.replace(/\r\n?/g, '\n')
  function eolOf(s: string) {
    let crlf = 0
    let lf = 0
    for (let i = s.indexOf('\n'); i !== -1; i = s.indexOf('\n', i + 1)) s[i - 1] === '\r' ? crlf++ : lf++
    return crlf > lf ? '\r\n' : '\n'
  }
  $: savedLF = toLF(saved)

  $: dirty = !loading && !error && text !== savedLF
  $: if (dirty !== !!tab.dirty) patchTab(tab.id, { dirty })

  // Un textarea con texto recién puesto deja el cursor (y el scroll) al final:
  // se abre desde arriba, como nano.
  function toTop() {
    if (!area) return
    area.setSelectionRange(0, 0)
    area.scrollTop = 0
    area.scrollLeft = 0
  }

  // La sesión SFTP propia del editor (ver arriba). Con texto recibido del
  // explorador solo hace falta para guardar, así que se abre en segundo plano.
  function ensureSession(): Promise<string> {
    if (sessionId) return Promise.resolve(sessionId)
    pendingSession ??= connectTab(tab)
      .then((info) => {
        // El editor se cerró mientras se abría: nadie más la va a cerrar.
        if (destroyed) {
          api.closeSession(info.id).catch(() => {})
          throw new CancelledError()
        }
        sessionId = info.id
        patchTab(tab.id, { sessionId: info.id })
        return info.id
      })
      .finally(() => (pendingSession = null))
    return pendingSession
  }

  async function load() {
    const h = handoffs.get(tab.id)
    if (h) {
      // Texto ya leído (por el explorador al abrirlo, o al pasar del panel de
      // abajo a una pestaña): se muestra sin volver a pedirlo al servidor.
      handoffs.delete(tab.id)
      saved = h.saved ?? ''
      text = toLF(h.text ?? saved)
      loading = false
      patchTab(tab.id, { status: 'ready' })
      if (tab.sessionId) sessionId = tab.sessionId
      else if (!local) ensureSession().catch(() => {}) // si falla, se reintenta al guardar
      await tick()
      toTop()
      if (focused && (!embedded || autofocus)) area?.focus()
      if (h.save) save()
      return
    }
    error = ''
    loading = true
    patchTab(tab.id, { status: 'connecting' })
    try {
      let raw: string
      if (local) raw = await api.localReadText(tab.path!)
      else {
        const sid = await ensureSession()
        raw = sudo
          ? await withSudoPassword((pw) => api.sftpSudoReadText(sid, tab.path!, pw))
          : await api.sftpReadText(sid, tab.path!)
      }
      saved = raw
      text = toLF(raw)
      patchTab(tab.id, { status: 'ready' })
      loading = false
      await tick()
      toTop()
      if (focused) area?.focus()
    } catch (e) {
      loading = false
      patchTab(tab.id, { status: 'error' })
      error = e instanceof CancelledError ? tr('term.cancelled') : errorText(e)
      if (!local && !sudo && isPermissionError(e) && (await askRoot('read')) && becomeRoot()) load()
    }
  }

  // Reintentar con una sesión nueva: la anterior pudo haberse caído.
  function retry() {
    if (sessionId) api.closeSession(sessionId).catch(() => {})
    sessionId = null
    load()
  }

  async function save() {
    if (!dirty || saving) return
    saving = true
    const content = eolOf(saved) === '\r\n' ? text.replace(/\n/g, '\r\n') : text
    let failed = false
    let err: unknown
    try {
      if (local) await api.localWriteText(tab.path!, content)
      else {
        const sid = await ensureSession()
        if (sudo) await withSudoPassword((pw) => api.sftpSudoWriteText(sid, tab.path!, content, pw))
        else await api.sftpWriteText(sid, tab.path!, content)
      }
      saved = content
      toast(tr('editor.saved', { name: tab.title }), 'success', 2000)
    } catch (e) {
      failed = true
      err = e
    } finally {
      saving = false
    }
    if (!failed) return
    if (!local && !sudo && isPermissionError(err) && (await askRoot('save'))) {
      if (becomeRoot()) save()
      return
    }
    toast(err instanceof CancelledError ? tr('term.cancelled') : errorText(err), 'error')
  }

  const setFont = (size: number) => (fontSize = Math.max(9, Math.min(28, size)))

  // Ctrl + rueda: zoom del texto (en vez de hacer zoom de toda la ventana).
  function onWheel(e: WheelEvent) {
    if (!(e.ctrlKey || e.metaKey) || !e.deltaY) return
    e.preventDefault()
    setFont(fontSize + (e.deltaY < 0 ? 1 : -1))
  }

  // ---------- Buscar (Ctrl+F) ----------
  // El editor es un <textarea>: la coincidencia actual se marca como selección
  // y se mueve el scroll a mano hasta ella. Pero la selección de un textarea sin
  // foco (el foco está en la caja de búsqueda) el navegador la pinta casi
  // invisible, así que el resaltado de verdad es una capa detrás del textarea
  // (transparente) con el mismo texto, donde las coincidencias van en <mark>.
  const MAX_MATCHES = 20000
  let findOpen = false
  let query = ''
  let matchCase = false
  let matches: number[] = [] // posición de cada coincidencia en `text`
  let cur = -1 // la coincidencia seleccionada (índice en matches), -1 si ninguna
  let findInput: HTMLInputElement
  let measureCtx: CanvasRenderingContext2D | null = null

  function findAll(s: string, q: string, cs: boolean) {
    const re = new RegExp(q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), cs ? 'g' : 'gi')
    const out: number[] = []
    for (const m of s.matchAll(re)) {
      out.push(m.index!)
      if (out.length >= MAX_MATCHES) break
    }
    return out
  }

  function syncMatches(s: string) {
    matches = query ? findAll(s, query, matchCase) : []
    if (cur >= matches.length) cur = matches.length - 1
  }
  // Al editar con la barra abierta, las coincidencias se recalculan.
  $: if (findOpen) syncMatches(text)

  let countLabel = ''
  $: {
    const total = matches.length >= MAX_MATCHES ? `${MAX_MATCHES}+` : String(matches.length)
    countLabel = !query ? '' : !matches.length ? tr('term.noResults') : cur < 0 ? tr('term.results', { count: total }) : tr('term.resultOf', { index: cur + 1, count: total })
  }

  // Capa de resaltado: HTML con el texto escapado y las coincidencias en <mark>
  // (la actual con class="cur"). Va después del bloque que recalcula `matches`.
  let backInner: HTMLDivElement
  let highlightHtml = ''
  const escapeHtml = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')

  function buildHighlight(s: string, ms: number[], current: number, len: number) {
    let out = ''
    let pos = 0
    for (let i = 0; i < ms.length; i++) {
      const a = ms[i]
      if (a < pos) continue
      out += escapeHtml(s.slice(pos, a)) + (i === current ? '<mark class="cur">' : '<mark>') + escapeHtml(s.slice(a, a + len)) + '</mark>'
      pos = a + len
    }
    return out + escapeHtml(s.slice(pos))
  }
  $: highlightHtml = findOpen && query && matches.length ? buildHighlight(text, matches, cur, query.length) : ''

  // ---------- Colores de sintaxis ----------
  // Misma idea: una capa detrás del textarea con el texto coloreado (highlight.js,
  // según la extensión), y el texto del textarea pasa a ser transparente (queda
  // el cursor y la selección). Si no hay lenguaje, el archivo es muy grande o
  // falla, no hay capa y el textarea se ve como siempre.
  let synInner: HTMLDivElement
  let lang: string | null = null
  let synHtml: string | null = null
  $: lang = tab.path ? languageFor(tab.path) : null
  $: synHtml = lang && !loading && !error && text.length <= MAX_HIGHLIGHT ? highlightCode(text, lang) : null

  // Las capas acompañan el scroll del textarea (con transform: así no dependen
  // de la barra de scroll del textarea, que las capas no tienen).
  function syncScroll() {
    if (!area) return
    const t = `translate(${-area.scrollLeft}px, ${-area.scrollTop}px)`
    if (backInner) backInner.style.transform = t
    if (synInner) synInner.style.transform = t
  }
  // Una capa recién creada arranca en el scroll actual.
  $: if (backInner || synInner) syncScroll()

  // Lleva el scroll del textarea hasta la posición `start`. Sin ajuste de línea
  // (white-space: pre) y con fuente monoespaciada, la posición sale de cuentas.
  function reveal(start: number) {
    const cs = getComputedStyle(area)
    const lh = parseFloat(cs.lineHeight) || fontSize * 1.5
    let line = 0
    for (let i = text.indexOf('\n'); i !== -1 && i < start; i = text.indexOf('\n', i + 1)) line++
    const lineStart = start === 0 ? 0 : text.lastIndexOf('\n', start - 1) + 1
    let col = 0
    for (let i = lineStart; i < start; i++) col = text[i] === '\t' ? col + 4 - (col % 4) : col + 1 // tab-size: 4
    measureCtx ??= document.createElement('canvas').getContext('2d')
    if (measureCtx) measureCtx.font = `${cs.fontSize} ${cs.fontFamily}`
    const cw = measureCtx?.measureText('M').width || fontSize * 0.6

    // Vertical: centrada si queda fuera (o tapada por la barra de búsqueda).
    const top = parseFloat(cs.paddingTop) + line * lh
    if (top < area.scrollTop + 2 * lh || top + 2 * lh > area.scrollTop + area.clientHeight) {
      area.scrollTop = Math.max(0, top - area.clientHeight / 2)
    }
    const x = parseFloat(cs.paddingLeft) + col * cw
    if (x < area.scrollLeft || x + query.length * cw > area.scrollLeft + area.clientWidth - 24) {
      area.scrollLeft = Math.max(0, x - area.clientWidth / 3)
    }
  }

  function selectMatch(i: number) {
    cur = i
    const start = matches[i]
    area.setSelectionRange(start, start + query.length)
    reveal(start)
  }

  // Cambió el texto buscado o "Aa": salta a la primera coincidencia desde el
  // cursor (así, al escribir más letras, la selección no se va para otro lado).
  function onFindInput() {
    query = findInput.value
    syncMatches(text)
    if (!matches.length) {
      cur = -1
      return
    }
    const from = area.selectionStart
    const i = matches.findIndex((m) => m >= from)
    selectMatch(i < 0 ? 0 : i)
  }

  function toggleCase() {
    matchCase = !matchCase
    onFindInput()
    findInput.focus()
  }

  function step(dir: 1 | -1) {
    if (!area || !query) return
    syncMatches(text)
    const n = matches.length
    if (!n) return
    const a = area.selectionStart
    const b = area.selectionEnd
    let i: number
    if (cur >= 0 && matches[cur] === a && b === a + query.length) {
      i = (cur + dir + n) % n // seguimos desde la coincidencia seleccionada
    } else if (dir > 0) {
      i = matches.findIndex((m) => m >= b) // si no, desde donde quedó el cursor
      if (i < 0) i = 0
    } else {
      i = n - 1
      for (let k = n - 1; k >= 0; k--) {
        if (matches[k] + query.length <= a) {
          i = k
          break
        }
      }
    }
    selectMatch(i)
  }

  async function openFind() {
    if (loading || error || !area) return
    const sel = text.slice(area.selectionStart, area.selectionEnd)
    if (sel && sel.length <= 200 && !/[\r\n]/.test(sel)) query = sel // como VS Code
    findOpen = true
    await tick()
    findInput.focus()
    findInput.select()
    syncMatches(text)
    cur = matches.indexOf(area.selectionStart)
    syncScroll()
  }

  function closeFind() {
    findOpen = false
    area?.focus() // la última coincidencia queda seleccionada
  }

  function onFindKey(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault()
      step(e.shiftKey ? -1 : 1)
    } else if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      closeFind()
    }
  }

  function onKey(e: KeyboardEvent) {
    const mod = e.ctrlKey || e.metaKey
    if (mod && e.key.toLowerCase() === 's') {
      e.preventDefault()
      save()
    } else if (mod && e.key.toLowerCase() === 'f') {
      e.preventDefault()
      openFind()
    } else if (e.key === 'F3' && query) {
      e.preventDefault()
      step(e.shiftKey ? -1 : 1)
    } else if (mod && (e.key === '=' || e.key === '+' || e.key === '-' || e.key === '0')) {
      e.preventDefault()
      setFont(e.key === '0' ? 13 : fontSize + (e.key === '-' ? -1 : 1))
    } else if (e.key === 'Tab' && !e.ctrlKey && e.target === area) {
      e.preventDefault()
      const s = area.selectionStart
      text = text.slice(0, s) + '\t' + text.slice(area.selectionEnd)
      tick().then(() => area.setSelectionRange(s + 1, s + 1))
    }
  }

  $: if (focused && area && !loading && (!embedded || autofocus)) tick().then(() => area.focus())

  onMount(load)
  onDestroy(() => {
    destroyed = true
    if (embedded && sessionId) api.closeSession(sessionId).catch(() => {})
  })
</script>

<div class="editor" role="presentation" on:keydown={onKey} on:wheel={onWheel}>
  <header>
    <Icon name="file" size={13} />
    {#if sudo}<span class="root" title={$t('tabs.rootTip')}>root</span>{/if}
    <span class="mono path" title={tab.path}>{tab.path}{dirty ? ' ●' : ''}</span>
    <span class="muted">{$t('editor.hint', { save: keys('Ctrl+S'), find: keys('Ctrl+F') })}</span>
    <button disabled={!dirty || saving} on:click={save}>{saving ? $t('form.saving') : $t('common.save')}</button>
  </header>
  {#if error}
    <div class="error">
      <p>{error}</p>
      <button on:click={retry}>{$t('ports.retry')}</button>
    </div>
  {:else if loading}
    <div class="error"><p>{$t('snippets.loading')}</p></div>
  {:else}
    <div class="body">
      {#if findOpen}
        <div class="find">
          <input
            bind:this={findInput}
            value={query}
            class:none={query && !matches.length}
            placeholder={$t('sidebar.search')}
            spellcheck="false"
            on:input={onFindInput}
            on:keydown={onFindKey}
          />
          <button class="icon text" class:on={matchCase} aria-pressed={matchCase} title={$t('editor.matchCase')} on:click={toggleCase}>Aa</button>
          <span class="count">{countLabel}</span>
          <button class="icon" title={$t('editor.prev')} disabled={!matches.length} on:click={() => step(-1)} aria-label={$t('editor.prevLabel')}><Icon name="up" size={14} /></button>
          <button class="icon down" title={$t('term.next')} disabled={!matches.length} on:click={() => step(1)} aria-label={$t('editor.nextLabel')}><Icon name="up" size={14} /></button>
          <button class="icon" title={$t('term.closeSearch')} on:click={closeFind} aria-label={$t('common.close')}><Icon name="x" size={14} /></button>
        </div>
      {/if}
      {#if findOpen}
        <div class="layer" aria-hidden="true">
          <div class="mono" bind:this={backInner} style="font-size: {fontSize}px">{@html highlightHtml}</div>
        </div>
      {/if}
      {#if synHtml !== null}
        <div class="layer" aria-hidden="true">
          <div class="mono syn" bind:this={synInner} style="font-size: {fontSize}px">{@html synHtml}</div>
        </div>
      {/if}
      <textarea
        class="mono"
        class:hl={synHtml !== null}
        style="font-size: {fontSize}px"
        bind:this={area}
        bind:value={text}
        on:scroll={syncScroll}
        spellcheck="false"
      />
    </div>
  {/if}
</div>

<style>
  .editor {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  header {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 4px 8px;
    border-bottom: 1px solid var(--border);
  }
  .path {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  /* Abierto como root (sudo). Igual que en TabBar.svelte. */
  .root {
    flex: none;
    padding: 0 5px;
    font-size: 10px;
    font-weight: 600;
    line-height: 16px;
    color: var(--bg);
    background: var(--danger);
    border-radius: 3px;
  }
  .body {
    flex: 1;
    min-height: 0;
    display: flex;
    position: relative;
    background: var(--bg);
  }
  /* Capas detrás del textarea (resaltado de búsqueda y colores de sintaxis):
     mismo texto, tipografía y márgenes que el textarea (si se cambia uno,
     cambiar el otro). Solo color: nada que cambie el ancho de los caracteres. */
  .layer {
    position: absolute;
    inset: 0;
    z-index: 0;
    overflow: hidden;
    pointer-events: none;
  }
  .layer > div {
    padding: 8px 12px;
    line-height: 1.5;
    tab-size: 4;
    white-space: pre;
    width: max-content;
    min-width: 100%;
    color: transparent;
  }
  .layer > .syn {
    color: var(--text);
  }
  .layer :global(mark) {
    background: var(--find-bg);
    color: transparent;
  }
  .layer :global(mark.cur) {
    background: var(--find-cur-bg);
    outline: 1px solid var(--find-cur-outline);
  }
  /* Con colores, el texto lo pinta la capa: el textarea solo pone cursor y selección. */
  textarea.hl {
    color: transparent;
    caret-color: var(--text);
  }
  textarea.hl::selection {
    background: var(--sel-bg);
    color: transparent;
  }
  /* Paleta (--syn-* de app.css, una por tema). Las clases son las de highlight.js. */
  .syn :global(.hljs-comment),
  .syn :global(.hljs-quote) {
    color: var(--syn-comment);
  }
  .syn :global(.hljs-keyword),
  .syn :global(.hljs-selector-tag),
  .syn :global(.hljs-literal),
  .syn :global(.hljs-name),
  .syn :global(.hljs-doctag) {
    color: var(--syn-keyword);
  }
  .syn :global(.hljs-built_in),
  .syn :global(.hljs-type),
  .syn :global(.hljs-title.class_),
  .syn :global(.hljs-title.class_.inherited__) {
    color: var(--syn-type);
  }
  .syn :global(.hljs-title),
  .syn :global(.hljs-title.function_) {
    color: var(--syn-func);
  }
  .syn :global(.hljs-string),
  .syn :global(.hljs-regexp),
  .syn :global(.hljs-meta .hljs-string) {
    color: var(--syn-string);
  }
  .syn :global(.hljs-number) {
    color: var(--syn-number);
  }
  .syn :global(.hljs-meta),
  .syn :global(.hljs-meta .hljs-keyword) {
    color: var(--syn-meta);
  }
  .syn :global(.hljs-attr),
  .syn :global(.hljs-attribute),
  .syn :global(.hljs-variable),
  .syn :global(.hljs-property),
  .syn :global(.hljs-template-variable),
  .syn :global(.hljs-selector-class),
  .syn :global(.hljs-selector-id) {
    color: var(--syn-attr);
  }
  .syn :global(.hljs-symbol),
  .syn :global(.hljs-bullet),
  .syn :global(.hljs-section),
  .syn :global(.hljs-strong),
  .syn :global(.hljs-link) {
    color: var(--syn-symbol);
  }
  .syn :global(.hljs-addition) {
    color: var(--syn-comment);
  }
  .syn :global(.hljs-deletion) {
    color: var(--syn-string);
  }
  .syn :global(.hljs-params),
  .syn :global(.hljs-subst),
  .syn :global(.hljs-punctuation) {
    color: var(--text);
  }
  /* Arriba a la derecha del texto, sin tapar la barra de scroll. */
  .find {
    position: absolute;
    top: 6px;
    right: 22px;
    z-index: 2;
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 4px 6px;
    background: var(--bg-3);
    border: 1px solid var(--border);
    box-shadow: 0 2px 8px var(--shadow-sm);
  }
  .find input {
    width: 190px;
  }
  .find input.none {
    border-color: var(--danger);
    outline-color: var(--danger);
  }
  .find .text {
    font-size: 12px;
    padding-inline: 5px;
  }
  .find .on {
    background: var(--bg-4);
    color: var(--text);
  }
  .find .down :global(svg) {
    transform: rotate(180deg);
  }
  .count {
    min-width: 78px;
    text-align: center;
    font-size: 12px;
    color: var(--text-2);
    white-space: nowrap;
  }
  textarea {
    flex: 1;
    min-width: 0;
    border: 0;
    resize: none;
    outline: none;
    background: transparent; /* se ve el negro de .body y, si hay, la capa .back */
    position: relative;
    z-index: 1;
    padding: 8px 12px;
    line-height: 1.5;
    tab-size: 4;
    white-space: pre;
  }
  .error {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 12px;
    color: var(--text-2);
  }
</style>
