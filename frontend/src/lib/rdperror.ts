import type { OsatError } from './api'
import { hasKey, tr } from './i18n'

export interface RdpFailure {
  /** El paso que falló, en palabras ("" si no se sabe). */
  step: string
  /** Detalles técnicos: [etiqueta, valor]. */
  details: [string, string][]
}

/**
 * El paso y los detalles técnicos de un error del gateway (GATEWAY, o AUTH_FAILED del gateway) o del
 * servidor RDP (RDP_SERVER), para la pantalla de error de la pestaña. null si el error no trae paso.
 */
export function rdpFailure(k: OsatError | null): RdpFailure | null {
  const d = k?.data ?? {}
  if (!k || !d.step) return null
  const stepKey = `rdp.${k.code === 'RDP_SERVER' ? 'srvStep' : 'gwStep'}.${d.step}`
  const step = hasKey(stepKey) ? tr(stepKey) : d.step
  const code = d.code ? (d.codeName ? `${d.code} (${d.codeName})` : d.code) : ''
  const rows: [string, string | undefined][] = [
    [tr('rdp.dCode'), [k.code, d.kind, d.reason].filter(Boolean).join(' / ')],
    [tr('rdp.dStep'), `${step} (${d.step})`],
    [tr('rdp.gateway'), d.gateway],
    [tr('rdp.dTarget'), d.target ?? d.host],
    [tr('rdp.dGwCode'), code],
    [tr('rdp.dStatus'), d.status],
    [tr('rdp.dDetail'), d.detail],
  ]
  return { step, details: rows.filter((r): r is [string, string] => !!r[1]) }
}

/** Los detalles como texto, para copiarlos. */
export const failureText = (message: string, f: RdpFailure) =>
  [message, ...f.details.map(([k, v]) => `${k}: ${v}`)].join('\n')
