import { describe, expect, it } from 'vitest'
import {
  findMobileTab,
  findNavItem,
  flattenNavigation,
  mobileTabs,
  pageTitle,
  productNavigation,
} from './navigation'

describe('navigation', () => {
  it('gives every sidebar item a unique icon and route', () => {
    const items = flattenNavigation(productNavigation)
    expect(new Set(items.map((item) => item.icon)).size).toBe(items.length)
    expect(new Set(items.map((item) => item.to)).size).toBe(items.length)
  })

  it('groups the sidebar including forum navigation', () => {
    expect(productNavigation.map((group) => group.label)).toEqual(['使用 API', '共享', '积分', '交流'])
    expect(productNavigation[0].items.map((item) => item.icon)).toEqual(['home', 'store', 'key', 'chart'])
  })

  it('has exactly five mobile tabs with unique icons', () => {
    expect(mobileTabs.map((tab) => tab.label)).toEqual(['首页', '模型', '渠道', '积分', '我的'])
    expect(new Set(mobileTabs.map((tab) => tab.icon)).size).toBe(5)
  })

  it('highlights the sidebar item for nested routes', () => {
    expect(findNavItem(productNavigation, '/home')?.label).toBe('首页')
    expect(findNavItem(productNavigation, '/models/gpt-5')?.label).toBe('模型')
    expect(findNavItem(productNavigation, '/channels/new')?.label).toBe('我的渠道')
    expect(findNavItem(productNavigation, '/points/trades/abc')?.label).toBe('积分')
    expect(findNavItem(productNavigation, '/account/password')).toBeUndefined()
    expect(findNavItem(productNavigation, '/modelsx')).toBeUndefined()
    expect(findNavItem(productNavigation, '/forum/tickets/new')?.label).toBe('工单')
    expect(findNavItem(productNavigation, '/forum/new')?.label).toBe('论坛')
  })

  it('maps API Key, 用量 and 账户设置 to the 我的 tab on mobile', () => {
    expect(findMobileTab(mobileTabs, '/keys')?.label).toBe('我的')
    expect(findMobileTab(mobileTabs, '/usage')?.label).toBe('我的')
    expect(findMobileTab(mobileTabs, '/account')?.label).toBe('我的')
    expect(findMobileTab(mobileTabs, '/me')?.label).toBe('我的')
    expect(findMobileTab(mobileTabs, '/forum/tickets')?.label).toBe('我的')
    expect(findMobileTab(mobileTabs, '/channels/abc')?.label).toBe('渠道')
    expect(findMobileTab(mobileTabs, '/points/trades/1')?.label).toBe('积分')
  })

  it('derives page titles', () => {
    expect(pageTitle('/keys')).toBe('API Key')
    expect(pageTitle('/account')).toBe('账户设置')
    expect(pageTitle('/me')).toBe('我的')
  })
})
