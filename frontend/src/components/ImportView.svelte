<script lang="ts">
  import { t, tr } from '../lib/i18n'
  // Importar conexiones guardadas en PuTTY, WinSCP, mRemoteNG u OpenSSH: se elige la
  // herramienta, se buscan sus sesiones (en este equipo o en un archivo
  // exportado) y se eligen cuáles dar de alta, con las contraseñas que
  // guardan WinSCP y mRemoteNG y las claves. O de un .osat exportado de otro
  // osat, que puede traer contraseñas y claves.
  import { onMount } from 'svelte'
  import { api, codedText, errorText, parseError, type ImportItem, type ImportResult, type ImportSource } from '../lib/api'
  import { askSecret } from '../lib/dialog'
  import { closeTab, patchTab, reloadConnections, reloadSites, toast, type Tab } from '../lib/stores'

  export let tab: Tab

  const close = () => closeTab(tab.id)
  onMount(() => patchTab(tab.id, { status: 'ready' }))

  // where y file son claves del diccionario.
  const sources: { id: ImportSource; name: string; where: string; file: string }[] = [
    { id: 'putty', name: 'PuTTY', where: 'import.putty.where', file: 'import.putty.file' },
    { id: 'winscp', name: 'WinSCP', where: 'import.winscp.where', file: 'import.winscp.file' },
    { id: 'mremoteng', name: 'mRemoteNG', where: 'import.mremoteng.where', file: 'import.mremoteng.file' },
    { id: 'openssh', name: 'OpenSSH', where: 'import.openssh.where', file: 'import.openssh.file' },
    { id: 'osat', name: 'OSAT', where: 'import.osat.where', file: 'import.osat.file' },
  ]

  let source: (typeof sources)[number] | null = null
  let result: ImportResult | null = null
  let selected = new Set<number>()
  let loading = false
  let importing = false
  let error = '' // al buscar (archivo inválido, cifrado…)
  let renames: Record<string, string> = {} // grupo de origen → nombre que se le quiere dar en osat
  let noGroup = false // importar todas sueltas, sin grupo
  // Credenciales por defecto del .osat: reemplazan las de este osat solo si se elige (marcado si acá no hay).
  let withDefaults = false

  $: items = result?.items ?? []
  $: allOn = items.length > 0 && selected.size === items.length
  // Un .osat puede traer solo snippets o sitios: se importan aunque no se elija ninguna conexión.
  $: extras = source?.id === 'osat' && !!(result?.snippets || result?.sites || (result?.hasDefaults && withDefaults))
  // Grupos de las sesiones marcadas: cada uno se puede renombrar antes de importar.
  $: groups = [...new Set(items.filter((_, i) => selected.has(i)).map((it) => it.group).filter(Boolean))]

  async function scan(s: (typeof sources)[number], path = '', master = '') {
    source = s
    loading = true
    error = ''
    renames = {}
    noGroup = false
    try {
      result = await api.importScan(s.id, path, master)
      // Las que ya están en osat arrancan sin marcar.
      selected = new Set(result.items.flatMap((it, i) => (it.exists ? [] : [i])))
    } catch (e) {
      // mRemoteNG o WinSCP con contraseña maestra: se pide y se vuelve a buscar.
      const ke = parseError(e)
      if (ke?.code === 'NEED_SECRET' && ke.data?.kind === 'master') {
        loading = false
        const pw = await askSecret({ title: tr('import.masterTitle', { name: s.name }), message: codedText(ke), label: tr('import.masterLabel'), allowSave: false, okText: tr('common.open') })
        if (pw) return scan(s, path, pw.secret)
      }
      error = errorText(e)
      result = null
      selected = new Set()
    } finally {
      loading = false
    }
  }

  async function pickFile() {
    if (!source) return
    try {
      const p = await api.pickImportFile(source.id)
      if (p) await (source.id === 'osat' ? openOsat(p) : scan(source, p))
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  // Un .osat no tiene ubicación habitual: se elige el archivo y se pide su contraseña.
  async function startOsat(s: (typeof sources)[number]) {
    source = s
    result = null
    error = ''
    await pickFile()
    if (!result && !error) back() // canceló
  }

  async function openOsat(path: string) {
    const pw = await askSecret({ title: tr('import.fileTitle'), message: path, label: tr('import.password'), allowSave: false, okText: tr('common.open') })
    if (!pw) return
    loading = true
    error = ''
    renames = {}
    noGroup = false
    try {
      result = await api.importOsatScan(path, pw.secret)
      selected = new Set(result.items.flatMap((it, i) => (it.exists ? [] : [i])))
      if (result.hasDefaults) {
        const d = await api.defaultCredsGet().catch(() => null)
        withDefaults = !!d && !d.user && !d.hasSecret
      }
    } catch (e) {
      error = errorText(e)
      result = null
      selected = new Set()
    } finally {
      loading = false
    }
  }

  function toggle(i: number) {
    const s = new Set(selected)
    s.has(i) ? s.delete(i) : s.add(i)
    selected = s
  }

  const toggleAll = () => (selected = allOn ? new Set() : new Set(items.map((_, i) => i)))

  function back() {
    source = null
    result = null
    error = ''
    selected = new Set()
  }

  async function doImport() {
    const list = items
      .filter((_, i) => selected.has(i))
      .map((it) => ({
        ...it,
        group: noGroup ? '' : renames[it.group]?.trim().replace(/^\/+|\/+$/g, '') || it.group,
      }))
    if (!list.length && !extras) return
    importing = true
    try {
      // Primero las credenciales por defecto: las conexiones que las usan pueden no traer usuario.
      if (source?.id === 'osat' && result?.hasDefaults && withDefaults) await api.importOsatDefaults()
      const n = await (source?.id === 'osat' ? api.importOsat(list) : api.importConnections(list))
      await reloadConnections()
      await reloadSites().catch(() => {})
      if (n || !extras) toast(tr(n === 1 ? 'import.imported.one' : 'import.imported.other', { count: n }), 'success')
      else toast(tr('import.extrasDone'), 'success')
      close()
    } catch (e) {
      // Puede haber importado algunas: recargar igual.
      await reloadConnections()
      await reloadSites().catch(() => {})
      toast(errorText(e), 'error', 8000)
    } finally {
      importing = false
    }
  }

  const label = (it: ImportItem) =>
    `${it.user ? it.user + '@' : ''}${it.host}${it.port !== (it.protocol === 'rdp' ? 3389 : 22) ? ':' + it.port : ''}${it.jump ? tr('import.via', { jump: it.jump }) : ''}`
</script>

<div class="page">
  <div class="body" class:wide={source}>
    <h2>{$t('import.title')}</h2>

    {#if !source}
      <p class="muted small">
        {$t('import.intro')}
      </p>
      <div class="sources">
        {#each sources as s}
          <button type="button" class="src" on:click={() => (s.id === 'osat' ? startOsat(s) : scan(s))}>
            <span class="name">{s.name}</span>
            <span class="muted">{$t(s.where)}</span>
            <span class="muted">{$t(s.file)}</span>
          </button>
        {/each}
      </div>
      <footer>
        <span class="spacer" />
        <button type="button" on:click={close}>{$t('common.cancel')}</button>
      </footer>
    {:else}
      <div class="where">
        <span class="muted small">
          {#if loading}{source.id === 'osat' ? $t('import.openingFile') : $t('import.searching', { name: source.name })}
          {:else if result?.notFound}{$t('import.notFound', { name: source.name })}
          {:else if result}<span class="mono">{result.location}</span>{/if}
        </span>
        <button type="button" class="ghost" disabled={loading} on:click={pickFile}>{$t('import.chooseFile')}</button>
      </div>

      {#if error && !loading}
        <p class="err small">{error}</p>
      {:else if result?.notFound && !loading}
        <p class="muted small">
          {$t('import.otherPc')}
          {#if source.id === 'putty'}
            {@const cmd = 'reg export HKCU\\Software\\SimonTatham\\PuTTY\\Sessions putty.reg'}
            {$t('import.puttyHow', { cmd })}
          {:else if source.id === 'winscp'}
            {$t('import.winscpHow')}
          {:else if source.id === 'openssh'}
            {$t('import.opensshHow', { path: '~/.ssh/config' })}
          {:else}
            {$t('import.mremotengHow', { path: '%APPDATA%\\mRemoteNG\\confCons.xml' })}
          {/if}
        </p>
      {:else if result && !loading}
        {#if items.length}
          <div class="list">
            <label class="row head">
              <input type="checkbox" checked={allOn} on:change={toggleAll} />
              <span>{$t('import.selected', { selected: selected.size, total: items.length })}</span>
            </label>
            {#each items as it, i}
              <label class="row" class:dim={it.exists && !selected.has(i)}>
                <input type="checkbox" checked={selected.has(i)} on:change={() => toggle(i)} />
                <span class="name" title={it.name}>{it.name}</span>
                <span class="tag">{it.protocol === 'rdp' ? 'RDP' : 'SSH'}</span>
                <span class="muted mono addr" title={label(it)}>{label(it)}</span>
                <span class="muted grp" title={it.group}>{it.group}</span>
                <span class="exists">{it.exists ? $t('import.exists') : ''}</span>
                <span class="muted creds">{[it.hasSecret && $t('import.credPassword'), it.hasKey && $t('import.credKey')].filter(Boolean).join(' + ')}</span>
              </label>
            {/each}
          </div>
        {:else}
          <p class="muted small">{$t('import.nothing')}</p>
        {/if}
        {#if result.snippets}
          <p class="muted small">{$t(result.snippets === 1 ? 'import.snippets.one' : 'import.snippets.other', { count: result.snippets })}</p>
        {/if}
        {#if result.sites}
          <p class="muted small">{$t(result.sites === 1 ? 'import.sites.one' : 'import.sites.other', { count: result.sites })}</p>
        {/if}
        {#if result.hasDefaults}
          <label class="small">
            <input type="checkbox" bind:checked={withDefaults} />
            {$t('import.withDefaults', { user: result.defaultsUser || '—' })}
          </label>
        {/if}
        {#if groups.length}
          <div class="groups">
            <label class="nogroup small">
              <input type="checkbox" bind:checked={noGroup} />
              <span>{$t('import.noGroup')}</span>
            </label>
            {#if !noGroup}
              <span class="muted small">{$t('import.groupRename')}</span>
              {#each groups as g}
                <label class="grow">
                  <span class="muted mono" title={g}>{g}</span>
                  <span class="muted">→</span>
                  <input type="text" placeholder={g} spellcheck="false" bind:value={renames[g]} />
                </label>
              {/each}
            {/if}
          </div>
        {/if}
        {#if result.skipped.length}
          <details>
            <summary class="muted small">{$t('import.warnings', { count: result.skipped.length })}</summary>
            <ul class="muted small">
              {#each result.skipped as s}<li>{s}</li>{/each}
            </ul>
          </details>
        {/if}
      {/if}

      <footer>
        <button type="button" class="ghost" on:click={back}>{$t('import.back')}</button>
        <span class="spacer" />
        <button type="button" on:click={close}>{$t('common.cancel')}</button>
        <button type="button" class="primary" disabled={(!selected.size && !extras) || importing || loading} on:click={doImport}>
          {importing ? $t('import.importing') : `${$t('import.run')}${selected.size ? ` (${selected.size})` : ''}`}
        </button>
      </footer>
    {/if}
  </div>
</div>

<style>
  .page {
    height: 100%;
    overflow: auto;
    background: var(--bg-2);
  }
  .body {
    width: 640px;
    max-width: 100%;
    margin: 0 auto;
    padding: 24px 20px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  /* La lista de sesiones necesita lugar para nombre, dirección y grupo completos. */
  .body.wide {
    width: 1100px;
  }
  h2 {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
  }
  .small {
    font-size: 12px;
    margin: 0;
  }
  .sources {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 8px;
  }
  .src {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 4px;
    padding: 12px;
    text-align: left;
    font-size: 12px;
    background: var(--bg);
    border: 1px solid var(--border);
    color: var(--text);
    cursor: pointer;
  }
  .src:hover {
    background: var(--bg-3);
    border-color: var(--text-3);
  }
  .src .name {
    font-size: 14px;
    font-weight: 600;
    color: var(--text);
  }
  .where {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .where .small {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .list {
    flex: 1;
    min-height: 0;
    overflow: auto;
    border: 1px solid var(--border);
  }
  .row {
    display: grid;
    grid-template-columns: auto minmax(0, 2fr) auto minmax(0, 1.3fr) minmax(0, 1.7fr) 62px 120px;
    align-items: center;
    gap: 10px;
    padding: 5px 8px;
    font-size: 13px;
    white-space: nowrap;
  }
  .row + .row {
    border-top: 1px solid var(--bg-3);
  }
  .creds {
    font-size: 11px;
  }
  .row.head {
    display: flex;
    gap: 8px;
    position: sticky;
    top: 0;
    background: var(--bg-3);
    color: var(--text-2);
    font-size: 12px;
  }
  .row.dim {
    opacity: 0.55;
  }
  .row .name {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .tag {
    font-size: 10px;
    padding: 0 4px;
    border: 1px solid var(--border);
    color: var(--text-2);
  }
  .addr,
  .grp {
    font-size: 12px;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .err {
    color: var(--danger);
  }
  .exists {
    font-size: 11px;
    color: var(--warning);
  }
  .groups {
    display: flex;
    flex-direction: column;
    gap: 6px;
    max-height: 132px;
    overflow: auto;
  }
  .nogroup {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .grow {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
    align-items: center;
    gap: 10px;
    font-size: 12px;
  }
  .grow span:first-child {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  details ul {
    margin: 6px 0 0;
    padding-left: 18px;
    max-height: 120px;
    overflow: auto;
  }
  footer {
    display: flex;
    gap: 8px;
  }
  .spacer {
    flex: 1;
  }
</style>
