<script lang="ts">
  import { onMount } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import ContextMenu, { type MenuItem } from './ContextMenu.svelte'
  import EditorView from './EditorView.svelte'
  import DiffView from './DiffView.svelte'
  import GitPanel from './GitPanel.svelte'
  import { api, clipboardSet, errorText, type FileEntry, type GitStatus } from '../lib/api'
  import { changeCount, hostLabel, joinPath, treeMarks, type GitItem } from '../lib/git'
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
      refreshGit()
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
    /** Los cambios de un archivo (DiffView) en vez de su editor. */
    diff?: GitItem
  }
  let opened: Open[] = []
  let current: string | null = null
  let seq = 0
  $: currentOpen = opened.find((o) => o.tab.id === current)
  $: currentPath = currentOpen?.diff ? undefined : currentOpen?.tab.path

  // La pestaña del repositorio muestra ● (y pide confirmar al cerrarla) si algún archivo tiene cambios.
  $: anyDirty = opened.some((o) => o.dirty)
  $: if (anyDirty !== !!tab.dirty) patchTab(tab.id, { dirty: anyDirty })

  // ---------- Control de versiones (git del servidor) ----------

  let side: 'files' | 'git' = 'files'
  let git: GitStatus | null = null
  let gitStamp = 0 // cambia cada vez que se relee el estado: los diffs abiertos se actualizan
  let gitPanel: GitPanel
  let diffMode: 'split' | 'unified' = 'split'
  $: marks = treeMarks(git)
  $: changes = changeCount(git)

  let gitSeq = 0
  /** Vuelve a leer el estado del repositorio. reload: archivos (rutas absolutas) que cambiaron en el
   * disco por una acción de git, o 'all': sus editores sin cambios se vuelven a abrir. */
  async function refreshGit(reload?: string[] | 'all') {
    if (!sessionId) return
    const my = ++gitSeq
    try {
      const st = await api.gitStatus(sessionId, root)
      if (my !== gitSeq) return
      git = st
    } catch (e) {
      if (my !== gitSeq) return
      git = { isRepo: false } as GitStatus
      if (side === 'git') toast(errorText(e), 'error', 8000)
    }
    gitStamp++
    if (reload) {
      opened = opened.map((o) =>
        !o.diff && !o.dirty && o.tab.path && (reload === 'all' || reload.includes(o.tab.path)) ? reopen(o) : o,
      )
      if (reload === 'all') refresh()
    }
  }

  // Un editor se vuelve a montar con otro id (y relee el archivo del servidor).
  function reopen(o: Open): Open {
    const id = `${tab.id}:f${++seq}`
    if (current === o.tab.id) current = id
    return { ...o, tab: { ...o.tab, id } }
  }

  // Al guardar un archivo (pasa de tener cambios sin guardar a no tenerlos), cambia el estado de git.
  let wasDirty = new Set<string>()
  $: {
    let saved = false
    const now = new Set<string>()
    for (const o of opened) {
      if (o.dirty) now.add(o.tab.id)
      else if (wasDirty.has(o.tab.id)) saved = true
    }
    wasDirty = now
    if (saved) refreshGit()
  }

  // Al volver a la pestaña (puede haber cambios hechos en una terminal), se relee el estado.
  let wasFocused = focused
  $: {
    if (focused && !wasFocused) refreshGit()
    wasFocused = focused
  }

  /** Abre los cambios de un archivo del panel de git, o el archivo mismo (file). */
  function openGitItem(it: GitItem, file = false) {
    if (!git) return
    const path = joinPath(git.root, it.file.path)
    const fname = path.slice(path.lastIndexOf('/') + 1)
    if (file) {
      openFile({ name: fname, path, isDir: false } as FileEntry)
      return
    }
    const found = opened.find((o) => o.diff && o.diff.file.path === it.file.path && o.diff.staged === it.staged)
    if (found) {
      found.diff = it
      opened = opened
      current = found.tab.id
      return
    }
    const t: Tab = {
      id: `${tab.id}:d${++seq}`,
      kind: 'editor',
      connId: tab.connId,
      title: tr(it.staged ? 'git.stagedOf' : 'git.changesOf', { name: fname }),
      color: tab.color,
      path,
      status: 'ready',
    }
    opened = [...opened, { tab: t, diff: it }]
    current = t.id
  }

  // Un diff abierto sigue a su archivo cuando cambia el estado: si ya no tiene cambios en su lista pasa a la
  // otra (de "cambios" a "preparado" al prepararlo, como en VS Code), y si no tiene cambios en ninguna se cierra.
  $: if (git) syncDiffs(git)
  function syncDiffs(st: GitStatus) {
    let changed = false
    const next: Open[] = []
    for (const o of opened) {
      if (!o.diff) {
        next.push(o)
        continue
      }
      const f = st.files?.find((x) => x.path === o.diff!.file.path)
      const inStaged = !!f && f.x !== '.' && f.x !== '?' && f.x !== 'U'
      const inChanges = !!f && (f.y !== '.' || f.x === '?' || f.x === 'U')
      const staged = o.diff.staged ? (inStaged ? true : inChanges ? false : null) : inChanges ? false : inStaged ? true : null
      if (staged === null || !f) {
        if (current === o.tab.id) current = null
        changed = true
        continue
      }
      if (staged !== o.diff.staged || f.x !== o.diff.file.x || f.y !== o.diff.file.y) {
        const fname = o.tab.path?.slice(o.tab.path.lastIndexOf('/') + 1) ?? f.path
        o.diff = { file: f, staged, letter: o.diff.letter }
        o.tab = { ...o.tab, title: tr(staged ? 'git.stagedOf' : 'git.changesOf', { name: fname }) }
        changed = true
      }
      next.push(o)
    }
    if (changed) {
      opened = next
      if (current === null) current = opened[opened.length - 1]?.tab.id ?? null
    }
  }

  // sudo: como root; el editor lo lee con sudo (y pide la contraseña si hace falta).
  async function openFile(e: FileEntry, sudo = false) {
    selectedDir = dirOf(e.path)
    const same = (o: Open) => !o.diff && o.tab.path === e.path && !!o.tab.sudo === sudo
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
    for (const o of opened) if (o.tab.path && !o.diff && within(o.tab.path, path) && !o.dirty) removeOpen(o.tab.id)
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
      refreshGit()
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
      refreshGit()
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
      refreshGit()
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
    <div class="body">
      <div class="activity" role="tablist" aria-orientation="vertical">
        <button class="act" class:on={side === 'files'} role="tab" aria-selected={side === 'files'} title={$t('git.files')} aria-label={$t('git.files')} on:click={() => (side = 'files')}>
          <Icon name="file" size={18} />
        </button>
        <button
          class="act"
          class:on={side === 'git'}
          role="tab"
          aria-selected={side === 'git'}
          title={$t('git.title')}
          aria-label={$t('git.title')}
          on:click={() => ((side = 'git'), refreshGit())}
        >
          <Icon name="branch" size={18} />
          {#if changes}<span class="badge">{changes > 99 ? '99+' : changes}</span>{/if}
        </button>
      </div>
      <div class="explorer" style="width: {treeWidth}px">
        <div class="pane" class:hidden={side !== 'git'}>
          <GitPanel bind:this={gitPanel} {sessionId} status={git} hasDirty={anyDirty} refresh={refreshGit} open={openGitItem} showMenu={(m) => (menu = m)} />
        </div>
        <div class="pane" class:hidden={side !== 'files'}>
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
                <span class="label mono" class:modified={marks.get(e.path)?.cls === 'modified'} class:added={marks.get(e.path)?.cls === 'added'} class:deleted={marks.get(e.path)?.cls === 'deleted' || marks.get(e.path)?.cls === 'conflict'}>{e.name}</span>
                {#if marks.get(e.path)}
                  <span class="mark {marks.get(e.path)?.cls}">{marks.get(e.path)?.letter || '●'}</span>
                {/if}
              </button>
            {/each}
          </div>
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
            {#if o.diff}
              <div class="difftools">
                <span class="mono path" title={o.tab.path}>{o.diff.file.origPath && o.diff.staged ? `${o.diff.file.origPath} → ` : ''}{o.diff.file.path}</span>
                <button class:on={diffMode === 'split'} on:click={() => (diffMode = 'split')}>{$t('git.split')}</button>
                <button class:on={diffMode === 'unified'} on:click={() => (diffMode = 'unified')}>{$t('git.unified')}</button>
              </div>
              {#if sessionId && git?.isRepo}
                <DiffView {sessionId} root={git.root} file={o.diff.file} staged={o.diff.staged} mode={diffMode} stamp={gitStamp} />
              {/if}
            {:else}
              <EditorView bind:dirty={opened[i].dirty} tab={o.tab} focused={focused && o.tab.id === current} embedded autofocus />
            {/if}
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
    </div>
    {#if git?.isRepo}
      <div class="statusbar">
        <button class="sb" title={$t('git.switchBranch')} on:click={(e) => gitPanel?.branchMenu(e)}>
          <Icon name="branch" size={12} />{git.branch || $t('git.detached')}{changes ? '*' : ''}
        </button>
        {#if git.remote && git.branch}
          <button class="sb" title={git.upstream ? $t('git.sync') : $t('git.publish')} on:click={() => gitPanel?.sync()}>
            {#if git.upstream}
              <Icon name="refresh" size={12} />↑{git.ahead} ↓{git.behind}
            {:else}
              <Icon name="upload" size={12} />{$t('git.publish')}
            {/if}
          </button>
        {/if}
        <span class="sb muted mono" title={git.remote || $t('git.localOnly')}>{git.remote ? hostLabel(git.host) : $t('git.localOnly')}</span>
      </div>
    {/if}
  {/if}
</div>

{#if menu}
  <ContextMenu {...menu} on:close={() => (menu = null)} />
{/if}

<style>
  .repo {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-height: 0;
    min-width: 0;
  }
  .body {
    flex: 1;
    display: flex;
    min-height: 0;
    min-width: 0;
  }
  /* Barra de íconos: árbol de archivos o control de versiones. */
  .activity {
    flex: none;
    width: 36px;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    padding-top: 6px;
    background: var(--rail);
    border-right: 1px solid var(--border);
  }
  .act {
    position: relative;
    width: 36px;
    height: 34px;
    display: flex;
    align-items: center;
    justify-content: center;
    border: 0;
    border-left: 2px solid transparent;
    background: transparent;
    color: var(--text-3);
  }
  .act:hover {
    color: var(--text);
  }
  .act.on {
    color: var(--text);
    border-left-color: var(--signal);
  }
  .badge {
    position: absolute;
    right: 3px;
    bottom: 3px;
    min-width: 14px;
    padding: 0 3px;
    box-sizing: border-box;
    background: var(--signal);
    color: var(--on-accent);
    font-size: 10px;
    line-height: 14px;
    font-weight: 600;
    text-align: center;
  }
  .pane {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }
  .pane.hidden {
    display: none;
  }
  /* Marcas de git en el árbol. */
  .mark {
    margin-left: auto;
    padding-left: 6px;
    font-size: 11px;
    font-weight: 600;
  }
  .label.modified,
  .mark.modified {
    color: var(--warning);
  }
  .label.added,
  .mark.added {
    color: var(--success);
  }
  .label.deleted,
  .mark.deleted,
  .mark.conflict {
    color: var(--danger);
  }
  .difftools {
    flex: none;
    display: flex;
    align-items: center;
    gap: 0;
    padding: 4px 8px;
    border-bottom: 1px solid var(--border);
    background: var(--bg-2);
    font-size: 12px;
  }
  .difftools .path {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text-2);
  }
  .difftools button {
    padding: 2px 8px;
    font-size: 11px;
  }
  .difftools button.on {
    background: var(--accent);
    color: var(--on-accent);
    border-color: var(--accent);
  }
  .statusbar {
    flex: none;
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 0 6px;
    height: 22px;
    background: var(--rail);
    border-top: 1px solid var(--border);
    font-size: 11px;
  }
  .sb {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 0 6px;
    height: 20px;
    border: 0;
    background: transparent;
    color: var(--text-2);
    font-size: 11px;
    white-space: nowrap;
  }
  button.sb:hover {
    background: var(--bg-3);
    color: var(--text);
  }
  .sb.muted {
    color: var(--text-3);
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
