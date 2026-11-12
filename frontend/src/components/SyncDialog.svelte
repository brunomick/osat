<script lang="ts">
  import { focusTrap } from '../lib/focustrap'
  import { t, tr } from '../lib/i18n'
  // Sincronizar una carpeta del servidor con una de esta PC (como el "Sincronizar" de WinSCP). "Comparar" arma
  // la vista previa en el backend (ver internal/remote/sync.go), que es obligatoria: "Sincronizar" ejecuta solo
  // lo que quedó marcado, como una transferencia del panel (con avance y cancelación).
  import { createEventDispatcher, onDestroy } from 'svelte'
  import Icon from '../lib/Icon.svelte'
  import { api, errorText, fmtSize, type SyncAction, type SyncPreview, type SyncRequest } from '../lib/api'
  import { toast } from '../lib/stores'
  import { holdOverlay } from '../lib/overlay'

  onDestroy(holdOverlay())

  export let sessionId: string
  /** Carpeta que muestra el explorador: es la carpeta del servidor por defecto. */
  export let cwd: string

  const dispatch = createEventDispatcher<{ close: void }>()

  let remoteDir = cwd
  let localDir = ''
  let direction: SyncRequest['direction'] = 'up'
  let mode: SyncRequest['mode'] = 'sync'
  let compare: SyncRequest['compare'] = 'time+size'
  let del = false
  let exclude = '.git, node_modules'
  let preview: SyncPreview | null = null
  let checked = new Set<string>()
  let busy = false
  let error = ''

  // Espejo y borrar en el destino son solo para una dirección.
  $: if (direction === 'both') {
    mode = 'sync'
    del = false
  }
  // Cambiar cualquier opción invalida la vista previa: hay que volver a comparar.
  $: invalidate(remoteDir, localDir, direction, mode, compare, del, exclude)
  function invalidate(..._: unknown[]) {
    preview = null
    error = ''
  }

  async function pickLocal() {
    try {
      const d = await api.pickFolder(tr('sync.pickFolder'), localDir)
      if (d) localDir = d
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  async function compareNow() {
    busy = true
    error = ''
    try {
      const p = await api.syncCompare(sessionId, {
        remoteDir: remoteDir.trim(),
        localDir: localDir.trim(),
        direction,
        mode,
        compare,
        delete: del,
        exclude: exclude.split(/[,\n]/).map((s) => s.trim()).filter(Boolean),
      })
      p.actions ??= []
      checked = new Set(p.actions.filter((a) => a.op !== 'skip').map((a) => a.rel))
      preview = p
    } catch (e) {
      error = errorText(e)
    } finally {
      busy = false
    }
  }

  async function run() {
    if (!preview || !checked.size) return
    busy = true
    try {
      await api.syncRun(sessionId, preview.planId, [...checked])
      toast(tr('sync.started'), 'info', 3000)
      dispatch('close')
    } catch (e) {
      error = errorText(e)
    } finally {
      busy = false
    }
  }

  function toggle(rel: string) {
    checked.has(rel) ? checked.delete(rel) : checked.add(rel)
    checked = checked
  }

  function toggleAll() {
    checked = allChecked ? new Set() : new Set(actions.filter((a) => a.op !== 'skip').map((a) => a.rel))
  }

  $: label = {
    upload: $t('sync.opUpload'),
    download: $t('sync.opDownload'),
    mkdirRemote: $t('sync.opMkdir'),
    mkdirLocal: $t('sync.opMkdir'),
    deleteRemote: $t('sync.opDeleteRemote'),
    deleteLocal: $t('sync.opDeleteLocal'),
    skip: $t('sync.opSkip'),
  } as Record<SyncAction['op'], string>
  const icon: Record<SyncAction['op'], string> = {
    upload: 'upload',
    download: 'download',
    mkdirRemote: 'folderPlus',
    mkdirLocal: 'folderPlus',
    deleteRemote: 'trash',
    deleteLocal: 'trash',
    skip: 'x',
  }
  const isDelete = (a: SyncAction) => a.op === 'deleteRemote' || a.op === 'deleteLocal'

  $: actions = preview?.actions ?? []
  $: selectable = actions.filter((a) => a.op !== 'skip').length
  $: allChecked = selectable > 0 && checked.size === selectable
  $: summary = summarize(actions, checked, $t)

  function summarize(list: SyncAction[], on: Set<string>, tf: typeof tr) {
    const sel = list.filter((a) => on.has(a.rel))
    const of = (...ops: SyncAction['op'][]) => sel.filter((a) => ops.includes(a.op))
    const bytes = (l: SyncAction[]) => fmtSize(l.reduce((n, a) => n + a.size, 0))
    const up = of('upload')
    const down = of('download')
    const dirs = of('mkdirRemote', 'mkdirLocal')
    const dels = of('deleteRemote', 'deleteLocal')
    const skipped = list.filter((a) => a.op === 'skip').length
    const parts: string[] = []
    if (up.length) parts.push(tf('sync.toUpload', { count: up.length, size: bytes(up) }))
    if (down.length) parts.push(tf('sync.toDownload', { count: down.length, size: bytes(down) }))
    if (dirs.length) parts.push(tf(dirs.length === 1 ? 'sync.foldersToCreate.one' : 'sync.foldersToCreate.other', { count: dirs.length }))
    if (dels.length) parts.push(tf('sync.toDelete', { count: dels.length }))
    if (skipped) parts.push(tf(skipped === 1 ? 'sync.skipped.one' : 'sync.skipped.other', { count: skipped }))
    return parts.join(', ') || tf('sync.nothingChecked')
  }

  const close = () => !busy && dispatch('close')
</script>

<div class="backdrop" role="presentation" on:mousedown|self={close} on:keydown={(e) => e.key === 'Escape' && close()}>
  <form class="modal" on:submit|preventDefault={compareNow} role="dialog" aria-modal="true" use:focusTrap aria-labelledby="sync-title">
    <h2 id="sync-title">{$t('sync.title')}</h2>

    <fieldset disabled={busy}>
      <label>
        {$t('sync.serverDir')}
        <input class="mono" bind:value={remoteDir} spellcheck="false" required />
      </label>
      <div class="field">
        <span class="lbl">{$t('sync.localDir')}</span>
        <div class="pick">
          <input class="mono" bind:value={localDir} spellcheck="false" placeholder={$t('sync.pickPlaceholder')} required />
          <button type="button" on:click={pickLocal}>{$t('settings.choose')}</button>
        </div>
      </div>

      <div class="field">
        <span class="lbl">{$t('sync.direction')}</span>
        <div class="seg">
          <button type="button" class:on={direction === 'up'} on:click={() => (direction = 'up')}>{$t('sync.up')}</button>
          <button type="button" class:on={direction === 'down'} on:click={() => (direction = 'down')}>{$t('sync.down')}</button>
          <button type="button" class:on={direction === 'both'} on:click={() => (direction = 'both')}>{$t('sync.both')}</button>
        </div>
      </div>

      <div class="field">
        <span class="lbl">{$t('sync.mode')}</span>
        <div class="seg">
          <button type="button" class:on={mode === 'sync'} title={$t('sync.modeSyncTip')} on:click={() => (mode = 'sync')}>
            {$t('sync.modeSync')}
          </button>
          <button
            type="button"
            class:on={mode === 'mirror'}
            disabled={direction === 'both'}
            title={direction === 'both' ? $t('sync.oneDirection') : $t('sync.modeMirrorTip')}
            on:click={() => (mode = 'mirror')}
          >
            {$t('sync.modeMirror')}
          </button>
        </div>
      </div>

      <label>
        {$t('sync.compareBy')}
        <select bind:value={compare}>
          <option value="time+size">{$t('sync.timeSize')}</option>
          <option value="size">{$t('sync.sizeOnly')}</option>
          <option value="content">{$t('sync.content')}</option>
        </select>
      </label>

      <label class="check" title={direction === 'both' ? $t('sync.oneDirection') : ''}>
        <input type="checkbox" bind:checked={del} disabled={direction === 'both'} />
        {$t('sync.delete')}
      </label>

      <label>
        {$t('sync.exclude')}
        <textarea class="mono" rows="2" bind:value={exclude} spellcheck="false" />
      </label>
    </fieldset>

    {#if error}<p class="err">{error}</p>{/if}

    {#if preview}
      <div class="preview">
        <div class="sumline">
          {#if actions.length}
            <label class="check all">
              <input type="checkbox" checked={allChecked} disabled={!selectable} on:change={toggleAll} />
              {summary}
            </label>
          {:else}
            <span class="muted">{$t('sync.noDiff')}</span>
          {/if}
        </div>
        {#if actions.length}
          <div class="list">
            {#each actions as a (a.rel)}
              <label class="row" class:del={isDelete(a)} class:skip={a.op === 'skip'} title={a.rel}>
                <input type="checkbox" checked={checked.has(a.rel)} disabled={a.op === 'skip'} on:change={() => toggle(a.rel)} />
                <Icon name={icon[a.op]} size={13} />
                <span class="op">{label[a.op]}</span>
                <span class="rel mono">{a.rel}</span>
                <span class="why">{a.reason}</span>
                <span class="size">{a.op === 'upload' || a.op === 'download' ? fmtSize(a.size) : ''}</span>
              </label>
            {/each}
          </div>
        {/if}
      </div>
    {/if}

    <footer>
      <span class="spacer" />
      <button type="button" disabled={busy} on:click={() => dispatch('close')}>{$t('common.cancel')}</button>
      {#if preview}
        <button type="button" class="primary" disabled={busy || !checked.size} on:click={run}>{$t('sync.modeSync')}</button>
      {:else}
        <button class="primary" type="submit" disabled={busy || !remoteDir.trim() || !localDir.trim()}>
          {busy ? $t('sync.comparing') : $t('sync.compare')}
        </button>
      {/if}
    </footer>
  </form>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    z-index: 400;
  }
  .modal {
    width: 760px;
    max-width: 94vw;
    max-height: 92vh;
    overflow: auto;
    background: var(--bg-2);
    border: 1px solid var(--border);
    padding: 20px;
    display: flex;
    flex-direction: column;
    gap: 14px;
  }
  h2 {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
  }
  fieldset {
    border: 0;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  label,
  .field {
    display: flex;
    flex-direction: column;
    gap: 4px;
    color: var(--text-2);
    font-size: 12px;
  }
  label input,
  label select,
  label textarea {
    color: var(--text);
    font-size: 13px;
  }
  textarea {
    resize: vertical;
  }
  .pick {
    display: flex;
    gap: 6px;
  }
  .pick input {
    flex: 1;
    min-width: 0;
    color: var(--text);
    font-size: 13px;
  }
  .seg {
    display: flex;
    border: 1px solid var(--border);
    overflow: hidden;
  }
  .seg button {
    flex: 1;
    border: 0;
    background: var(--bg);
  }
  .seg button + button {
    border-left: 1px solid var(--border);
  }
  .seg button.on {
    background: var(--accent);
    color: var(--on-accent);
  }
  .check {
    flex-direction: row;
    align-items: center;
    gap: 8px;
    color: var(--text);
    font-size: 13px;
  }
  .preview {
    display: flex;
    flex-direction: column;
    gap: 6px;
  }
  .list {
    max-height: 300px;
    overflow: auto;
    border: 1px solid var(--border);
    background: var(--bg);
  }
  .row {
    display: grid;
    grid-template-columns: 16px 14px 130px minmax(0, 1fr) auto 64px;
    align-items: center;
    gap: 8px;
    padding: 3px 8px;
    color: var(--text);
    font-size: 12px;
  }
  .row:hover {
    background: var(--bg-3);
  }
  .row.del {
    color: var(--danger);
  }
  .row.skip {
    color: var(--text-3);
  }
  .op {
    white-space: nowrap;
  }
  .rel {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .why,
  .size {
    color: var(--text-3);
    white-space: nowrap;
  }
  .row.del .why {
    color: inherit;
  }
  .size {
    text-align: right;
  }
  .muted {
    color: var(--text-3);
    font-size: 12px;
  }
  .err {
    margin: 0;
    color: var(--danger);
    font-size: 12px;
  }
  footer {
    display: flex;
    gap: 8px;
    margin-top: 4px;
  }
  .spacer {
    flex: 1;
  }
</style>
