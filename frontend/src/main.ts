import '@xterm/xterm/css/xterm.css'
import '@fontsource-variable/jetbrains-mono'
import './app.css'
import App from './App.svelte'
import { api } from './lib/api'
import { untilUnlocked } from './lib/lock'
import { loadPlatform } from './lib/platform'
import { initTheme } from './lib/theme'

initTheme()

// xterm mide el ancho de los caracteres al abrir cada terminal: la fuente
// (incluida en la app) tiene que estar cargada antes de montar, si no las
// columnas se calculan con la fuente de reemplazo. Las vistas también leen el
// sistema operativo (lib/platform) al montarse.
Promise.all([document.fonts.load('14px "JetBrains Mono Variable"').catch(() => {}), loadPlatform()])
  .finally(() => {
    new App({ target: document.getElementById('app')! })
    // Con osat bloqueado la animación espera detrás de la pantalla de bloqueo y arranca al desbloquear.
    untilUnlocked().then(() => {
      document.getElementById('splash')?.classList.remove('hold')
      hideSplash()
    })
  })

// Los errores de JavaScript que nadie atrapa van al log de osat (logs/osat.log).
function reportError(message: string, stack?: string) {
  try {
    api.logFrontendError(message, stack ?? '').catch(() => {})
  } catch {
    // sin backend (fuera de la app) no hay a dónde registrarlo
  }
}
window.addEventListener('error', (e) => reportError(e.message, e.error?.stack))
window.addEventListener('unhandledrejection', (e) => {
  const r = e.reason
  reportError(r instanceof Error ? r.message : String(r), r instanceof Error ? r.stack : undefined)
})

// La pantalla de inicio de index.html: cuando termina de armar "OSAT" (la app
// ya está montada debajo), se desliza hacia la derecha y se quita.
function hideSplash() {
  const splash = document.getElementById('splash')
  if (!splash) return
  const intro = splash.getAnimations({ subtree: true }).map((a) => a.finished)
  Promise.allSettled(intro).then(() =>
    setTimeout(() => {
      splash.addEventListener('transitionend', () => splash.remove(), { once: true })
      splash.classList.add('out')
      if (getComputedStyle(splash).transitionDuration === '0s') splash.remove() // sin animaciones
    }, 200),
  )
}
