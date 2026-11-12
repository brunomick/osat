import { describe, expect, it } from 'vitest'
import { treeMove, type TreeItem } from './treenav'

// Favoritos (abierto)
//   fav1
// grupo A (abierto)
//   subgrupo B (plegado)
//   conexión c1
// grupo C (plegado)
const items: TreeItem[] = [
  { level: 1, expanded: true }, // 0 Favoritos
  { level: 2 }, // 1 fav1
  { level: 1, expanded: true }, // 2 A
  { level: 2, expanded: false }, // 3 B
  { level: 2 }, // 4 c1
  { level: 1, expanded: false }, // 5 C
]

describe('treeMove', () => {
  it('abajo y arriba', () => {
    expect(treeMove(items, 1, 'ArrowDown')).toEqual({ focus: 2 })
    expect(treeMove(items, 1, 'ArrowUp')).toEqual({ focus: 0 })
  })
  it('no se sale por los extremos', () => {
    expect(treeMove(items, 5, 'ArrowDown')).toBeNull()
    expect(treeMove(items, 0, 'ArrowUp')).toBeNull()
  })
  it('Inicio y Fin', () => {
    expect(treeMove(items, 3, 'Home')).toEqual({ focus: 0 })
    expect(treeMove(items, 3, 'End')).toEqual({ focus: 5 })
    expect(treeMove(items, 0, 'Home')).toBeNull()
    expect(treeMove(items, 5, 'End')).toBeNull()
  })
  it('derecha en un grupo plegado lo abre', () => {
    expect(treeMove(items, 3, 'ArrowRight')).toEqual({ toggle: true })
    expect(treeMove(items, 5, 'ArrowRight')).toEqual({ toggle: true })
  })
  it('derecha en un grupo abierto baja al primer hijo', () => {
    expect(treeMove(items, 2, 'ArrowRight')).toEqual({ focus: 3 })
  })
  it('derecha en un grupo abierto sin hijos visibles o en una hoja no hace nada', () => {
    expect(treeMove([{ level: 1, expanded: true }, { level: 1 }], 0, 'ArrowRight')).toBeNull()
    expect(treeMove(items, 4, 'ArrowRight')).toBeNull()
  })
  it('izquierda en un grupo abierto lo pliega', () => {
    expect(treeMove(items, 2, 'ArrowLeft')).toEqual({ toggle: true })
  })
  it('izquierda en un hijo o en un grupo plegado sube al padre', () => {
    expect(treeMove(items, 4, 'ArrowLeft')).toEqual({ focus: 2 })
    expect(treeMove(items, 3, 'ArrowLeft')).toEqual({ focus: 2 })
    expect(treeMove(items, 1, 'ArrowLeft')).toEqual({ focus: 0 })
  })
  it('izquierda en una raíz plegada o sin padre no hace nada', () => {
    expect(treeMove(items, 5, 'ArrowLeft')).toBeNull()
  })
  it('otras teclas y filas inexistentes', () => {
    expect(treeMove(items, 1, 'a')).toBeNull()
    expect(treeMove(items, 99, 'ArrowDown')).toBeNull()
  })
})
