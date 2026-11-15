import { describe, expect, it } from 'vitest'
import { oscCwd } from './termcwd'

describe('oscCwd', () => {
  it('lee la carpeta de OSC 9;9', () => {
    expect(oscCwd('9;C:\\Users\\yo')).toBe('C:\\Users\\yo')
    expect(oscCwd('9;"C:\\Program Files"')).toBe('C:\\Program Files')
    expect(oscCwd('9;\\\\server\\share')).toBe('\\\\server\\share')
  })
  it('acomoda la unidad que avisa Git Bash', () => {
    expect(oscCwd('9;c:\\Users')).toBe('C:\\Users')
    expect(oscCwd('9;d:')).toBe('D:\\')
  })
  it('ignora los demás OSC 9', () => {
    expect(oscCwd('hola')).toBeNull()
    expect(oscCwd('4;1;50')).toBeNull()
    expect(oscCwd('9;')).toBeNull()
  })
})
