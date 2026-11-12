// Interfaz de Android (App.svelte con isAndroid): en lugar de barra lateral y paneles, una pantalla
// a la vez. Inicio (servidores) y Sesiones (pestañas abiertas) llevan la barra de abajo; una
// pestaña se muestra sola, a pantalla completa.
import { writable } from 'svelte/store'
import { isAppTab, type Tab } from './stores'

export type MobileScreen = 'home' | 'sessions' | 'tab'
export const mobileScreen = writable<MobileScreen>('home')

/** Adónde se vuelve al dejar una pestaña (tabs: las abiertas): a Sesiones si queda alguna; desde las de osat (ajustes, formularios…), a Inicio. */
export function backFrom(tab: Pick<Tab, 'kind'>, tabs: Pick<Tab, 'kind'>[]): MobileScreen {
  return !isAppTab(tab) && tabs.some((t) => !isAppTab(t)) ? 'sessions' : 'home'
}

/** "usuario@host:puerto" (usuario y puerto opcionales), como el campo host del formulario. */
export function parseTarget(s: string): { user: string; host: string; port: number } | null {
  const m = s.trim().match(/^(?:([^@\s]+)@)?([^:\s@]+)(?::(\d+))?$/)
  if (!m) return null
  return { user: m[1] ?? '', host: m[2], port: m[3] ? Number(m[3]) : 0 }
}
