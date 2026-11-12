<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import TabBar from './TabBar.svelte'
  import { paneRects, type PaneNode } from '../lib/layout'

  // Un panel del mosaico: su barra de pestañas y un cuerpo vacío. Las vistas
  // de las pestañas no se dibujan acá (se perderían al mover la pestaña a otro
  // panel): viven en una capa aparte y se posicionan sobre este cuerpo, cuyo
  // rectángulo se publica en paneRects.
  export let pane: PaneNode
  export let focused: boolean

  let body: HTMLDivElement
  let ro: ResizeObserver
  let poll: ReturnType<typeof setInterval>
  let last = ''

  function measure() {
    const host = body?.closest('.views')
    if (!host) return
    const c = host.getBoundingClientRect()
    const r = body.getBoundingClientRect()
    const rect = { x: r.left - c.left, y: r.top - c.top, w: r.width, h: r.height }
    const key = `${rect.x},${rect.y},${rect.w},${rect.h}`
    if (key === last) return
    last = key
    paneRects.update((m) => ({ ...m, [pane.id]: rect }))
  }

  onMount(() => {
    ro = new ResizeObserver(measure)
    ro.observe(body)
    window.addEventListener('resize', measure)
    // Un panel puede moverse sin cambiar de tamaño (p. ej. al redimensionar la
    // barra lateral); measure no publica si no cambió.
    poll = setInterval(measure, 250)
    measure()
  })

  onDestroy(() => {
    ro?.disconnect()
    clearInterval(poll)
    window.removeEventListener('resize', measure)
    paneRects.update((m) => {
      const { [pane.id]: _, ...rest } = m
      return rest
    })
  })
</script>

<div class="pane" class:focused>
  <TabBar {pane} {focused} />
  <div class="body" bind:this={body} />
</div>

<style>
  .pane {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
  }
  .body {
    flex: 1;
    min-height: 0;
    position: relative;
  }
</style>
