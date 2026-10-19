import { describe, expect, it } from 'vitest'
import {
  findMobileTab,
  findNavItem,
  flattenNavigation,
  mobileTabs,
  productNavigation,
} from './navigation'

describe('navigation', () => {
  it('gives every sidebar item a unique icon and route', () => {
    const items = flattenNavigation(productNavigation)
    expect(new Set(items.map((item) => item.icon)).size).toBe(items.length)
    expect(new Set(items.map((item) => item.to)).size).toBe(items.length)
  })

  it('groups the sidebar including forum navigation', () => {
    expect(productNavigation.map((group) => group.label)).toEqual(['', '使用', '市场', '积分', '供应', '社区'])
    expect(productNavigation.find(group => group.label === '使用')?.items.map(item => item.to)).toEqual(['/keys', '/calls'])
  })

  it('has exactly five mobile tabs with unique icons', () => {
    expect(mobileTabs.map((tab) => tab.label)).toEqual(['首页', '使用', '市场', '积分', '供应'])
    expect(new Set(mobileTabs.map((tab) => tab.icon)).size).toBe(5)
  })

  it('highlights the sidebar item for nested routes', () => {
    expect(findNavItem(productNavigation, '/home')?.label).toBe('首页')
    expect(findNavItem(productNavigation, '/models/gpt-5')?.label).toBe('模型')
    expect(findNavItem(productNavigation, '/supply/channels/new')?.label).toBe('我的渠道')
    expect(findNavItem(productNavigation, '/points/trades/abc')?.label).toBe('积分')
    expect(findNavItem(productNavigation, '/account/password')).toBeUndefined()
    expect(findNavItem(productNavigation, '/modelsx')).toBeUndefined()
    expect(findNavItem(productNavigation, '/tickets/new')?.label).toBe('工单')
    expect(findNavItem(productNavigation, '/forum/new')?.label).toBe('论坛')
  })

  it('maps usage and supply objects to their domains and keeps account outside the bottom tabs', () => {
    expect(findMobileTab(mobileTabs, '/keys')?.label).toBe('使用')
    expect(findMobileTab(mobileTabs, '/calls/insights')?.label).toBe('使用')
    expect(findMobileTab(mobileTabs, '/account')).toBeUndefined()
    expect(findMobileTab(mobileTabs, '/me')).toBeUndefined()
    expect(findMobileTab(mobileTabs, '/tickets')).toBeUndefined()
    expect(findMobileTab(mobileTabs, '/supply/channels/abc')?.label).toBe('供应')
    expect(findMobileTab(mobileTabs, '/points/trades/1')?.label).toBe('积分')
  })
})
