// noVNC no trae tipos: acá van solo los que usa VncView.svelte (ver docs/API.md del paquete).
declare module '@novnc/novnc' {
  export interface RFBCredentials {
    username?: string
    password?: string
    target?: string
  }

  export interface RFBOptions {
    shared?: boolean
    credentials?: RFBCredentials
  }

  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, urlOrChannel: string | WebSocket, options?: RFBOptions)
    /** La imagen se escala a su contenedor. */
    scaleViewport: boolean
    /** Pedirle al servidor que cambie su resolución al tamaño del contenedor. */
    resizeSession: boolean
    sendCredentials(credentials: RFBCredentials): void
    sendCtrlAltDel(): void
    clipboardPasteFrom(text: string): void
    disconnect(): void
    focus(options?: FocusOptions): void
  }
}
