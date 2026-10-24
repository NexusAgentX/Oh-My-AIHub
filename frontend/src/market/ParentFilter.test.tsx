// @vitest-environment jsdom
import { act, useState } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, beforeEach, expect, it } from 'vitest'
import { ParentFilter } from './ParentFilter'
import type { Group, ParentSelection, Supplier } from './parentSelection'

function supplier(id: string, name: string, groups: string[]): Supplier {
  return {
    id, name, groups,
    kind: 'official_api',
    website_url: '', icon_data: '', icon_source: '',
    summary: '', description: '', docs_url: '',
  }
}
function group(id: string, supplierID: string, name: string): Group {
  return { id, supplier_id: supplierID, name, description: '', status_url: '', model_ids: [] }
}

const suppliers = [supplier('s1', '供应商一', ['g1', 'g2']), supplier('s2', '供应商二', ['g3', 'g4'])]
const groups = [group('g1', 's1', '分组一'), group('g2', 's1', '分组二'), group('g3', 's2', '分组三'), group('g4', 's2', '分组四')]

let host: HTMLDivElement
let root: Root
let latest: ParentSelection

function Harness() {
  const [selection, setSelection] = useState<ParentSelection>({ suppliers: new Set(), groups: new Set() })
  const change = (next: ParentSelection) => {
    latest = next
    setSelection(next)
  }
  return <ParentFilter groups={groups} onChange={change} selection={selection} suppliers={suppliers} />
}

function row(text: string) {
  return [...host.querySelectorAll('.parent-filter-row')].find((node) => node.textContent === text) as HTMLElement
}
function checkbox(text: string) {
  return row(text).querySelector('input') as HTMLInputElement
}
async function click(node: HTMLElement) {
  await act(async () => node.click())
}
async function open() {
  await click(host.querySelector('.parent-filter-trigger') as HTMLElement)
}

beforeEach(async () => {
  host = document.createElement('div')
  document.body.append(host)
  root = createRoot(host)
  await act(async () => root.render(<Harness />))
})
afterEach(async () => {
  await act(async () => root.unmount())
  host.remove()
})

it('opens with the first supplier groups and supports whole-supplier selection', async () => {
  await open()
  expect(host.querySelector('.parent-filter-panel')).not.toBeNull()
  expect(host.textContent).toContain('分组一')
  expect(host.textContent).not.toContain('分组三')

  await click(checkbox('供应商一'))
  expect([...latest.suppliers]).toEqual(['s1'])
  expect([...latest.groups]).toEqual([])
  expect(checkbox('分组一').checked).toBe(true)
  expect(checkbox('分组一').indeterminate).toBe(false)

  await click(checkbox('分组二'))
  expect([...latest.suppliers]).toEqual([])
  expect([...latest.groups]).toEqual(['g1'])
  expect(checkbox('供应商一').indeterminate).toBe(true)
})

it('shows the hovered/focused supplier groups and keeps partial groups', async () => {
  await open()
  await act(async () => checkbox('供应商二').focus())
  const headings = [...host.querySelectorAll('.parent-filter-heading')].map((node) => node.textContent)
  expect(headings[1]).toBe('供应商二')
  expect(host.textContent).toContain('分组三')
  expect(host.textContent).not.toContain('分组一')

  await click(checkbox('分组三'))
  expect([...latest.suppliers]).toEqual([])
  expect([...latest.groups]).toEqual(['g3'])
})

it('clears every parent filter and closes on Escape with focus back on the trigger', async () => {
  await open()
  await click(checkbox('供应商一'))
  await click(checkbox('全部供应商'))
  expect(latest.suppliers.size).toBe(0)
  expect(latest.groups.size).toBe(0)

  const trigger = host.querySelector('.parent-filter-trigger') as HTMLButtonElement
  await act(async () => {
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
  })
  expect(host.querySelector('.parent-filter-panel')).toBeNull()
  expect(document.activeElement).toBe(trigger)
})
