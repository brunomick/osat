<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import ContextMenu, { type MenuItem } from './ContextMenu.svelte'
  import EditorView from './EditorView.svelte'
  import { api, clipboardSet, errorText, type FileEntry } from '../lib/api'
  import { t, tr } from '../lib/i18n'
  import { connectTab, CancelledError } from '../lib/connect'
  import { confirmDialog, promptDialog, renameDialog } from '../lib/dialog'
  import { handoffs, openBrowser, patchTab, toast, type Tab } from '../lib/stores'
  import { askRoot, isPermissionError } from '../lib/sudo'

  // Una carpeta del servidor abierta como un proyecto de VS Code: a la izquierda
  // el árbol de archivos y carpetas, a la derecha los archivos abiertos (cada uno
  // con su editor, que conserva lo que no se guardó al cambiar de uno a otro).
  export let tab: Tab
  /** Visible y en el panel enfocado: el editor activo recibe el teclado. */
  export let focused = false

  const root = (tab.path ?? '/').replace(/\/+$/, '') || '/'
  const rootName = root === '/' ? '/' : root.split('/').pop()!

  let sessionId: string | null = null
  let loading = true
  let error = ''
  let children: Record<string, FileEntry[]> = {} // carpeta → su contenido (solo las ya abiertas)
  let expanded = new Set<string>([root])
  let selectedDir = root // dónde se crea lo nuevo: la última carpeta tocada (o la del último archivo)
  let menu: { x: number; y: number; items: MenuItem[] } | null = null
  let treeWidth = 260

  interface Row {
    e: FileEntry
    depth: number
  }
  function flatten(ch: Record<string, FileEntry[]>, open: Set<string>, dir: string, depth: number, out: Row[] = []): Row[] {
    for (const e of ch[dir] ?? []) {
      out.push({ e, depth })
      if (e.isDir && open.has(e.path)) flatten(ch, open, e.path, depth + 1, out)
    }
    return out
  }
  $: rows = flatten(children, expanded, root, 0)

  const join = (dir: string, name: string) => (dir === '/' ? '/' + name : `${dir}/${name}`)
  const dirOf = (p: string) => p.slice(0, p.lastIndexOf('/')) || '/'
  const within = (p: string, dir: string) => p === dir || p.startsWith(dir === '/' ? '/' : dir + '/')

  async function connect() {
    error = ''
    loading = true
    patchTab(tab.id, { status: 'connecting', sessionId: undefined })
    try {
      const info = await connectTab(tab)
      sessionId = info.id
      patchTab(tab.id, { status: 'ready', sessionId: info.id })
      children = {}
      await load(root, true)
      loading = false
      for (const d of expanded) if (d !== root) load(d) // al reconectar, las carpetas que estaban abiertas
    } catch (e) {
      loading = false
      patchTab(tab.id, { status: 'error' })
      error = e instanceof CancelledError ? tr('term.cancelled') : errorText(e)
    }
  }

  async function load(dir: string, strict = false) {
    if (!sessionId) return
    try {
      const l = await api.sftpList(sessionId, dir)
      children = { ...children, [dir]: l.entries ?? [] }
    } catch (e) {
      if (strict) throw e
      toast(errorText(e), 'error')
    }
  }

  const refresh = () => Promise.all([...expanded].map((d) => load(d)))

  function collapseAll() {
    expanded = new Set([root])
    selectedDir = root
  }

  async function toggle(e: FileEntry) {
    selectedDir = e.path
    if (expanded.has(e.path)) {
      expanded.delete(e.path)
      expanded = expanded
      return
    }
    expanded.add(e.path)
    expanded = expanded
    if (!children[e.path]) await load(e.path)
  }

  // ---------- Archivos abiertos ----------

  interface Open {
    tab: Tab // pseudo-pestaña (no está en el store de pestañas)
    dirty?: boolean
  }
  let opened: Open[] = []
  let current: string | null = null
  let seq = 0
  $: currentPath = opened.find((o) => o.tab.id === current)?.tab.path

  // La pestaña del repositorio muestra ● (y pide confirmar al cerrarla) si algún archivo tiene cambios.
  $: anyDirty = opened.some((o) => o.dirty)
  $: if (anyDirty !== !!tab.dirty) patchTab(tab.id, { dirty: anyDirty })

  // sudo: como root; el editor lo lee con sudo (y pide la contraseña si hace falta).
  async function openFile(e: FileEntry, sudo = false) {
    selectedDir = dirOf(e.path)
    const same = (o: Open) => o.tab.path === e.path && !!o.tab.sudo === sudo
    const found = opened.find(same)
    if (found) {
      current = found.tab.id
      return
    }
    if (!sessionId) return
    let text: string | undefined
    if (!sudo) {
      try {
        text = await api.sftpReadText(sessionId, e.path) // falla si es binario o muy grande
      } catch (err) {
        if (isPermissionError(err)) {
          if (await askRoot('read')) openFile(e, true)
        } else toast(errorText(err), 'error')
        return
      }
    }
    if (opened.some(same)) return // se abrió mientras se leía
    const t: Tab = {
      id: `${tab.id}:f${++seq}`,
      kind: 'editor',
      connId: tab.connId,
      title: e.name,
      color: tab.color,
      fromSession: sessionId,
      path: e.path,
      sudo: sudo || undefined,
      status: 'connecting',
    }
    if (text !== undefined) handoffs.set(t.id, { text, saved: text })
    opened = [...opened, { tab: t }]
    current = t.id
  }

  function removeOpen(id: string) {
    const i = opened.findIndex((o) => o.tab.id === id)
    opened = opened.filter((o) => o.tab.id !== id)
    if (current === id) current = (opened[i] ?? opened[i - 1])?.tab.id ?? null
  }

  async function closeFile(id: string) {
    const o = opened.find((x) => x.tab.id === id)
    if (!o) return
    if (o.dirty) {
      const ok = await confirmDialog({
        title: tr('sftp.unsavedTitle'),
        message: tr('sftp.unsavedMsg', { path: o.tab.path ?? '' }),
        okText: tr('sftp.discardClose'),
        danger: true,
      })
      if (!ok) return
    }
    removeOpen(id)
  }

  // Un archivo borrado o movido deja de estar abierto (si no tiene cambios sin guardar).
  function dropOpenUnder(path: string) {
    for (const o of opened) if (o.tab.path && within(o.tab.path, path) && !o.dirty) removeOpen(o.tab.id)
  }

  // ---------- Crear, renombrar, borrar ----------

  async function create(kind: 'file' | 'dir', dir = selectedDir) {
    const name = await promptDialog({
      title: kind === 'file' ? tr('sftp.newFile') : tr('local.newFolder'),
      message: tr('local.insideOf', { dir }),
      okText: tr('common.create'),
    })
    if (!name || !sessionId) return
    const p = join(dir, name)
    try {
      await (kind === 'file' ? api.sftpCreateFile(sessionId, p) : api.sftpMkdir(sessionId, p))
      expanded.add(dir)
      expanded = expanded
      await load(dir)
      const made = children[dir]?.find((x) => x.path === p)
      if (made && kind === 'file') openFile(made)
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function rename(e: FileEntry) {
    const name = await renameDialog(e)
    if (!name || !sessionId) return
    try {
      await api.sftpRename(sessionId, e.path, join(dirOf(e.path), name))
      dropOpenUnder(e.path)
      expanded.delete(e.path)
      expanded = expanded
      await load(dirOf(e.path))
    } catch (err) {
      toast(errorText(err), 'error')
    }
  }

  async function remove(e: FileEntry) {
    if (!sessionId) return
    const ok = await confirmDialog({
      title: tr('sftp.deleteTitle'),
      message: tr(e.isDir ? 'sftp.deleteOneDir' : 'sftp.deleteOne', { name: e.name }),
      okText: tr('common.delete'),
      danger: true,
    })
    if (!ok) return
    try {
      await api.sftpRemove(sessionId, [e.path])
      dropOpenUnder(e.path)
    } catch (err) {
      toast(errorText(err), 'error')
    }
    await load(dirOf(e.path))
  }

  function copyPath(p: string) {
    clipboardSet(p)
    toast(tr('repo.pathCopied'), 'success', 1500)
  }

  // El archivo (o la carpeta) se ve en el navegador integrado; la raíz del sitio es el repositorio,
  // así los enlaces como /css/app.css o ../img/logo.png funcionan.
  const showInBrowser = (p?: string) => openBrowser(tab, sessionId ?? undefined, { file: p, root })

  function rowMenu(ev: MouseEvent, e: FileEntry) {
    ev.preventDefault()
    ev.stopPropagation()
    const dir = e.isDir ? e.path : dirOf(e.path)
    menu = {
      x: ev.clientX,
      y: ev.clientY,
      items: [
        e.isDir
          ? { label: expanded.has(e.path) ? tr('repo.closeFolder') : tr('repo.openFolder'), icon: 'folder', action: () => toggle(e) }
          : { label: tr('common.open'), icon: 'file', action: () => openFile(e) },
        {
          label: tr('sftp.openBrowser'),
          icon: 'globe',
          action: () => (e.isDir ? openBrowser(tab, sessionId ?? undefined, { root: e.path }) : showInBrowser(e.path)),
        },
        { separator: true, label: '' },
        { label: tr('repo.newFileMenu'), icon: 'file', action: () => create('file', dir) },
        { label: tr('repo.newFolderMenu'), icon: 'folderPlus', action: () => create('dir', dir) },
        { separator: true, label: '' },
        { label: tr('local.copyPath'), icon: 'copy', action: () => copyPath(e.path) },
        { label: tr('local.rename'), icon: 'edit', action: () => rename(e) },
        { label: tr('local.deleteMenu'), icon: 'trash', danger: true, action: () => remove(e) },
      ],
    }
  }

  function bgMenu(ev: MouseEvent) {
    ev.preventDefault()
    menu = {
      x: ev.clientX,
      y: ev.clientY,
      items: [
        { label: tr('repo.newFileMenu'), icon: 'file', action: () => create('file', root) },
        { label: tr('repo.newFolderMenu'), icon: 'folderPlus', action: () => create('dir', root) },
        { separator: true, label: '' },
        { label: tr('local.refresh'), icon: 'refresh', action: refresh },
      ],
    }
  }

  // Flechas izquierda/derecha: mueven el divisor de a 16 px.
  function resizeKey(e: KeyboardEvent) {
    const d = e.key === 'ArrowLeft' ? -16 : e.key === 'ArrowRight' ? 16 : 0
    if (!d) return
    e.preventDefault()
    treeWidth = Math.min(600, Math.max(160, treeWidth + d))
  }

  function startResize(ev: MouseEvent) {
    ev.preventDefault()
    const startX = ev.clientX
    const startW = treeWidth
    const move = (e: MouseEvent) => (treeWidth = Math.min(600, Math.max(160, startW + e.clientX - startX)))
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
  }

  onMount(connect)
</script>

<div class="repo">
  {#if error}
    <div class="error">
      <p>{error}</p>
      <button class="primary" on:click={connect}>{$t('sftp.reconnect')}</button>
    </div>
  {:else}
    <div class="explorer" style="width: {treeWidth}px">
      <div class="head">
        <span class="name" title={root}>{rootName}</span>
        <button class="icon" title={$t('sftp.newFile')} on:click={() => create('file')} aria-label={$t('sftp.newFile')}><Icon name="file" size={14} /></button>
        <button class="icon" title={$t('local.newFolder')} on:click={() => create('dir')} aria-label={$t('local.newFolder')}><Icon name="folderPlus" size={14} /></button>
        <button class="icon" title={$t('local.refresh')} on:click={refresh} aria-label={$t('local.refresh')}><Icon name="refresh" size={14} /></button>
        <button class="icon" title={$t('repo.collapseAll')} on:click={collapseAll} aria-label={$t('repo.collapseAll')}><Icon name="up" size={14} /></button>
        <button class="icon" title={$t('sftp.openBrowser')} on:click={() => showInBrowser()} aria-label={$t('sftp.openBrowser')}><Icon name="globe" size={14} /></button>
      </div>
      <div class="tree" role="presentation" on:contextmenu={bgMenu}>
        {#if loading}
          <p class="hint">{$t('snippets.loading')}</p>
        {:else if !rows.length}
          <p class="hint">{$t('repo.emptyFolder')}</p>
        {/if}
        {#each rows as { e, depth } (e.path)}
          <button
            class="row"
            class:active={!e.isDir && e.path === currentPath}
            style="padding-left: {8 + depth * 14}px"
            title={e.path}
            on:click={() => (e.isDir ? toggle(e) : openFile(e))}
            on:contextmenu={(ev) => rowMenu(ev, e)}
          >
            {#if e.isDir}
              <span class="chev" class:open={expanded.has(e.path)}><Icon name="chevron" size={12} /></span>
              <Icon name="folder" size={14} />
            {:else}
              <span class="chev" />
              <Icon name="file" size={14} />
            {/if}
            <span class="label mono">{e.name}</span>
          </button>
        {/each}
      </div>
    </div>
    <div
      class="split"
      role="slider"
      aria-orientation="vertical"
      aria-label={$t('repo.treeWidth')}
      aria-valuenow={treeWidth}
      aria-valuemin={160}
      aria-valuemax={600}
      tabindex="0"
      title={$t('sftp.dragResize')}
      on:mousedown={startResize}
      on:keydown={resizeKey}
    />

    <div class="main">
      {#if opened.length}
        <div class="strip">
          {#each opened as o (o.tab.id)}
            <button
              class="otab"
              class:on={o.tab.id === current}
              title={o.tab.path}
              on:click={() => (current = o.tab.id)}
              on:auxclick={(e) => e.button === 1 && closeFile(o.tab.id)}
            >
              <span class="mono">{o.tab.title}{o.dirty ? ' ●' : ''}</span>
              <span class="x" role="button" tabindex="-1" title={$t('common.close')} on:click|stopPropagation={() => closeFile(o.tab.id)} on:keydown={() => {}}>
                <Icon name="x" size={11} />
              </span>
            </button>
          {/each}
        </div>
      {/if}
      {#each opened as o, i (o.tab.id)}
        <div class="editor" class:hidden={o.tab.id !== current}>
          <EditorView bind:dirty={opened[i].dirty} tab={o.tab} focused={focused && o.tab.id === current} embedded autofocus />
        </div>
      {/each}
      {#if !opened.length}
        <div class="empty">
          <Icon name="repo" size={34} />
          <p class="mono">{root}</p>
          <p>{$t('repo.pickFile')}</p>
        </div>
      {/if}
    </div>
  {/if}
</div>

{#if menu}
  <ContextMenu {...menu} on:close={() => (menu = null)} />
{/if}

<style>
  .repo {
    flex: 1;
    display: flex;
    min-height: 0;
    min-width: 0;
  }
  .explorer {
    flex: none;
    display: flex;
    flex-direction: column;
    min-height: 0;
    background: var(--bg-2);
  }
  .head {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 4px 6px 4px 10px;
    border-bottom: 1px solid var(--border);
  }
  .name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-weight: 600;
    text-transform: uppercase;
    font-size: 11px;
    letter-spacing: 0.04em;
    color: var(--text-2);
  }
  .tree {
    flex: 1;
    min-height: 0;
    overflow: auto;
    padding: 4px 0;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 4px;
    width: 100%;
    padding: 2px 8px;
    border: 0;
    background: transparent;
    color: var(--text-2);
    text-align: left;
    white-space: nowrap;
  }
  .row:hover {
    background: var(--bg-3);
    color: var(--text);
  }
  .row.active {
    background: var(--bg-4);
    color: var(--text);
  }
  .label {
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .chev {
    width: 12px;
    height: 12px;
    flex: none;
    display: inline-flex;
    transition: transform 0.1s;
  }
  .chev.open {
    transform: rotate(90deg);
  }
  .hint {
    margin: 8px 12px;
    color: var(--text-3);
    font-size: 12px;
  }
  .split {
    flex: none;
    width: 4px;
    cursor: col-resize;
    background: var(--border);
    opacity: 0.4;
  }
  .split:hover {
    opacity: 1;
    background: var(--accent);
  }
  .main {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  .strip {
    flex: none;
    display: flex;
    overflow-x: auto;
    border-bottom: 1px solid var(--border);
    background: var(--bg-2);
  }
  .otab {
    display: inline-flex;
    align-items: center;
    gap: 8px;
    padding: 5px 8px 5px 12px;
    border: 0;
    border-right: 1px solid var(--border);
    background: transparent;
    color: var(--text-2);
    white-space: nowrap;
  }
  .otab.on {
    background: var(--bg);
    color: var(--text);
  }
  .x {
    display: inline-flex;
    padding: 2px;
    opacity: 0.6;
  }
  .x:hover {
    opacity: 1;
    background: var(--bg-4);
  }
  .editor {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
  }
  .editor.hidden {
    display: none;
  }
  .empty {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 8px;
    color: var(--text-3);
  }
  .empty p {
    margin: 0;
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
