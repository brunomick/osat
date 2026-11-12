<script lang="ts">
  export let name: string
  export let size = 16
  /** Relleno sólido (p. ej. el pin de una carpeta ya fijada). */
  export let filled = false

  // Íconos estilo "outline" (24x24, stroke), con el trazo de la marca:
  // 2px y puntas cuadradas, como las letras del logo.
  const paths: Record<string, string> = {
    terminal: 'M4 17l6-6-6-6M12 19h8',
    folder: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z',
    file: 'M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9zM14 3v6h6',
    plus: 'M12 5v14M5 12h14',
    x: 'M18 6 6 18M6 6l12 12',
    edit: 'M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z',
    trash: 'M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6',
    up: 'M12 19V5M5 12l7-7 7 7',
    back: 'M19 12H5M12 19l-7-7 7-7',
    forward: 'M5 12h14M12 5l7 7-7 7',
    home: 'M3 11l9-8 9 8M5 10v10h14V10',
    refresh: 'M21 12a9 9 0 1 1-3-6.7L21 8M21 3v5h-5',
    upload: 'M12 16V4M6 10l6-6 6 6M4 20h16',
    download: 'M12 4v12M6 10l6 6 6-6M4 20h16',
    folderPlus: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2zM12 10v6M9 13h6',
    search: 'M11 19a8 8 0 1 0 0-16 8 8 0 0 0 0 16zM21 21l-4.3-4.3',
    chevron: 'M9 6l6 6-6 6',
    server: 'M4 4h16v6H4zM4 14h16v6H4zM8 7h.01M8 17h.01',
    link: 'M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1 1M14 11a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1-1',
    swap: 'M7 16V4M3 8l4-4 4 4M17 8v12M21 16l-4 4-4-4',
    splitRight: 'M3 4h18v16H3zM12 4v16',
    splitDown: 'M3 4h18v16H3zM3 12h18',
    monitor:'M3 4h18v12H3zM8 20h8M12 16v4',
    vnc: 'M3 4h18v12H3zM8 20h8M12 16v4M9 6.5l6 3-2.5 1L11 13z',
    pc: 'M4 5h16v10H4zM2 19h20',
    pin: 'M12 17v5M9 10.8a2 2 0 0 1-1.1 1.8l-1.8.9A2 2 0 0 0 5 15.2V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.8a2 2 0 0 0-1.1-1.8l-1.8-.9A2 2 0 0 1 15 10.8V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z',
    star: 'M12 3l2.8 5.7 6.2.9-4.5 4.4 1 6.2-5.5-2.9-5.5 2.9 1-6.2L3 9.6l6.2-.9z',
    external:'M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5',
    copy: 'M9 9h11v11H9zM5 15H4V4h11v1',
    paste: 'M9 3h6v4H9zM15 5h3a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V6a1 1 0 0 1 1-1h3',
    key: 'M15 7a4 4 0 1 1-3.9 5H8v3H5v-3H3v-2h8.1A4 4 0 0 1 15 7z',
    lock: 'M6 11h12v10H6zM8 11V7a4 4 0 0 1 8 0v4',
    eye: 'M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z',
    menu: 'M4 6h16M4 12h16M4 18h16',
    code: 'M8 7l-5 5 5 5M16 7l5 5-5 5M14 4l-4 16',
    gear: 'M9.9 5.1L10.3 2.2L13.7 2.2L14.1 5.1L15.4 5.6L17.7 3.8L20.2 6.3L18.4 8.6L18.9 9.9L21.8 10.3L21.8 13.7L18.9 14.1L18.4 15.4L20.2 17.7L17.7 20.2L15.4 18.4L14.1 18.9L13.7 21.8L10.3 21.8L9.9 18.9L8.6 18.4L6.3 20.2L3.8 17.7L5.6 15.4L5.1 14.1L2.2 13.7L2.2 10.3L5.1 9.9L5.6 8.6L3.8 6.3L6.3 3.8L8.6 5.6zM12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z',
    repo: 'M4 5a2 2 0 0 1 2-2h13v14H6a2 2 0 0 0-2 2zM4 19a2 2 0 0 0 2 2h13v-4M9 7h6',
    ports: 'M4 8h14M14 4l4 4-4 4M20 16H6M10 12l-4 4 4 4',
    check: 'M5 12l5 5L20 7',
    globe: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18',
    eyeOff:
      'M10.6 5.1A10 10 0 0 1 12 5c6.5 0 10 7 10 7a17 17 0 0 1-2.6 3.5M6.6 6.6C3.7 8.5 2 12 2 12s3.5 7 10 7a9.7 9.7 0 0 0 5.4-1.6M9.9 9.9a3 3 0 0 0 4.2 4.2M3 3l18 18',
  }
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill={filled ? 'currentColor' : 'none'}
  stroke="currentColor"
  stroke-width="2"
  stroke-linecap="square"
  stroke-linejoin="miter"
  aria-hidden="true"
>
  <path d={paths[name] ?? ''} />
</svg>
