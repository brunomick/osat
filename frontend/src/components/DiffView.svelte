<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { EditorState, type Extension } from '@codemirror/state'
  import { EditorView, lineNumbers } from '@codemirror/view'
  import { MergeView, unifiedMergeView } from '@codemirror/merge'
  import { api, errorText, type GitFile } from '../lib/api'
  import { joinPath } from '../lib/git'
  import { t } from '../lib/i18n'

  // Los cambios de un archivo del repositorio, con CodeMirror (@codemirror/merge), de solo lectura:
  // lado a lado o unificado. Como en VS Code, un archivo de "Cambios" compara lo preparado con la copia
  // de trabajo, y uno de "Preparados" compara el último commit con lo preparado.
  export let sessionId: string
  export let root: string // raíz del repositorio
  export let file: GitFile
  export let staged: boolean
  export let mode: 'split' | 'unified' = 'split'
  /** Cambia cuando el estado del repositorio se actualizó: se vuelve a leer el archivo. */
  export let stamp = 0

  let host: HTMLDivElement
  let error = ''
  let loading = true
  let before = ''
  let after = ''
  let view: MergeView | EditorView | null = null
  let seq = 0

  const theme = EditorView.theme({
    '&': { height: '100%', fontSize: '12.5px', backgroundColor: 'var(--bg)', color: 'var(--text)' },
    '.cm-scroller': { fontFamily: 'var(--mono)', lineHeight: '1.5' },
    '.cm-gutters': { backgroundColor: 'var(--bg)', color: 'var(--text-3)', border: 'none' },
    '.cm-content': { caretColor: 'transparent' },
    '&.cm-focused': { outline: 'none' },
    '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': { background: 'var(--sel-bg) !important' },
    // Colores del diff (los mismos en todos los temas: son translúcidos).
    '&.cm-merge-a .cm-changedLine, .cm-deletedChunk': { backgroundColor: 'rgba(212, 106, 106, 0.16)' },
    '&.cm-merge-b .cm-changedLine, .cm-inlineChangedLine': { backgroundColor: 'rgba(61, 220, 111, 0.13)' },
    '&.cm-merge-a .cm-changedText, .cm-deletedChunk .cm-deletedText': { background: 'rgba(212, 106, 106, 0.4)' },
    '&.cm-merge-b .cm-changedText': { background: 'rgba(61, 220, 111, 0.35)' },
    '.cm-changeGutter': { width: '3px', paddingLeft: '0' },
    '&.cm-merge-a .cm-changedLineGutter, .cm-deletedLineGutter': { background: 'var(--danger)' },
    '&.cm-merge-b .cm-changedLineGutter': { background: 'var(--success)' },
  })

  const base = (): Extension[] => [
    lineNumbers(),
    EditorState.readOnly.of(true),
    EditorView.editable.of(false),
    EditorView.lineWrapping,
    theme,
  ]

  async function load() {
    const my = ++seq
    error = ''
    try {
      // Antes: el último commit (preparados) o lo preparado (cambios). Un renombrado preparado viene de su nombre anterior.
      const beforePath = staged && file.origPath ? file.origPath : file.path
      const untracked = file.x === '?'
      const b = untracked ? { text: '', exists: false } : await api.gitFileAt(sessionId, root, staged ? 'HEAD' : '', beforePath)
      // Después: lo preparado (preparados) o el archivo en el disco del servidor (cambios); un borrado no tiene.
      let a = ''
      if (staged) {
        if (file.x !== 'D') a = (await api.gitFileAt(sessionId, root, '', file.path)).text
      } else if (file.y !== 'D') {
        a = await api.sftpReadText(sessionId, joinPath(root, file.path))
      }
      if (my !== seq) return
      before = b.text
      after = a
      loading = false
      render()
    } catch (e) {
      if (my !== seq) return
      loading = false
      error = errorText(e)
      destroyView()
    }
  }

  function destroyView() {
    view?.destroy()
    view = null
  }

  function render() {
    if (!host) return
    destroyView()
    if (mode === 'split') {
      view = new MergeView({
        a: { doc: before, extensions: base() },
        b: { doc: after, extensions: base() },
        parent: host,
        gutter: true,
        collapseUnchanged: { margin: 4, minSize: 8 },
      })
    } else {
      view = new EditorView({
        parent: host,
        state: EditorState.create({
          doc: after,
          extensions: [...base(), unifiedMergeView({ original: before, mergeControls: false, gutter: true, collapseUnchanged: { margin: 4, minSize: 8 } })],
        }),
      })
    }
  }

  let lastMode = mode
  $: if (mode !== lastMode) {
    lastMode = mode
    if (!loading && !error) render()
  }
  let lastStamp = stamp
  $: if (stamp !== lastStamp) {
    lastStamp = stamp
    load()
  }

  onMount(load)
  onDestroy(() => {
    seq++
    destroyView()
  })
</script>

<div class="diff">
  {#if error}
    <p class="msg err">{error}</p>
  {:else if loading}
    <p class="msg">{$t('snippets.loading')}</p>
  {:else if before === after}
    <p class="msg">{$t('git.noTextChanges')}</p>
  {/if}
  <div class="host" class:hidden={!!error || loading || before === after} bind:this={host} />
</div>

<style>
  .diff {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    background: var(--bg);
  }
  .host {
    flex: 1;
    min-height: 0;
    overflow: auto;
  }
  .host :global(.cm-mergeView) {
    height: 100%;
  }
  .host :global(.cm-mergeViewEditors) {
    min-height: 100%;
  }
  .host :global(.cm-mergeViewEditor + .cm-mergeViewEditor) {
    border-left: 1px solid var(--border);
  }
  .hidden {
    display: none;
  }
  .msg {
    margin: 16px;
    color: var(--text-3);
    font-size: 13px;
  }
  .err {
    color: var(--danger);
  }
</style>
