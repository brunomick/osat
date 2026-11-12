import { describe, expect, it } from 'vitest'
import { renderMarkdown } from './markdown'

describe('renderMarkdown', () => {
  it('títulos, listas y párrafos', () => {
    expect(renderMarkdown('### Agregado\n\n- Uno\n- Dos\n\nTexto suelto.')).toBe(
      '<h4>Agregado</h4><ul><li>Uno</li><li>Dos</li></ul><p>Texto suelto.</p>',
    )
  })
  it('negrita, cursiva, código y enlaces como texto', () => {
    expect(renderMarkdown('- **Guardar** en *Ajustes* con `Ctrl+S`, ver [docs](https://x)')).toBe(
      '<ul><li><b>Guardar</b> en <i>Ajustes</i> con <code>Ctrl+S</code>, ver docs</li></ul>',
    )
  })
  it('no interpreta asteriscos dentro del código', () => {
    expect(renderMarkdown('`*.conf` y `**x**`')).toBe('<p><code>*.conf</code> y <code>**x**</code></p>')
  })
  it('escapa el HTML', () => {
    expect(renderMarkdown('<img src=x onerror=alert(1)> & "comillas"')).toBe(
      '<p>&lt;img src=x onerror=alert(1)&gt; &amp; &quot;comillas&quot;</p>',
    )
  })
  it('CRLF y líneas que continúan un ítem', () => {
    expect(renderMarkdown('- Uno\r\n  sigue\r\n- Dos')).toBe('<ul><li>Uno sigue</li><li>Dos</li></ul>')
  })
})
