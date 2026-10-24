import type { components } from '../api/schema.gen'

export type Supplier = components['schemas']['MarketBrowseSupplier']
export type Group = components['schemas']['MarketBrowseGroup']

/** 供应商筛选的两种作用域：整供应商（全部分组）与零散分组，后端按并集过滤。 */
export type ParentSelection = {
  suppliers: Set<string>
  groups: Set<string>
}

export type SupplierState = 'all' | 'some' | 'none'

export function readParentSelection(params: URLSearchParams): ParentSelection {
  return {
    suppliers: new Set(params.getAll('supplier_id')),
    groups: new Set(params.getAll('group_id')),
  }
}

/** 写回 URL：排序去重，清掉游标；保持分享链接稳定。 */
export function writeParentSelection(
  params: URLSearchParams,
  selection: ParentSelection,
): URLSearchParams {
  const next = new URLSearchParams(params)
  next.delete('supplier_id')
  next.delete('group_id')
  next.delete('cursor')
  for (const id of [...selection.suppliers].sort()) next.append('supplier_id', id)
  for (const id of [...selection.groups].sort()) next.append('group_id', id)
  return next
}

export function supplierState(
  supplier: Supplier,
  selection: ParentSelection,
): SupplierState {
  if (selection.suppliers.has(supplier.id)) return 'all'
  return supplier.groups.some((id) => selection.groups.has(id)) ? 'some' : 'none'
}

/** 勾供应商=全选它的分组，取消=清空它的分组；半选状态点一下变全选。 */
export function toggleSupplier(
  supplier: Supplier,
  selection: ParentSelection,
): ParentSelection {
  const suppliers = new Set(selection.suppliers)
  const groups = new Set(selection.groups)
  if (suppliers.delete(supplier.id)) {
    supplier.groups.forEach((id) => groups.delete(id))
  } else {
    suppliers.add(supplier.id)
    supplier.groups.forEach((id) => groups.delete(id))
  }
  return { suppliers, groups }
}

/**
 * 勾一个分组：供应商的全部组都勾上时收拢成整供应商，避免 URL 里留一堆组 ID；
 * 从整供应商里取消一个组时，其余组保留为半选。
 */
export function toggleGroup(
  groupID: string,
  supplier: Supplier,
  selection: ParentSelection,
): ParentSelection {
  const suppliers = new Set(selection.suppliers)
  const groups = new Set(selection.groups)
  if (suppliers.delete(supplier.id)) {
    supplier.groups.forEach((id) => {
      if (id !== groupID) groups.add(id)
    })
    return { suppliers, groups }
  }
  if (groups.delete(groupID)) return { suppliers, groups }
  groups.add(groupID)
  if (supplier.groups.length > 0 && supplier.groups.every((id) => groups.has(id))) {
    suppliers.add(supplier.id)
    supplier.groups.forEach((id) => groups.delete(id))
  }
  return { suppliers, groups }
}

export function isAllSuppliers(selection: ParentSelection) {
  return selection.suppliers.size === 0 && selection.groups.size === 0
}

/** 触发器摘要：全部供应商 / 供应商名 / N 个供应商 · M 个分组。 */
export function parentSummary(
  suppliers: Supplier[],
  groups: Group[],
  selection: ParentSelection,
): string {
  if (isAllSuppliers(selection)) return '全部供应商'
  const full = suppliers.filter((supplier) => selection.suppliers.has(supplier.id))
  const groupSupplierIDs = new Set(
    [...selection.groups]
      .map((id) => groups.find((group) => group.id === id)?.supplier_id)
      .filter((id): id is string => Boolean(id)),
  )
  const parts: string[] = []
  if (selection.suppliers.size === 1) parts.push(full[0]?.name ?? '1 个供应商')
  else if (selection.suppliers.size > 1) parts.push(`${selection.suppliers.size} 个供应商`)
  else if (groupSupplierIDs.size === 1) {
    const id = [...groupSupplierIDs][0]
    const name = suppliers.find((supplier) => supplier.id === id)?.name
    if (name) parts.push(name)
  }
  if (selection.groups.size > 0) parts.push(`${selection.groups.size} 个分组`)
  return parts.join(' · ')
}
