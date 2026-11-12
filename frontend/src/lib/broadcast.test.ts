import { beforeEach, describe, expect, it } from 'vitest'
import { get } from 'svelte/store'
import {
  bracketedModes,
  broadcast,
  candidates,
  defaultSelection,
  destinations,
  isActive,
  pasteData,
  pasteEach,
  prune,
  recipients,
  remove,
  setBracketed,
  start,
  stop,
  type BroadcastTab,
} from './broadcast'

const term = (id: string, p: Partial<BroadcastTab> = {}): BroadcastTab => ({
  id,
  kind: 'terminal',
  status: 'ready',
  sessionId: `s-${id}`,
  ...p,
})

const targets = () => get(broadcast).targets

beforeEach(() => {
  stop()
  bracketedModes.set({})
})

describe('start / stop', () => {
  it('arranca con dos o más y queda activo', () => {
    start(['a', 'b', 'c'])
    expect(targets()).toEqual(['a', 'b', 'c'])
    expect(isActive()).toBe(true)
  })
  it('con menos de dos no arranca', () => {
    start(['a'])
    expect(targets()).toEqual([])
    expect(isActive()).toBe(false)
    start([])
    expect(isActive()).toBe(false)
  })
  it('no cuenta dos veces la misma pestaña', () => {
    start(['a', 'a'])
    expect(isActive()).toBe(false)
    start(['a', 'b', 'a'])
    expect(targets()).toEqual(['a', 'b'])
  })
  it('arrancar de nuevo reemplaza las elegidas; con menos de dos lo apaga', () => {
    start(['a', 'b'])
    start(['c', 'd', 'e'])
    expect(targets()).toEqual(['c', 'd', 'e'])
    start(['c'])
    expect(isActive()).toBe(false)
  })
  it('stop lo apaga', () => {
    start(['a', 'b'])
    stop()
    expect(targets()).toEqual([])
    expect(isActive()).toBe(false)
  })
})

describe('remove', () => {
  it('saca una terminal y sigue si quedan dos o más', () => {
    start(['a', 'b', 'c'])
    remove('b')
    expect(targets()).toEqual(['a', 'c'])
    expect(isActive()).toBe(true)
  })
  it('si quedan menos de dos, se detiene', () => {
    start(['a', 'b'])
    remove('a')
    expect(targets()).toEqual([])
    expect(isActive()).toBe(false)
  })
  it('una que no participa no cambia nada', () => {
    start(['a', 'b'])
    remove('z')
    expect(targets()).toEqual(['a', 'b'])
  })
})

describe('candidates', () => {
  it('solo terminales conectadas con sesión', () => {
    const tabs: BroadcastTab[] = [
      term('a'),
      term('b', { kind: 'sftp' }),
      term('c', { kind: 'rdp' }),
      term('d', { status: 'connecting', sessionId: undefined }),
      term('e', { status: 'closed', sessionId: undefined }),
      term('f', { sessionId: undefined }),
      term('g'),
    ]
    expect(candidates(tabs).map((t) => t.id)).toEqual(['a', 'g'])
  })
})

describe('defaultSelection', () => {
  it('marca las que se ven en el mosaico', () => {
    const cands = [term('a'), term('b'), term('c')]
    expect(defaultSelection(cands, ['c', 'a', 'x'])).toEqual(['a', 'c'])
    expect(defaultSelection(cands, [])).toEqual([])
  })
})

describe('recipients', () => {
  const tabs = [term('a'), term('b'), term('c'), term('d')]

  it('lo que se escribe en una va a todas las del broadcast, incluida ella', () => {
    start(['a', 'b', 'c'])
    expect(recipients('b', tabs)).toEqual([
      { id: 'a', sessionId: 's-a' },
      { id: 'b', sessionId: 's-b' },
      { id: 'c', sessionId: 's-c' },
    ])
  })
  it('vacío si la pestaña no está en el broadcast', () => {
    start(['a', 'b'])
    expect(recipients('d', tabs)).toEqual([])
  })
  it('vacío si no hay broadcast', () => {
    expect(recipients('a', tabs)).toEqual([])
  })
  it('se saltea las que no están conectadas', () => {
    start(['a', 'b', 'c'])
    const now = [term('a'), term('b', { status: 'connecting', sessionId: undefined }), term('c')]
    expect(recipients('a', now).map((r) => r.id)).toEqual(['a', 'c'])
  })
  it('usa la sesión actual de cada pestaña (después de reconectar cambia)', () => {
    start(['a', 'b'])
    expect(recipients('a', [term('a', { sessionId: 'nueva' }), term('b')]).map((r) => r.sessionId)).toEqual(['nueva', 's-b'])
  })
})

describe('destinations', () => {
  it('las del broadcast conectadas, participe o no quien pregunta', () => {
    start(['a', 'c'])
    expect(destinations([term('a'), term('b'), term('c')]).map((r) => r.id)).toEqual(['a', 'c'])
  })
  it('vacío sin broadcast', () => {
    expect(destinations([term('a')])).toEqual([])
  })
})

describe('prune', () => {
  it('saca las que se cerraron o se cayeron', () => {
    start(['a', 'b', 'c'])
    prune([term('a'), term('c')]) // b se cerró
    expect(targets()).toEqual(['a', 'c'])
    prune([term('a'), term('c', { status: 'closed', sessionId: undefined })]) // c se cayó
    expect(isActive()).toBe(false)
    expect(targets()).toEqual([])
  })
  it('saca una pestaña que dejó de ser una terminal conectada', () => {
    start(['a', 'b', 'c'])
    prune([term('a'), term('b', { status: 'connecting', sessionId: undefined }), term('c')])
    expect(targets()).toEqual(['a', 'c'])
  })
  it('no toca nada si todas siguen bien', () => {
    start(['a', 'b'])
    const before = get(broadcast)
    prune([term('a'), term('b'), term('c')])
    expect(get(broadcast)).toBe(before)
  })
  it('sin broadcast no hace nada', () => {
    prune([])
    expect(targets()).toEqual([])
  })
})

describe('pasteData', () => {
  it('pegar sin Enter', () => {
    expect(pasteData('ls -la', false, false)).toBe('ls -la')
  })
  it('ejecutar: con Enter al final', () => {
    expect(pasteData('ls -la', true, false)).toBe('ls -la\r')
  })
  it('los saltos de línea van como Enter', () => {
    expect(pasteData('cd /srv\nls\r\npwd', true, false)).toBe('cd /srv\rls\rpwd\r')
  })
  it('con pegado entre corchetes, el Enter va afuera de las marcas', () => {
    expect(pasteData('a\nb', true, true)).toBe('\x1b[200~a\rb\x1b[201~\r')
    expect(pasteData('a', false, true)).toBe('\x1b[200~a\x1b[201~')
  })
})

describe('pegado entre corchetes por destino', () => {
  const dests = [
    { id: 'a', sessionId: 's-a' },
    { id: 'b', sessionId: 's-b' },
    { id: 'c', sessionId: 's-c' },
  ]

  it('cada destino recibe el pegado según el modo de su terminal', () => {
    expect(pasteEach(dests, 'ls\npwd', true, { a: true, c: true })).toEqual([
      { sessionId: 's-a', data: '\x1b[200~ls\rpwd\x1b[201~\r' },
      { sessionId: 's-b', data: 'ls\rpwd\r' }, // b no lo activó: no recibe las marcas
      { sessionId: 's-c', data: '\x1b[200~ls\rpwd\x1b[201~\r' },
    ])
  })
  it('una terminal que no publicó nada se trata como sin pegado entre corchetes', () => {
    expect(pasteEach(dests, 'x', false, {}).map((r) => r.data)).toEqual(['x', 'x', 'x'])
  })
  it('sin destinos no hay nada que escribir', () => {
    expect(pasteEach([], 'x', true, { a: true })).toEqual([])
  })
  it('usa el mapa que publican las terminales y se mantiene al día', () => {
    setBracketed('a', true)
    setBracketed('b', true)
    expect(get(bracketedModes)).toEqual({ a: true, b: true })
    setBracketed('b', false) // un programa a pantalla completa lo apagó
    expect(get(bracketedModes)).toEqual({ a: true })
    expect(pasteEach(dests, 'x', false, get(bracketedModes)).map((r) => r.data)).toEqual(['\x1b[200~x\x1b[201~', 'x', 'x'])
  })
  it('publicar lo mismo dos veces no cambia el mapa', () => {
    setBracketed('a', true)
    const before = get(bracketedModes)
    setBracketed('a', true)
    setBracketed('z', false)
    expect(get(bracketedModes)).toBe(before)
  })
})
