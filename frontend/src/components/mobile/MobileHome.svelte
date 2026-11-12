<script lang="ts">
  // Android: Inicio. Un campo que busca entre los servidores y, con Enter, conecta (o arma uno
  // nuevo con usuario@host:puerto); abajo, favoritos y el árbol de grupos (como la barra lateral).
  // Tocar un servidor abre su terminal y tocar un grupo lo pliega; mantenerlos apretados (o ⋯)
  // muestra sus opciones, y mantener apretada la lista, las de toda la lista (grupos, exportar…).
  import { t, tr } from '../../lib/i18n'
  import Icon from '../../lib/Icon.svelte'
  import Wordmark from '../../lib/Wordmark.svelte'
  import { api, errorText, type Connection } from '../../lib/api'
  import { confirmDialog, promptDialog } from '../../lib/dialog'
  import { baseName, buildRows, favoriteConns, isWithin, parentOf } from '../../lib/groups'
  import { mobileScreen, parseTarget } from '../../lib/mobile'
  import { connections, exportGroup, groupPaths, openAppTab, openConnForm, openForConnection, openPorts, reloadConnections, toast } from '../../lib/stores'
  import ActionSheet, { type SheetItem } from './ActionSheet.svelte'

  let query = ''
  let sheet: { title: string; items: SheetItem[] } | null = null
  // Grupos plegados o abiertos a mano, como en la barra lateral (misma clave): se recuerdan al
  // reabrir osat y un grupo que no figura arranca plegado.
  const COLLAPSED_KEY = 'osat.groupsCollapsed'
  let collapsed: Record<string, boolean> = (() => {
    try {
      return JSON.parse(localStorage.getItem(COLLAPSED_KEY) ?? '{}') as Record<string, boolean>
    } catch {
      return {}
    }
  })()
  $: try {
    localStorage.setItem(COLLAPSED_KEY, JSON.stringify(collapsed))
  } catch {
    // sin almacenamiento: vale hasta cerrar la app
  }

  $: q = query.trim().toLowerCase()
  $: filtered = q
    ? $connections.filter((c) => [c.name, c.host, c.user, c.group].some((v) => v?.toLowerCase().includes(q)))
    : $connections
  $: favs = q ? [] : favoriteConns($connections)
  $: sorted = [...filtered].sort((a, b) => (a.group || '').localeCompare(b.group || '') || a.name.localeCompare(b.name))
  $: rows = q ? [] : buildRows($connections, $groupPaths, collapsed, true)

  function open(c: Connection, kind: 'terminal' | 'sftp' = 'terminal') {
    openForConnection(c, kind)
    mobileScreen.set('tab')
  }

  function go(fn: () => void) {
    fn()
    mobileScreen.set('tab')
  }

  // Enter: el único servidor que coincide, uno guardado con ese destino, o el formulario con esos datos.
  function submit() {
    if (filtered.length === 1) return open(filtered[0])
    const d = parseTarget(query)
    if (!d) return
    const match = $connections.find((c) => c.host === d.host && (!d.user || c.user === d.user) && (!d.port || c.port === d.port))
    if (match) return open(match)
    go(() => openConnForm({ host: d.host, user: d.user || undefined, port: d.port || 22 }))
    query = ''
  }

  async function run(job: () => Promise<void>) {
    try {
      await job()
      await reloadConnections()
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function remove(c: Connection) {
    const ok = await confirmDialog({
      title: tr('sidebar.removeConn'),
      message: tr('sidebar.removeConnAsk', { name: c.name }),
      okText: tr('common.delete'),
      danger: true,
    })
    if (ok) await run(() => api.deleteConnection(c.id))
  }

  // Grupos: lo mismo que en la barra lateral de escritorio (Sidebar.svelte).
  async function addGroup(parent: string) {
    const name = (
      await promptDialog({
        title: parent ? tr('sidebar.newSubgroup') : tr('sidebar.newGroup'),
        message: parent ? tr('sidebar.insideOf', { parent }) : tr('sidebar.groupName'),
        okText: tr('common.create'),
      })
    )?.trim()
    if (!name) return
    await run(() => api.addGroup(parent ? `${parent}/${name}` : name))
    if (parent) collapsed = { ...collapsed, [parent]: false }
  }

  async function renameGroup(path: string) {
    const name = (await promptDialog({ title: tr('sidebar.editName'), message: path, value: baseName(path), okText: tr('common.save') }))?.trim()
    if (!name || name === baseName(path)) return
    const parent = parentOf(path)
    const to = parent ? `${parent}/${name}` : name
    if (name.includes('/')) return toast(tr('sidebar.groupSlash'), 'error')
    if ($groupPaths.some((g) => g.toLowerCase() === to.toLowerCase() && g !== path)) return toast(tr('sidebar.groupExists', { name: to }), 'error')
    await run(() => api.moveGroup(path, to))
    collapsed = Object.fromEntries(Object.entries(collapsed).map(([p, v]) => [isWithin(p, path) ? to + p.slice(path.length) : p, v]))
  }

  // Eliminar un grupo borra también sus subgrupos y servidores (path "" = todo).
  async function removeGroup(path: string) {
    const conns = $connections.filter((c) => (path ? isWithin(c.group, path) : true)).length
    const subs = new Set([...$groupPaths, ...$connections.map((c) => c.group)].filter((g) => g && g !== path && (!path || isWithin(g, path)))).size
    if (!path && !conns && !subs) return toast(tr('sidebar.nothingToDelete'))
    const plural = (base: string, n: number) => tr(`${base}.${n === 1 ? 'one' : 'other'}`, { count: n })
    const what = [conns && plural('sidebar.connCount', conns), subs && plural(path ? 'sidebar.subgroupCount' : 'sidebar.groupCount', subs)]
      .filter(Boolean)
      .join(tr('sidebar.and'))
    const ok = await confirmDialog({
      title: path ? tr('sidebar.removeGroup') : tr('sidebar.removeAll'),
      message:
        (path ? (what ? tr('sidebar.removeGroupAskWith', { path, what }) : tr('sidebar.removeGroupAsk', { path })) : tr('sidebar.removeAllAsk', { what })) +
        (conns ? ' ' + tr('sidebar.removeCredsNote') : '') +
        ' ' +
        tr('sidebar.noUndo'),
      okText: tr('common.delete'),
      danger: true,
    })
    if (ok) await run(() => api.deleteConnectionsIn(path).then(() => {}))
  }

  function groupActions(path: string) {
    sheet = {
      title: path,
      items: [
        { label: tr('mobile.addServerHere'), icon: 'plus', action: () => go(() => openConnForm({ group: path })) },
        { label: tr('sidebar.addSubgroup'), icon: 'folderPlus', action: () => addGroup(path) },
        { label: tr('sidebar.editName'), icon: 'edit', action: () => renameGroup(path) },
        { label: tr('sidebar.exportGroup'), icon: 'upload', action: () => exportGroup.set(path) },
        { label: tr('sidebar.removeGroup'), icon: 'trash', danger: true, action: () => removeGroup(path) },
      ],
    }
  }

  function listActions() {
    sheet = {
      title: tr('sidebar.servers'),
      items: [
        { label: tr('mobile.addServer'), icon: 'plus', action: () => go(() => openConnForm()) },
        { label: tr('sidebar.addGroup'), icon: 'folderPlus', action: () => addGroup('') },
        { label: tr('mobile.importConns'), icon: 'download', action: () => go(() => openAppTab('import')) },
        { label: tr('sidebar.exportConns'), icon: 'upload', action: () => exportGroup.set('') },
        { label: tr('sidebar.removeAll'), icon: 'trash', danger: true, action: () => removeGroup('') },
      ],
    }
  }

  function actions(c: Connection) {
    const rdp = c.protocol === 'rdp'
    const target = `${c.user ? c.user + '@' : ''}${c.host}`
    sheet = {
      title: c.name === target ? c.name : `${c.name} · ${target}`,
      items: [
        ...(rdp
          ? [{ label: tr('mobile.openRdp'), icon: 'monitor', action: () => open(c) }]
          : [
              { label: tr('mobile.openTerminal'), icon: 'terminal', action: () => open(c) },
              { label: tr('mobile.openSftp'), icon: 'folder', action: () => open(c, 'sftp') },
              { label: tr('mobile.ports'), icon: 'ports', action: () => go(() => openPorts(c)) },
            ]),
        { label: c.favorite ? tr('sidebar.unfavorite') : tr('sidebar.favorite'), icon: 'star', action: () => run(() => api.setFavorite(c.id, !c.favorite)) },
        { label: tr('mobile.edit'), icon: 'edit', action: () => go(() => openConnForm(c)) },
        { label: tr('sidebar.removeConn'), icon: 'trash', danger: true, action: () => remove(c) },
      ],
    }
  }
</script>

<div class="home">
  <header>
    <span class="title"><Wordmark height={18} /></span>
    <button class="icon" title={$t('snippets.list')} aria-label={$t('snippets.list')} on:click={() => go(() => openAppTab('snippets'))}>
      <Icon name="code" size={20} />
    </button>
    <button class="icon" title={$t('mobile.importConns')} aria-label={$t('mobile.importConns')} on:click={() => go(() => openAppTab('import'))}>
      <Icon name="download" size={20} />
    </button>
    <button class="icon" title={$t('mobile.addServer')} aria-label={$t('mobile.addServer')} on:click={() => go(() => openConnForm())}>
      <Icon name="plus" size={20} />
    </button>
  </header>

  <form class="quick" on:submit|preventDefault={submit} title={$t('mobile.quickTip')}>
    <Icon name="search" size={16} />
    <input bind:value={query} placeholder={$t('mobile.quickPlaceholder')} autocapitalize="off" autocomplete="off" spellcheck="false" enterkeyhint="go" />
    {#if query}
      <button type="button" class="icon" aria-label={$t('common.close')} on:click={() => (query = '')}><Icon name="x" size={16} /></button>
    {/if}
  </form>

  <div class="list" role="presentation" on:contextmenu|preventDefault={listActions}>
    {#if !$connections.length}
      <div class="empty">
        <p>{$t('mobile.empty')}</p>
        <button class="primary" on:click={() => go(() => openConnForm())}>{$t('mobile.addServer')}</button>
        <button on:click={() => go(() => openAppTab('import'))}>{$t('mobile.importConns')}</button>
      </div>
    {/if}

    {#if favs.length}
      <div class="sec">{$t('sidebar.favorites')}</div>
      {#each favs as c (c.id)}
        <div class="row" role="button" tabindex="0" on:click={() => open(c)} on:keydown={(e) => e.key === 'Enter' && open(c)} on:contextmenu|preventDefault|stopPropagation={() => actions(c)}>
          <Icon name="star" size={16} filled />
          <div class="main"><span class="name">{c.name}</span><small>{c.user ? c.user + '@' : ''}{c.host}</small></div>
          <button class="icon" aria-label={$t('mobile.more')} on:click|stopPropagation={() => actions(c)}>⋯</button>
        </div>
      {/each}
    {/if}

    {#if q}
      {#each sorted as c, i (c.id)}
        {#if i === 0 || (sorted[i - 1].group || '') !== (c.group || '')}
          <div class="sec">{c.group || $t('mobile.noGroup')}</div>
        {/if}
        <div class="row" role="button" tabindex="0" on:click={() => open(c)} on:keydown={(e) => e.key === 'Enter' && open(c)} on:contextmenu|preventDefault|stopPropagation={() => actions(c)}>
            <span class="dot" style="background: {c.color || 'var(--text-3)'}" />
            <div class="main"><span class="name">{c.name}</span><small>{c.user ? c.user + '@' : ''}{c.host}{c.port && c.port !== 22 && c.port !== 3389 ? ':' + c.port : ''}</small></div>
            <Icon name={c.protocol === 'rdp' ? 'monitor' : 'terminal'} size={16} />
            <button class="icon" aria-label={$t('mobile.more')} on:click|stopPropagation={() => actions(c)}>⋯</button>
          </div>
      {/each}
    {:else}
      {#if rows.length}<div class="sec">{$t('sidebar.servers')}</div>{/if}
      {#each rows as r (r.key)}
        {#if r.kind === 'group'}
          <div class="row group" style="padding-left: {16 + r.depth * 16}px" role="button" tabindex="0" on:click={() => (collapsed = { ...collapsed, [r.path]: r.open })} on:keydown={(e) => e.key === 'Enter' && (collapsed = { ...collapsed, [r.path]: r.open })} on:contextmenu|preventDefault|stopPropagation={() => groupActions(r.path)}>
            <span class="chev" class:open={r.open}><Icon name="chevron" size={14} /></span>
            <Icon name="folder" size={16} />
            <div class="main"><span class="name">{r.name}</span></div>
            <small class="count">{r.count}</small>
            <button class="icon" aria-label={$t('mobile.more')} on:click|stopPropagation={() => groupActions(r.path)}>⋯</button>
          </div>
        {:else}
          {@const c = r.c}
          <div class="row" style="padding-left: {16 + r.depth * 16}px" role="button" tabindex="0" on:click={() => open(c)} on:keydown={(e) => e.key === 'Enter' && open(c)} on:contextmenu|preventDefault|stopPropagation={() => actions(c)}>
            <span class="dot" style="background: {c.color || 'var(--text-3)'}" />
            <div class="main"><span class="name">{c.name}</span><small>{c.user ? c.user + '@' : ''}{c.host}{c.port && c.port !== 22 && c.port !== 3389 ? ':' + c.port : ''}</small></div>
            <Icon name={c.protocol === 'rdp' ? 'monitor' : 'terminal'} size={16} />
            <button class="icon" aria-label={$t('mobile.more')} on:click|stopPropagation={() => actions(c)}>⋯</button>
          </div>
        {/if}
      {/each}
    {/if}
  </div>
</div>

{#if sheet}
  <ActionSheet title={sheet.title} items={sheet.items} on:close={() => (sheet = null)} />
{/if}

<style>
  .home {
    height: 100%;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }
  header {
    display: flex;
    align-items: center;
    gap: 4px;
    padding: 12px 8px 8px 16px;
  }
  .title {
    flex: 1;
    display: flex;
  }
  .icon {
    padding: 8px 10px;
    border: 0;
    background: none;
    font-size: 18px;
    line-height: 1;
  }
  .quick {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 12px 8px;
    padding: 0 4px 0 12px;
    border-radius: 10px;
    background: var(--bg-3);
    color: var(--text-3);
  }
  .quick input {
    flex: 1;
    min-width: 0;
    border: 0;
    background: none;
    padding: 12px 0;
    font-size: 15px;
    outline: none;
  }
  .list {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding-bottom: 12px;
  }
  .sec {
    padding: 14px 16px 6px;
    color: var(--text-3);
    font-size: 12px;
  }
  .row {
    display: flex;
    align-items: center;
    gap: 12px;
    min-height: 52px;
    padding: 4px 4px 4px 16px;
    border-bottom: 1px solid var(--border);
    color: var(--text-2);
  }
  .row:active {
    background: var(--bg-2);
  }
  .main {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .name {
    color: var(--text);
    font-size: 15px;
  }
  .main small,
  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .main small {
    color: var(--text-3);
    font-size: 12px;
  }
  .row.group .name {
    color: var(--text-2);
  }
  .count {
    color: var(--text-3);
    font-size: 12px;
  }
  .chev {
    display: flex;
    transition: transform 0.15s;
  }
  .chev.open {
    transform: rotate(90deg);
  }
  .dot {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: 50%;
  }
  .empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 10px;
    padding: 48px 24px;
    color: var(--text-2);
  }
  .empty button {
    min-width: 200px;
    padding: 12px;
  }
</style>
