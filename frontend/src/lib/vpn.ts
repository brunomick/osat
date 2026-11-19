import { get, writable } from 'svelte/store'
import { api, errorText, type VpnInfo } from './api'
import { openAppTab, settingsSection, toast } from './stores'

/** La VPN guardada y el estado del túnel (null hasta la primera consulta). */
export const vpn = writable<VpnInfo | null>(null)

let timer: ReturnType<typeof setTimeout> | undefined

// Mientras la VPN está activada se consulta el estado: seguido mientras conecta, más espaciado después.
function schedule() {
  clearTimeout(timer)
  const v = get(vpn)
  if (!v?.enabled) return
  timer = setTimeout(refreshVpn, v.status.state === 'connecting' ? 2000 : 10000)
}

export async function refreshVpn() {
  try {
    vpn.set(await api.vpnGet())
  } catch {
    // sin backend (vista previa): el botón queda como sin configurar
  }
  schedule()
}

function apply(v: VpnInfo) {
  vpn.set(v)
  schedule()
}

/** Botón de la VPN: la activa o desactiva; si todavía no hay una configurada, abre Ajustes → VPN. */
export async function toggleVpn() {
  const v = get(vpn)
  if (!v?.configured) {
    openVpnSettings()
    return
  }
  await setVpnEnabled(!v.enabled)
}

export async function setVpnEnabled(on: boolean) {
  try {
    apply(await api.vpnSetEnabled(on))
  } catch (e) {
    toast(errorText(e), 'error')
    await refreshVpn()
  }
}

export async function saveVpn(conf: string): Promise<boolean> {
  try {
    apply(await api.vpnSave(conf))
    return true
  } catch (e) {
    toast(errorText(e), 'error')
    return false
  }
}

export async function deleteVpn() {
  try {
    apply(await api.vpnDelete())
  } catch (e) {
    toast(errorText(e), 'error')
  }
}

export function openVpnSettings() {
  settingsSection.set('vpn')
  openAppTab('settings')
}
