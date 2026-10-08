import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import type { PendingItem } from '../api/types'
import { PendingList } from './PendingItems'

const items: PendingItem[] = [
  {
    id: 'c2c-release-t1',
    kind: 'c2c_release',
    label: '待放行',
    tone: 'warning',
    title: '买家 已付款 ¥10.00',
    detail: '确认收款后放行 10 积分',
    to: '/c2c/trades/t1',
  },
  {
    id: 'channel-failed-c1',
    kind: 'channel_failed',
    label: '校验失败',
    tone: 'danger',
    title: '失败渠道',
    detail: 'GPT-5 · OpenAI Responses',
    to: '/channels/c1',
  },
]

describe('PendingList', () => {
  it('renders each backend item with its label, copy and link', () => {
    const markup = renderToStaticMarkup(
      <MemoryRouter>
        <PendingList items={items} />
      </MemoryRouter>,
    )
    expect(markup).toContain('待放行')
    expect(markup).toContain('买家 已付款 ¥10.00')
    expect(markup).toContain('确认收款后放行 10 积分')
    expect(markup).toContain('href="/c2c/trades/t1"')
    expect(markup).toContain('校验失败')
    expect(markup).toContain('href="/channels/c1"')
    expect(markup.match(/<li/g)).toHaveLength(2)
  })
})
