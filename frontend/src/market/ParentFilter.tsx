import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { Icon } from '../ui'
import {
  isAllSuppliers,
  parentSummary,
  supplierState,
  toggleGroup,
  toggleSupplier,
  type Group,
  type ParentSelection,
  type Supplier,
} from './parentSelection'
import './parent-filter.css'

function CheckRow({
  checked,
  indeterminate = false,
  active = false,
  onActivate,
  onToggle,
  children,
}: {
  checked: boolean
  indeterminate?: boolean
  active?: boolean
  onActivate?: () => void
  onToggle: () => void
  children: ReactNode
}) {
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (input.current) input.current.indeterminate = indeterminate
  }, [indeterminate])
  return (
    <label
      className={`parent-filter-row${active ? ' parent-filter-row-active' : ''}`}
      onFocus={onActivate}
      onMouseEnter={onActivate}
    >
      <input ref={input} checked={checked} onChange={onToggle} type="checkbox" />
      <span className="parent-filter-name">{children}</span>
    </label>
  )
}

/**
 * 模型页的供应商/分组筛选：左栏复选供应商，右栏查看并复选该供应商的分组。
 * 勾供应商代表它的全部分组；只勾部分分组时供应商呈半选。
 */
export function ParentFilter({
  suppliers,
  groups,
  selection,
  onChange,
  loading = false,
}: {
  suppliers: Supplier[]
  groups: Group[]
  selection: ParentSelection
  onChange: (selection: ParentSelection) => void
  loading?: boolean
}) {
  const [open, setOpen] = useState(false)
  const [activeID, setActiveID] = useState('')
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const panelID = useId()
  const summary = parentSummary(suppliers, groups, selection)

  const active =
    suppliers.find((supplier) => supplier.id === activeID) ??
    suppliers.find((supplier) => supplierState(supplier, selection) !== 'none') ??
    suppliers[0]
  const activeGroups = active ? groups.filter((group) => group.supplier_id === active.id) : []

  useEffect(() => {
    if (!open) return
    panel.current?.focus()
    const onPointer = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false)
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setOpen(false)
      trigger.current?.focus()
    }
    document.addEventListener('pointerdown', onPointer)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('pointerdown', onPointer)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <div className="parent-filter" ref={root}>
      <button
        aria-controls={panelID}
        aria-expanded={open}
        aria-label={`供应商与分组筛选：${summary}`}
        className="parent-filter-trigger"
        onClick={() => setOpen((value) => !value)}
        ref={trigger}
        title={summary}
        type="button"
      >
        <span>{summary}</span>
        <Icon name="chevron-down" size={16} />
      </button>
      {open && (
        <div
          aria-label="供应商与分组筛选"
          className="parent-filter-panel"
          id={panelID}
          ref={panel}
          role="group"
          tabIndex={-1}
        >
          <div className="parent-filter-column">
            <p className="parent-filter-heading">供应商</p>
            <CheckRow
              checked={isAllSuppliers(selection)}
              indeterminate={!isAllSuppliers(selection)}
              onToggle={() => onChange({ suppliers: new Set(), groups: new Set() })}
            >
              全部供应商
            </CheckRow>
            {loading ? (
              <p className="parent-filter-empty">正在加载供应商…</p>
            ) : suppliers.length === 0 ? (
              <p className="parent-filter-empty">暂无供应商</p>
            ) : (
              suppliers.map((supplier) => {
                const state = supplierState(supplier, selection)
                return (
                  <CheckRow
                    active={supplier.id === active?.id}
                    checked={state === 'all'}
                    indeterminate={state === 'some'}
                    key={supplier.id}
                    onActivate={() => setActiveID(supplier.id)}
                    onToggle={() => {
                      setActiveID(supplier.id)
                      onChange(toggleSupplier(supplier, selection))
                    }}
                  >
                    {supplier.name}
                  </CheckRow>
                )
              })
            )}
          </div>
          <div className="parent-filter-column">
            <p className="parent-filter-heading">{active ? active.name : '分组'}</p>
            {activeGroups.length ? (
              activeGroups.map((group) => (
                <CheckRow
                  checked={
                    selection.suppliers.has(group.supplier_id) || selection.groups.has(group.id)
                  }
                  key={group.id}
                  onToggle={() => {
                    if (active) onChange(toggleGroup(group.id, active, selection))
                  }}
                >
                  {group.name}
                </CheckRow>
              ))
            ) : (
              <p className="parent-filter-empty">
                {suppliers.length === 0 ? '选择供应商后显示分组' : '该供应商暂无分组'}
              </p>
            )}
          </div>
        </div>
      )}
    </div>
  )
}
