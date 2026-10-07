import { describe, expect, it } from 'vitest'
import {
  adminNavigation,
  findNavItem,
  flattenNavigation,
  productNavigation,
} from './navigation'

describe('navigation', () => {
  it.each([
    ['product', productNavigation],
    ['admin', adminNavigation],
  ])('gives every %s item a unique icon and route', (_name, groups) => {
    const items = flattenNavigation(groups)
    expect(new Set(items.map((item) => item.icon)).size).toBe(items.length)
    expect(new Set(items.map((item) => item.to)).size).toBe(items.length)
  })

  it('does not share icons between product and admin navigation', () => {
    const icons = [...flattenNavigation(productNavigation), ...flattenNavigation(adminNavigation)].map(
      (item) => item.icon,
    )
    expect(new Set(icons).size).toBe(icons.length)
  })

  it('groups the product navigation as 使用 API / 共享渠道 / 积分', () => {
    expect(productNavigation.map((group) => group.label)).toEqual(['使用 API', '共享渠道', '积分'])
  })

  it('keeps the bottom tab bar short enough for 390px screens', () => {
    expect(flattenNavigation(productNavigation).filter((item) => item.tab)).toHaveLength(5)
    expect(flattenNavigation(adminNavigation).filter((item) => item.tab)).toHaveLength(4)
  })

  it('matches nested routes to their navigation item', () => {
    expect(findNavItem(productNavigation, '/keys/new')?.label).toBe('API Keys')
    expect(findNavItem(productNavigation, '/market/channels/abc')?.label).toBe('API 市场')
    expect(findNavItem(productNavigation, '/wallet/insufficient')?.label).toBe('钱包')
    expect(findNavItem(adminNavigation, '/admin/c2c/disputes/1')?.label).toBe('争议处理')
    expect(findNavItem(productNavigation, '/account')).toBeUndefined()
  })
})
