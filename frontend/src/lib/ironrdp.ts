// Cliente RDP del frontend: IronRDP (Devolutions) compilado a WASM. Se carga
// recién al abrir el primer escritorio remoto, así los ~6 MB del WASM no
// demoran el arranque de osat.

type IronRdp = typeof import('@devolutions/iron-remote-desktop-rdp')

let ready: Promise<IronRdp> | null = null

export function loadIronRdp(): Promise<IronRdp> {
  return (ready ??= (async () => {
    await import('@devolutions/iron-remote-desktop') // define el elemento <iron-remote-desktop>
    const rdp = await import('@devolutions/iron-remote-desktop-rdp')
    await rdp.init('WARN')
    return rdp
  })().catch((e) => {
    ready = null // que el próximo intento vuelva a probar
    throw e
  }))
}
