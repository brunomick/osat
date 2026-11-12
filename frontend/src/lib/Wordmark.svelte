<script lang="ts">
  // El logo en una línea: OSAT con las letras de la marca, siempre con los
  // colores de osat (--brand-*, app.css) sea cual sea el tema.
  export let height = 12
</script>

<svg width={(height * 476) / 104} {height} viewBox="-2 -2 476 104" role="img" aria-label="osat">
  <g fill="none" stroke="var(--brand-ink)" stroke-width="18" stroke-linecap="square" stroke-linejoin="round">
    <rect x="8" y="8" width="84" height="84" rx="21" />
    <path d="M216,8 H153 A21,21 0 0 0 153,50 H195 A21,21 0 0 1 195,92 H132" />
    <path d="M256,92 V29 A21,21 0 0 1 277,8 H319 A21,21 0 0 1 340,29 V92 M256,54 H340" />
    <path d="M380,8 H464 M422,8 V92" />
  </g>
  <rect x="33.5" y="33.5" width="33" height="33" fill="var(--brand-signal)" />
</svg>
