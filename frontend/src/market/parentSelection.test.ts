import { describe, expect, it } from 'vitest'
import type { components } from '../api/schema.gen'
import {
  isAllSuppliers,
  parentSummary,
  readParentSelection,
  supplierState,
  toggleGroup,
  toggleSupplier,
  writeParentSelection,
  type ParentSelection,
} from './parentSelection'

type Supplier = components['schemas']['MarketBrowseSupplier']
type Group = components['schemas']['MarketBrowseGroup']

function supplier(id: string, name: string, groups: string[]): Supplier {
  return {
    id, name, groups,
    kind: 'official_api',
    website_url: '', icon_data: '', icon_source: '',
    summary: '', description: '', docs_url: '',
  }
}
function group(id: string, supplier_id: string, name: string): Group {
  return { id, supplier_id, name, description: '', status_url: '', model_ids: [] }
}
function selection(suppliers: string[] = [], groups: string[] = []): ParentSelection {
  return { suppliers: new Set(suppliers), groups: new Set(groups) }
}

const s1 = supplier('s1', '供应商一', ['g1', 'g2'])
const s2 = supplier('s2', '供应商二', ['g3'])
const groups = [group('g1', 's1', '分组一'), group('g2', 's1', '分组二'), group('g3', 's2', '分组三')]

describe('supplier/group selection', () => {
  it('round-trips repeated URL params, sorted and without the cursor', () => {
    const params = new URLSearchParams('q=x&cursor=abc&supplier_id=s2&supplier_id=s1&group_id=g2&group_id=g1')
    const read = readParentSelection(params)
    expect([...read.suppliers]).toEqual(['s2', 's1'])
    const written = writeParentSelection(params, selection(['s2', 's1'], ['g2', 'g1', 'g1']))
    expect(written.get('q')).toBe('x')
    expect(written.get('cursor')).toBeNull()
    expect(written.getAll('supplier_id')).toEqual(['s1', 's2'])
    expect(written.getAll('group_id')).toEqual(['g1', 'g2'])
  })

  it('marks whole, partial and unselected suppliers', () => {
    expect(supplierState(s1, selection())).toBe('none')
    expect(supplierState(s1, selection([], ['g1']))).toBe('some')
    expect(supplierState(s1, selection(['s1']))).toBe('all')
  })

  it('toggles a supplier between all groups and none', () => {
    const full = toggleSupplier(s1, selection([], ['g1']))
    expect([...full.suppliers]).toEqual(['s1'])
    expect([...full.groups]).toEqual([])
    const none = toggleSupplier(s1, full)
    expect(none.suppliers.size).toBe(0)
    expect(none.groups.size).toBe(0)
  })

  it('promotes a fully checked supplier and demotes it when one group is removed', () => {
    const first = toggleGroup('g1', s1, selection())
    expect([...first.groups]).toEqual(['g1'])
    const full = toggleGroup('g2', s1, first)
    expect([...full.suppliers]).toEqual(['s1'])
    expect([...full.groups]).toEqual([])
    const partial = toggleGroup('g2', s1, full)
    expect([...partial.suppliers]).toEqual([])
    expect([...partial.groups]).toEqual(['g1'])
  })

  it('summarizes all, named, multi and partial selections', () => {
    const empty = selection()
    expect(isAllSuppliers(empty)).toBe(true)
    expect(parentSummary([s1, s2], groups, empty)).toBe('全部供应商')
    expect(parentSummary([s1, s2], groups, selection(['s1']))).toBe('供应商一')
    expect(parentSummary([s1, s2], groups, selection(['s1', 's2']))).toBe('2 个供应商')
    expect(parentSummary([s1, s2], groups, selection([], ['g1', 'g2']))).toBe('供应商一 · 2 个分组')
    expect(parentSummary([s1, s2], groups, selection(['s1'], ['g3']))).toBe('供应商一 · 1 个分组')
  })
})
