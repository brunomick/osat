<script lang="ts">
  // Vista previa de la terminal en Ajustes: unas líneas de ejemplo (ls, git, un log) pintadas con la paleta,
  // la fuente y el cursor elegidos, y los 16 colores ANSI de la paleta abajo. No es una terminal de verdad.
  import type { TermTheme } from '../lib/termthemes'

  export let palette: TermTheme
  export let font: string
  export let fontSize: number
  export let cursor: 'bar' | 'block' | 'underline'
  export let blink: boolean
  export let lineHeight = 1.2
  export let letterSpacing = 0
  export let weight = 400
  /** Margen como el de la terminal (ver TerminalView): a la izquierda un poco más, abajo un poco menos. */
  export let padding = 10

  type Color = keyof TermTheme
  const prompt: [string, Color][] = [
    ['bruno@web-01', 'green'],
    [':', 'foreground'],
    ['~', 'blue'],
    ['$ ', 'foreground'],
  ]
  // Cada línea es una lista de [texto, color de la paleta].
  const lines: [string, Color][][] = [
    [...prompt, ['ls -l', 'foreground']],
    [['drwxr-xr-x  ', 'foreground'], ['app/', 'blue']],
    [['-rwxr-xr-x  ', 'foreground'], ['deploy.sh', 'green']],
    [['lrwxrwxrwx  ', 'foreground'], ['current', 'cyan'], [' -> releases/42', 'foreground']],
    [['-rw-r--r--  ', 'foreground'], ['backup.tar.gz', 'red']],
    [...prompt, ['git status -s', 'foreground']],
    [[' M ', 'red'], ['src/app.ts', 'foreground']],
    [['?? ', 'red'], ['docs/notes.md', 'foreground']],
    [...prompt, ['tail -n 3 app.log', 'foreground']],
    [['12:04:01 ', 'brightBlack'], ['info  ', 'blue'], ['listening on :8080', 'foreground']],
    [['12:04:07 ', 'brightBlack'], ['warn  ', 'yellow'], ['slow query (812 ms)', 'foreground']],
    [['12:04:09 ', 'brightBlack'], ['error ', 'red'], ['connection refused', 'foreground']],
  ]
  const ansi: Color[] = ['black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white']
  const bright: Color[] = ['brightBlack', 'brightRed', 'brightGreen', 'brightYellow', 'brightBlue', 'brightMagenta', 'brightCyan', 'brightWhite']
</script>

<div class="term" style:background={palette.background} style:color={palette.foreground} style:font-family={`${font}, var(--mono)`} style:font-size={`${fontSize}px`}
  style:line-height={lineHeight}
  style:letter-spacing={`${letterSpacing}px`}
  style:font-weight={weight}
  style:padding={`${padding}px ${padding + 4}px ${Math.round(padding * 0.6)}px`}
>
  {#each lines as line}
    <div class="line">{#each line as [text, color]}<span style:color={palette[color]}>{text}</span>{/each}</div>
  {/each}
  <div class="line">
    {#each prompt as [text, color]}<span style:color={palette[color]}>{text}</span>{/each}<span
      class="cursor {cursor}"
      class:blink
      style:--cur={palette.cursor}
      style:--cur-text={palette.cursorAccent}>&nbsp;</span
    >
  </div>
  <div class="chips">
    {#each [ansi, bright] as row}
      <div class="row">
        {#each row as c}<i style:background={palette[c]} title={c} />{/each}
      </div>
    {/each}
  </div>
</div>

<style>
  .term {
    border: 1px solid var(--border);
    overflow: hidden;
    white-space: pre;
  }
  .line {
    min-height: 1lh;
  }
  .cursor.block {
    background: var(--cur);
    color: var(--cur-text);
  }
  .cursor.bar {
    box-shadow: inset 2px 0 0 var(--cur);
  }
  .cursor.underline {
    box-shadow: inset 0 -2px 0 var(--cur);
  }
  .cursor.blink {
    animation: blink 1s step-end infinite;
  }
  @keyframes blink {
    50% {
      background: transparent;
      box-shadow: none;
    }
  }
  .chips {
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-top: 12px;
  }
  .row {
    display: flex;
    gap: 4px;
  }
  .row i {
    flex: 1;
    height: 14px;
  }
</style>
