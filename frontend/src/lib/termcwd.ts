// Carpeta actual de una terminal de este equipo: la shell la avisa en cada prompt con
// OSC 9;9 (ESC ] 9 ; 9 ; carpeta ESC \), como en Windows Terminal (internal/localterm).

/** La carpeta de los datos de un OSC 9 ("9;C:\x"), o null si es otro OSC 9 (una notificación…). */
export function oscCwd(data: string): string | null {
  if (!data.startsWith('9;')) return null
  let dir = data.slice(2).trim()
  if (dir.length > 1 && dir.startsWith('"') && dir.endsWith('"')) dir = dir.slice(1, -1)
  if (!dir) return null
  // Git Bash avisa "c:\x" (o "c:" en la raíz): la unidad en mayúscula y con su barra.
  if (/^[a-zA-Z]:/.test(dir)) dir = dir[0].toUpperCase() + ':' + (dir.slice(2) || '\\')
  return dir
}
