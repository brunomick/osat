<script lang="ts">
  // El ícono de la marca: la O con su núcleo verde, siempre con los colores de
  // osat (--brand-*, app.css) sea cual sea el tema; con tile va sobre su cuadrado.
  export let size = 48
  export let tile = false
</script>

<svg width={size} height={size} viewBox={tile ? '0 0 512 512' : '104 104 304 304'} aria-hidden="true">
  {#if tile}
    <rect width="512" height="512" rx="104" fill="var(--brand-tile)" />
  {/if}
  <rect x="129" y="129" width="254" height="254" rx="62" fill="none" stroke="var(--brand-ink)" stroke-width="50" />
  <rect x="220" y="220" width="72" height="72" fill="var(--brand-signal)" />
</svg>
