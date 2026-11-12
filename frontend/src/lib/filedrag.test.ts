// @vitest-environment jsdom
// Un solo archivo para las funciones puras y para beginFileDrag: todo corre en jsdom.
import { afterEach, beforeEach, describe as suite, expect, it, vi } from 'vitest'
import { get } from 'svelte/store'

// El sistema operativo se cambia por test: filedrag.ts lee `platform` cada vez que se mueve el mouse.
const os = vi.hoisted(() => ({ value: 'linux' as 'windows' | 'darwin' | 'linux' }))
vi.mock('./platform', () => ({
  get platform() {
    return os.value
  },
}))

import { allIn, baseName, beginFileDrag, describe, fileDrag, fsViews, parentOf, type FsSource, type FsView } from './filedrag'

suite('baseName', () => {
  it('toma el último tramo de una ruta POSIX', () => {
    expect(baseName('/srv/www/index.html')).toBe('index.html')
  })
  it('también de una ruta de Windows', () => {
    expect(baseName('C:\\Users\\bruno\\notas.txt')).toBe('notas.txt')
  })
  it('ignora las barras del final', () => {
    expect(baseName('/srv/www/')).toBe('www')
    expect(baseName('C:\\Users\\')).toBe('Users')
  })
  it('para la raíz devuelve la ruta tal cual', () => {
    expect(baseName('/')).toBe('/')
  })
})

suite('describe', () => {
  it('un elemento se nombra entre comillas', () => {
    expect(describe(['/srv/a.txt'])).toBe('“a.txt”')
  })
  it('varios se cuentan', () => {
    expect(describe(['/a', '/b'])).toBe('2 elementos')
    expect(describe(['/a', '/b', '/c'])).toBe('3 elementos')
  })
})

suite('parentOf', () => {
  it('rutas POSIX', () => {
    expect(parentOf('/a/b')).toBe('/a')
    expect(parentOf('/a/b/c.txt')).toBe('/a/b')
  })
  it('un elemento de la raíz tiene por padre la raíz', () => {
    expect(parentOf('/a')).toBe('/')
    expect(parentOf('/')).toBe('/')
  })
  it('ignora las barras del final', () => {
    expect(parentOf('/a/b/')).toBe('/a')
  })
  it('rutas de Windows', () => {
    expect(parentOf('C:\\a\\b')).toBe('C:\\a')
    expect(parentOf('C:\\a\\b\\c.txt')).toBe('C:\\a\\b')
  })
  it('el padre de un elemento de la raíz de una unidad es la unidad con su barra', () => {
    expect(parentOf('C:\\a')).toBe('C:\\')
    expect(parentOf('C:\\')).toBe('C:\\')
  })
})

suite('allIn', () => {
  it('true si todos están directamente en la carpeta', () => {
    expect(allIn(['/a/x', '/a/y'], '/a')).toBe(true)
  })
  it('false si alguno está en otra carpeta', () => {
    expect(allIn(['/a/x', '/b/y'], '/a')).toBe(false)
  })
  it('false si están más adentro (no es el padre directo)', () => {
    expect(allIn(['/a/b/x'], '/a')).toBe(false)
  })
  it('rutas de Windows', () => {
    expect(allIn(['C:\\a\\x', 'C:\\a\\y'], 'C:\\a')).toBe(true)
    expect(allIn(['C:\\x'], 'C:\\')).toBe(true)
  })
})

suite('beginFileDrag', () => {
  const source: FsSource = { tabId: 'src', side: 'remote', connId: 'c1', sessionId: 's1' }
  const paths = ['/home/a.txt', '/home/b.txt']

  // jsdom no implementa elementFromPoint: cada test dice qué elemento hay "bajo el mouse".
  const under = vi.fn<(x: number, y: number) => Element | null>()
  let view: FsView
  let folder: HTMLElement // carpeta (data-drop) dentro de la lista de la pestaña "dst"
  let emptyArea: HTMLElement // lista de "dst" sin carpeta debajo
  const out = vi.fn()
  const end = vi.fn()

  const mouse = (type: 'mousemove' | 'mouseup', x = 10, y = 10) =>
    window.dispatchEvent(new MouseEvent(type, { clientX: x, clientY: y, bubbles: true }))
  const escape = () => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', cancelable: true, bubbles: true }))

  beforeEach(() => {
    os.value = 'linux'
    under.mockReset()
    out.mockReset()
    end.mockReset()
    document.elementFromPoint = under
    document.body.innerHTML = `
      <div data-fs-tab="dst" id="list">
        <div id="empty"></div>
        <div data-drop="/srv/dir" id="folder"></div>
      </div>`
    folder = document.getElementById('folder')!
    emptyArea = document.getElementById('empty')!
    view = {
      cwd: vi.fn(() => '/srv'),
      verb: vi.fn(() => 'Subir a /srv/dir'),
      drop: vi.fn(),
      refresh: vi.fn(),
    }
    fsViews.clear()
    fsViews.set('dst', view)
  })

  afterEach(() => {
    // Si un test dejó un arrastre abierto, Escape lo cierra y saca sus listeners de window.
    escape()
    fsViews.clear()
    fileDrag.set(null)
  })

  it('no hace nada hasta que el mouse se mueve', () => {
    beginFileDrag(source, paths, out, end)
    expect(get(fileDrag)).toBeNull()
  })

  it('al mover sobre una carpeta actualiza el store con el destino y el texto de la vista', () => {
    under.mockReturnValue(folder)
    beginFileDrag(source, paths, out, end)
    mouse('mousemove', 30, 40)
    expect(get(fileDrag)).toEqual({
      source,
      paths,
      x: 30,
      y: 40,
      target: { tabId: 'dst', dir: '/srv/dir' },
      label: 'Subir a /srv/dir',
    })
    expect(under).toHaveBeenCalledWith(30, 40)
    expect(view.verb).toHaveBeenCalledWith(source, paths, '/srv/dir')
  })

  it('sobre la lista sin carpeta debajo, el destino es la carpeta actual de la vista', () => {
    under.mockReturnValue(emptyArea)
    beginFileDrag(source, paths, out, end)
    mouse('mousemove')
    expect(get(fileDrag)?.target).toEqual({ tabId: 'dst', dir: '/srv' })
    expect(view.verb).toHaveBeenCalledWith(source, paths, '/srv')
  })

  it('sigue al mouse: cada movimiento actualiza la posición', () => {
    under.mockReturnValue(folder)
    beginFileDrag(source, paths, out, end)
    mouse('mousemove', 1, 2)
    mouse('mousemove', 3, 4)
    expect(get(fileDrag)).toMatchObject({ x: 3, y: 4 })
  })

  it('si la vista no acepta el destino (verb null) no hay target y el texto es el genérico', () => {
    vi.mocked(view.verb).mockReturnValue(null)
    under.mockReturnValue(folder)
    beginFileDrag(source, paths, out, end)
    mouse('mousemove')
    expect(get(fileDrag)).toMatchObject({ target: null, label: '2 elementos · soltá en una carpeta' })
  })

  it('no pregunta a la vista si una de las rutas es la propia carpeta destino', () => {
    under.mockReturnValue(folder)
    beginFileDrag(source, ['/srv/dir'], out, end)
    mouse('mousemove')
    expect(view.verb).not.toHaveBeenCalled()
    expect(get(fileDrag)).toMatchObject({ target: null, label: '“dir” · soltá en una carpeta' })
  })

  it('fuera de cualquier vista registrada no hay target', () => {
    under.mockReturnValue(document.body)
    beginFileDrag(source, paths, out, end)
    mouse('mousemove')
    expect(get(fileDrag)?.target).toBeNull()
    expect(view.verb).not.toHaveBeenCalled()
  })

  it('una lista cuya pestaña no está en fsViews tampoco es destino', () => {
    fsViews.clear()
    under.mockReturnValue(folder)
    beginFileDrag(source, paths, out, end)
    mouse('mousemove')
    expect(get(fileDrag)?.target).toBeNull()
  })

  it('soltar sobre una vista registrada llama a su drop con el origen, las rutas y la carpeta', () => {
    under.mockReturnValue(folder)
    beginFileDrag(source, paths, out, end)
    mouse('mousemove')
    mouse('mouseup')
    expect(view.drop).toHaveBeenCalledTimes(1)
    expect(view.drop).toHaveBeenCalledWith(source, paths, '/srv/dir')
    expect(get(fileDrag)).toBeNull()
    expect(end).toHaveBeenCalledTimes(1)
    expect(out).not.toHaveBeenCalled()
  })

  it('soltar sin destino válido no llama a drop pero sí termina', () => {
    vi.mocked(view.verb).mockReturnValue(null)
    under.mockReturnValue(folder)
    beginFileDrag(source, paths, out, end)
    mouse('mousemove')
    mouse('mouseup')
    expect(view.drop).not.toHaveBeenCalled()
    expect(get(fileDrag)).toBeNull()
    expect(end).toHaveBeenCalledTimes(1)
  })

  it('soltar sin haber movido el mouse no hace nada', () => {
    beginFileDrag(source, paths, out, end)
    mouse('mouseup')
    expect(view.drop).not.toHaveBeenCalled()
    expect(end).toHaveBeenCalledTimes(1)
  })

  it('Escape cancela sin soltar', () => {
    under.mockReturnValue(folder)
    beginFileDrag(source, paths, out, end)
    mouse('mousemove')
    expect(get(fileDrag)?.target).not.toBeNull()
    const notPrevented = escape()
    expect(notPrevented).toBe(false) // preventDefault: la tecla no llega a nadie más
    expect(get(fileDrag)).toBeNull()
    expect(end).toHaveBeenCalledTimes(1)
    mouse('mouseup')
    expect(view.drop).not.toHaveBeenCalled()
  })

  it('otras teclas no cancelan', () => {
    under.mockReturnValue(folder)
    beginFileDrag(source, paths, out, end)
    mouse('mousemove')
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' }))
    expect(get(fileDrag)).not.toBeNull()
    expect(end).not.toHaveBeenCalled()
  })

  it('al terminar deja de escuchar el mouse', () => {
    under.mockReturnValue(folder)
    beginFileDrag(source, paths, out, end)
    mouse('mousemove')
    mouse('mouseup')
    mouse('mousemove', 99, 99)
    expect(get(fileDrag)).toBeNull()
    mouse('mouseup')
    expect(view.drop).toHaveBeenCalledTimes(1)
    expect(end).toHaveBeenCalledTimes(1)
  })

  it('funciona sin la función end', () => {
    under.mockReturnValue(folder)
    beginFileDrag(source, paths, out)
    mouse('mousemove')
    expect(() => mouse('mouseup')).not.toThrow()
    expect(view.drop).toHaveBeenCalledTimes(1)
  })

  suite('en Windows', () => {
    beforeEach(() => {
      os.value = 'windows'
    })

    it('el texto sin destino sugiere soltar fuera de la ventana', () => {
      under.mockReturnValue(document.body)
      beginFileDrag(source, paths, out, end)
      mouse('mousemove')
      expect(get(fileDrag)?.label).toBe('2 elementos · soltá en una carpeta o fuera de la ventana')
    })

    it.each([
      ['a la izquierda', () => [-1, 50]],
      ['arriba', () => [50, -1]],
      ['a la derecha', () => [window.innerWidth, 50]],
      ['abajo', () => [50, window.innerHeight]],
    ])('al salir de la ventana %s termina y llama a out con las rutas', (_lado, pos) => {
      under.mockReturnValue(folder)
      beginFileDrag(source, paths, out, end)
      mouse('mousemove', 20, 20)
      const [x, y] = pos()
      mouse('mousemove', x, y)
      expect(out).toHaveBeenCalledTimes(1)
      expect(out).toHaveBeenCalledWith(paths)
      expect(get(fileDrag)).toBeNull()
      expect(end).toHaveBeenCalledTimes(1)
      mouse('mouseup')
      expect(view.drop).not.toHaveBeenCalled()
    })

    it('moverse por dentro de la ventana no llama a out', () => {
      under.mockReturnValue(folder)
      beginFileDrag(source, paths, out, end)
      mouse('mousemove', 0, 0)
      mouse('mousemove', window.innerWidth - 1, window.innerHeight - 1)
      expect(out).not.toHaveBeenCalled()
    })
  })

  suite('en Linux', () => {
    it('salir de la ventana no hace nada especial: el arrastre sigue', () => {
      under.mockReturnValue(null)
      beginFileDrag(source, paths, out, end)
      mouse('mousemove', -5, 20)
      expect(out).not.toHaveBeenCalled()
      expect(end).not.toHaveBeenCalled()
      expect(get(fileDrag)).toMatchObject({ target: null, label: '2 elementos · soltá en una carpeta' })
    })
  })
})
