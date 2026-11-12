<script lang="ts">
  import Icon from '../lib/Icon.svelte'
  import type { MenuItem } from './ContextMenu.svelte'
  import { api, errorText, parseError, type GitStatus } from '../lib/api'
  import { discardSets, gitLists, hostLabel, letterClass, relTime, repoName, type GitItem } from '../lib/git'
  import { locale, t, tr } from '../lib/i18n'
  import { confirmDialog, promptDialog, questionsDialog } from '../lib/dialog'
  import { toast } from '../lib/stores'

  // Panel de control de versiones de la pestaña Repositorio, como el de VS Code: mensaje y commit,
  // archivos preparados y con cambios, y las acciones con el remoto. Las acciones corren con el git
  // del servidor (internal/remote/git.go). RepoView le pasa el estado y lo vuelve a leer con refresh.
  export let sessionId: string | null
  export let status: GitStatus | null
  /** Hay archivos abiertos con cambios sin guardar (no se cambia de rama ni se traen cambios). */
  export let hasDirty = false
  /** Vuelve a leer el estado. reload: archivos (rutas absolutas) que cambiaron en el disco, o 'all'. */
  export let refresh: (reload?: string[] | 'all') => Promise<void>
  /** Abre los cambios de un archivo (o el archivo, con file). */
  export let open: (item: GitItem, file?: boolean) => void
  /** Muestra un menú (lo dibuja RepoView: este panel puede estar oculto). */
  export let showMenu: (m: { x: number; y: number; items: MenuItem[] }) => void

  let message = ''
  let busy = ''
  const cancelled = new Error('cancelled') // el usuario cerró una pregunta: no es un error que haya que mostrar

  $: lists = gitLists(status?.files ?? [])
  $: root = status?.root ?? ''
  $: canSync = !!status?.remote && !!status?.branch

  const name = (p: string) => p.slice(p.lastIndexOf('/') + 1)
  const dirName = (p: string) => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '')
  const abs = (paths: string[]) => paths.map((p) => (root === '/' ? '/' + p : `${root}/${p}`))

  /** Corre una acción con el cartel de "trabajando" y avisa si falla. */
  async function run(label: string, fn: () => Promise<void>): Promise<boolean> {
    if (busy || !sessionId || !status?.isRepo) return false
    busy = label
    try {
      await fn()
      return true
    } catch (e) {
      if (e !== cancelled) toast(errorText(e), 'error', 8000)
      return false
    } finally {
      busy = ''
    }
  }

  const stage = (items: GitItem[]) =>
    run(tr('git.working'), async () => {
      await api.gitStage(sessionId!, root, items.map((i) => i.file.path))
      await refresh()
    })

  const unstage = (items: GitItem[]) =>
    run(tr('git.working'), async () => {
      await api.gitUnstage(sessionId!, root, items.map((i) => i.file.path))
      await refresh()
    })

  async function discard(items: GitItem[]) {
    if (!items.length) return
    const { tracked, untracked } = discardSets(items)
    const msg = items.length === 1 ? tr('git.discardOne', { name: name(items[0].file.path) }) : tr('git.discardMany', { n: items.length })
    const ok = await confirmDialog({
      title: tr('git.discardTitle'),
      message: untracked.length ? `${msg} ${tr('git.discardNew')}` : msg,
      okText: tr('git.discard'),
      danger: true,
    })
    if (!ok) return
    await run(tr('git.working'), async () => {
      await api.gitDiscard(sessionId!, root, tracked, untracked)
      await refresh(abs([...tracked, ...untracked]))
    })
  }

  /** Hace el commit; si git no sabe quién lo hace, pregunta nombre y email y lo intenta de nuevo. */
  async function doCommit(msg: string) {
    try {
      await api.gitCommit(sessionId!, root, msg)
    } catch (e) {
      if (parseError(e)?.code !== 'GIT_IDENTITY') throw e
      const answers = await questionsDialog({
        title: tr('git.identityTitle'),
        message: tr('git.identityMsg'),
        questions: [
          { text: tr('git.name'), echo: true },
          { text: tr('git.email'), echo: true },
        ],
      })
      if (!answers) throw cancelled
      await api.gitSetIdentity(sessionId!, root, answers[0], answers[1])
      await api.gitCommit(sessionId!, root, msg)
    }
  }

  async function commit(andPush = false) {
    if (!status?.isRepo || busy) return
    const msg = message.trim()
    if (!msg) return toast(tr('git.needMessage'), 'error')
    let stageAll = false
    if (!lists.staged.length) {
      if (!lists.changes.length) return toast(tr('git.nothingToCommit'), 'error')
      const ok = await confirmDialog({ title: tr('git.nothingStagedTitle'), message: tr('git.nothingStagedMsg'), okText: tr('git.stageAllCommit') })
      if (!ok) return
      stageAll = true
    }
    await run(tr('git.working'), async () => {
      try {
        if (stageAll) await api.gitStage(sessionId!, root, lists.changes.map((i) => i.file.path))
        await doCommit(msg)
        message = ''
        toast(tr('git.committed'), 'success', 2000)
        if (andPush) {
          await api.gitPush(sessionId!, root)
          toast(tr('git.pushed'), 'success', 2000)
        }
      } finally {
        await refresh()
      }
    })
  }

  async function pull() {
    if (hasDirty) return toast(tr('git.saveFirst'), 'error')
    await run(tr('git.working'), async () => {
      try {
        await api.gitPull(sessionId!, root)
        toast(tr('git.pulled'), 'success', 2000)
      } finally {
        await refresh('all')
      }
    })
  }

  const push = () =>
    run(tr('git.working'), async () => {
      try {
        await api.gitPush(sessionId!, root)
        toast(tr('git.pushed'), 'success', 2000)
      } finally {
        await refresh()
      }
    })

  const fetch = () =>
    run(tr('git.working'), async () => {
      await api.gitFetch(sessionId!, root)
      await refresh()
      toast(tr('git.fetched'), 'success', 1500)
    })

  /** Sincronizar (el botón de la barra de estado): trae los cambios y sube los commits, o publica la rama. */
  export async function sync() {
    if (!status?.isRepo || !canSync) return
    if (!status.upstream) return push()
    if (status.behind > 0 && hasDirty) return toast(tr('git.saveFirst'), 'error')
    await run(tr('git.working'), async () => {
      try {
        if (status!.behind > 0) await api.gitPull(sessionId!, root)
        if (status!.ahead > 0 || status!.behind > 0) await api.gitPush(sessionId!, root)
        else await api.gitFetch(sessionId!, root)
      } finally {
        await refresh('all')
      }
    })
  }

  async function checkout(branch: string, create: boolean) {
    if (hasDirty) return toast(tr('git.saveFirst'), 'error')
    await run(tr('git.working'), async () => {
      try {
        await api.gitCheckout(sessionId!, root, branch, create)
        toast(tr('git.switched', { branch }), 'success', 2000)
      } finally {
        await refresh('all')
      }
    })
  }

  async function newBranch() {
    const from = status?.branch || status?.head || ''
    const b = await promptDialog({ title: tr('git.newBranch').replace('…', ''), message: tr('git.newBranchMsg', { from }), okText: tr('common.create') })
    if (b?.trim()) await checkout(b.trim(), true)
  }

  /** Menú de ramas (también desde la barra de estado). */
  export async function branchMenu(ev: MouseEvent) {
    if (!sessionId || !status?.isRepo) return
    const x = ev.clientX
    const y = ev.clientY
    let branches: string[] = []
    try {
      branches = await api.gitBranches(sessionId, root)
    } catch (e) {
      return toast(errorText(e), 'error')
    }
    showMenu({
      x,
      y,
      items: [
        { label: tr('git.newBranch'), icon: 'plus', action: newBranch },
        ...(branches.length ? [{ separator: true, label: '' }] : []),
        ...branches.map((b) => ({
          label: b,
          icon: b === status?.branch ? 'check' : 'branch',
          disabled: b === status?.branch,
          action: () => checkout(b, false),
        })),
      ],
    })
  }

  function moreMenu(ev: MouseEvent) {
    const r = (ev.currentTarget as HTMLElement).getBoundingClientRect()
    showMenu({
      x: r.left,
      y: r.bottom,
      items: [
        { label: tr('git.commitPush'), icon: 'check', disabled: !canSync, action: () => commit(true) },
        { separator: true, label: '' },
        { label: tr('git.pull'), icon: 'download', disabled: !status?.upstream, action: pull },
        { label: status?.upstream ? tr('git.push') : tr('git.publish'), icon: 'upload', disabled: !canSync, action: push },
        { label: tr('git.fetch'), icon: 'refresh', disabled: !status?.remote, action: fetch },
        { separator: true, label: '' },
        { label: tr('git.switchBranch'), icon: 'branch', action: () => branchMenu(ev) },
        { label: tr('git.newBranch'), icon: 'plus', action: newBranch },
      ],
    })
  }

  function rowMenu(ev: MouseEvent, it: GitItem) {
    ev.preventDefault()
    showMenu({
      x: ev.clientX,
      y: ev.clientY,
      items: [
        { label: tr('git.openChanges'), icon: 'diff', action: () => open(it) },
        { label: tr('git.openFile'), icon: 'file', disabled: it.file.y === 'D' && !it.staged, action: () => open(it, true) },
        { separator: true, label: '' },
        it.staged
          ? { label: tr('git.unstage'), icon: 'minus', action: () => unstage([it]) }
          : { label: tr('git.stage'), icon: 'plus', action: () => stage([it]) },
        ...(it.staged ? [] : [{ label: tr('git.discard'), icon: 'undo', danger: true, action: () => discard([it]) }]),
      ],
    })
  }

  function keydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
      e.preventDefault()
      commit()
    }
  }
</script>

<div class="git">
  <div class="head">
    <span class="title">{$t('git.title')}</span>
    <button class="icon" title={$t('git.refresh')} aria-label={$t('git.refresh')} disabled={!!busy} on:click={() => refresh()}><Icon name="refresh" size={14} /></button>
    <button class="icon" title={$t('git.more')} aria-label={$t('git.more')} disabled={!status?.isRepo || !!busy} on:click={moreMenu}><Icon name="menu" size={14} /></button>
  </div>

  {#if !status}
    <p class="hint">{$t('git.loading')}</p>
  {:else if !status.isRepo}
    <p class="hint">{$t('git.notRepo')}</p>
  {:else}
    <div class="origin mono" title={status.remote || $t('git.localOnly')}>
      {#if status.remote}
        <span class="host">{hostLabel(status.host)}</span>
        <span class="ell">{repoName(status.remote)}</span>
      {:else}
        <span class="ell">{$t('git.localOnly')}</span>
      {/if}
      <span class="branch"><Icon name="branch" size={12} />{status.branch || $t('git.detached')}</span>
    </div>

    <div class="compose">
      <textarea class="mono" rows="2" placeholder={$t('git.message')} aria-label={$t('git.message')} bind:value={message} on:keydown={keydown} disabled={!!busy} />
      <div class="commit">
        <button class="primary" disabled={!!busy} on:click={() => commit()}><Icon name="check" size={14} />{$t('git.commit')}</button>
        <button class="primary more" disabled={!!busy} title={$t('git.more')} aria-label={$t('git.more')} on:click={moreMenu}><Icon name="chevron" size={12} /></button>
      </div>
      {#if busy}<p class="busy">{busy}</p>{/if}
    </div>

    <div class="lists">
      {#if lists.staged.length}
        <div class="sec">
          <span>{$t('git.staged')}</span>
          <span class="acts">
            <button class="icon" title={$t('git.unstageAll')} aria-label={$t('git.unstageAll')} disabled={!!busy} on:click={() => unstage(lists.staged)}><Icon name="minus" size={13} /></button>
          </span>
          <span class="count">{lists.staged.length}</span>
        </div>
        {#each lists.staged as it (it.file.path + ':s')}
          <div class="row" role="button" tabindex="0" title={it.file.path} on:click={() => open(it)} on:keydown={(e) => e.key === 'Enter' && open(it)} on:contextmenu={(ev) => rowMenu(ev, it)}>
            <Icon name="file" size={13} />
            <span class="fname mono" class:gone={it.letter === 'D'}>{name(it.file.path)}</span>
            <span class="fdir">{dirName(it.file.path)}</span>
            <span class="acts">
              <button class="icon" title={$t('git.openFile')} aria-label={$t('git.openFile')} on:click|stopPropagation={() => open(it, true)}><Icon name="file" size={13} /></button>
              <button class="icon" title={$t('git.unstage')} aria-label={$t('git.unstage')} disabled={!!busy} on:click|stopPropagation={() => unstage([it])}><Icon name="minus" size={13} /></button>
            </span>
            <span class="letter {letterClass(it)}">{it.letter}</span>
          </div>
        {/each}
      {/if}

      <div class="sec">
        <span>{$t('git.changes')}</span>
        {#if lists.changes.length}
          <span class="acts">
            <button class="icon" title={$t('git.discardAll')} aria-label={$t('git.discardAll')} disabled={!!busy} on:click={() => discard(lists.changes)}><Icon name="undo" size={13} /></button>
            <button class="icon" title={$t('git.stageAll')} aria-label={$t('git.stageAll')} disabled={!!busy} on:click={() => stage(lists.changes)}><Icon name="plus" size={13} /></button>
          </span>
        {/if}
        <span class="count">{lists.changes.length}</span>
      </div>
      {#each lists.changes as it (it.file.path + ':c')}
        <div class="row" role="button" tabindex="0" title={letterClass(it) === 'conflict' ? $t('git.conflict') : it.file.path} on:click={() => open(it)} on:keydown={(e) => e.key === 'Enter' && open(it)} on:contextmenu={(ev) => rowMenu(ev, it)}>
          <Icon name="file" size={13} />
          <span class="fname mono" class:gone={it.letter === 'D'}>{name(it.file.path)}</span>
          <span class="fdir">{dirName(it.file.path)}</span>
          <span class="acts">
            {#if it.letter !== 'D'}
              <button class="icon" title={$t('git.openFile')} aria-label={$t('git.openFile')} on:click|stopPropagation={() => open(it, true)}><Icon name="file" size={13} /></button>
            {/if}
            <button class="icon" title={$t('git.discard')} aria-label={$t('git.discard')} disabled={!!busy} on:click|stopPropagation={() => discard([it])}><Icon name="undo" size={13} /></button>
            <button class="icon" title={$t('git.stage')} aria-label={$t('git.stage')} disabled={!!busy} on:click|stopPropagation={() => stage([it])}><Icon name="plus" size={13} /></button>
          </span>
          <span class="letter {letterClass(it)}">{it.letter}</span>
        </div>
      {:else}
        <p class="hint">{$t('git.noChanges')}</p>
      {/each}
    </div>

    <div class="foot">
      {#if status.ahead > 0}
        <p class="ahead"><Icon name="up" size={12} />{status.ahead === 1 ? $t('git.aheadOne') : $t('git.ahead', { n: status.ahead })}</p>
      {/if}
      {#if status.behind > 0}
        <p class="behind"><Icon name="download" size={12} />{status.behind === 1 ? $t('git.behindOne') : $t('git.behind', { n: status.behind })}</p>
      {/if}
      <p class="last" title={status.subject}>
        {status.head ? $t('git.lastCommit', { hash: status.head, when: relTime(status.time, $locale) }) : $t('git.noCommits')}
      </p>
    </div>
  {/if}
</div>

<style>
  .git {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }
  .head {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 4px 6px 4px 10px;
    border-bottom: 1px solid var(--border);
  }
  .title {
    flex: 1;
    font-weight: 600;
    text-transform: uppercase;
    font-size: 11px;
    letter-spacing: 0.04em;
    color: var(--text-2);
  }
  .hint {
    margin: 10px 12px;
    color: var(--text-3);
    font-size: 12px;
  }
  .origin {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 8px 10px 4px;
    font-size: 11px;
    color: var(--text-2);
    min-width: 0;
  }
  .host {
    flex: none;
    color: var(--text);
  }
  .ell {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .branch {
    flex: none;
    display: inline-flex;
    align-items: center;
    gap: 3px;
  }
  .compose {
    padding: 4px 8px 8px;
  }
  textarea {
    width: 100%;
    box-sizing: border-box;
    resize: vertical;
    min-height: 42px;
    font-size: 12px;
  }
  .commit {
    display: flex;
    gap: 1px;
    margin-top: 6px;
  }
  .commit button {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
  }
  .commit button:first-child {
    flex: 1;
  }
  .commit .more {
    padding-left: 6px;
    padding-right: 6px;
  }
  .commit .more :global(svg) {
    transform: rotate(90deg);
  }
  .busy {
    margin: 6px 0 0;
    font-size: 11px;
    color: var(--text-3);
  }
  .lists {
    flex: 1;
    min-height: 0;
    overflow: auto;
    padding-bottom: 6px;
  }
  .sec {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 6px 8px 3px 10px;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-2);
  }
  .sec > span:first-child {
    flex: 1;
  }
  .count {
    min-width: 16px;
    text-align: center;
    font-weight: 400;
    color: var(--text-3);
  }
  .row {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 2px 8px 2px 18px;
    color: var(--text-2);
    cursor: pointer;
    white-space: nowrap;
    font-size: 12.5px;
  }
  .row:hover,
  .row:focus-visible {
    background: var(--bg-3);
    color: var(--text);
    outline: none;
  }
  .fname {
    flex: none;
    max-width: 60%;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .fname.gone {
    text-decoration: line-through;
  }
  .fdir {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    font-size: 11px;
    color: var(--text-3);
  }
  .acts {
    display: none;
    gap: 0;
  }
  .row:hover .acts,
  .row:focus-within .acts,
  .sec:hover .acts {
    display: inline-flex;
  }
  .acts .icon {
    padding: 2px;
  }
  .letter {
    flex: none;
    width: 14px;
    text-align: center;
    font-weight: 600;
    font-size: 12px;
  }
  .modified {
    color: var(--warning);
  }
  .added {
    color: var(--success);
  }
  .deleted,
  .conflict {
    color: var(--danger);
  }
  .foot {
    flex: none;
    padding: 8px 10px;
    border-top: 1px solid var(--border);
    font-size: 11px;
    color: var(--text-3);
  }
  .foot p {
    margin: 2px 0;
    display: flex;
    align-items: center;
    gap: 4px;
  }
  .ahead,
  .behind {
    color: var(--warning);
  }
  .last {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    display: block !important;
  }
</style>
