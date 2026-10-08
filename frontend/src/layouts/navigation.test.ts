import { describe, expect, it } from 'vitest'
import { findNavItem, flattenNavigation, productNavigation } from './navigation'

describe('navigation', () => {
  it('gives every item a unique icon and route', () => {
    const items = flattenNavigation(productNavigation)
    expect(new Set(items.map((item) => item.icon)).size).toBe(items.length)
    expect(new Set(items.map((item) => item.to)).size).toBe(items.length)
  })

  it('keeps the bottom tab bar short enough for 390px screens', () => {
    expect(flattenNavigation(productNavigation).filter((item) => item.tab).length).toBeLessThanOrEqual(5)
  })

  it('matches nested routes to their navigation item', () => {
    expect(findNavItem(productNavigation, '/home')?.label).toBe('首页')
    expect(findNavItem(productNavigation, '/account/password')).toBeUndefined()
  })
})
