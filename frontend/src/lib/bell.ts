let ctx: AudioContext | undefined

/** Un pitido corto (880 Hz, ~80 ms, volumen bajo) generado en el momento: no hay archivos de audio. */
export function beep() {
  try {
    ctx ??= new AudioContext()
    if (ctx.state === 'suspended') ctx.resume().catch(() => {})
    const osc = ctx.createOscillator()
    const gain = ctx.createGain()
    osc.frequency.value = 880
    gain.gain.value = 0.05
    osc.connect(gain).connect(ctx.destination)
    osc.start()
    osc.stop(ctx.currentTime + 0.08)
  } catch {
    // sin audio disponible: la campana simplemente no suena
  }
}
