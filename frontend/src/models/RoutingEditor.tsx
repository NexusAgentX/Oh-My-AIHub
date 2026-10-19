import { Link } from 'react-router-dom'
import { useFormDefaults } from '../settings/queries'
import { useId, useState, type DragEvent, type KeyboardEvent } from 'react'
import type { ModelRoutingChannel, RoutingMode } from '../api/types'
import { FormatTags, routingModeLabels } from '../calls'
import { formatMultiplier } from '../money/format'
import { formatTypicalPrice } from '../typical/format'
import { RoutingMetrics } from './RoutingMetrics'
import { Badge, Disclosure, Icon, Segmented, TextField } from '../ui'
import {
  advancedChanged,
  changeMode,
  channelStateLabel,
  moveChannel,
  moveTo,
  orderedChannels,
  setUsed,
  type RoutingDraft,
} from './routing'

const modeOptions = (Object.keys(routingModeLabels) as RoutingMode[]).map((key) => ({
  key,
  label: routingModeLabels[key],
}))
type RoutingChannel = ModelRoutingChannel

function parseOptionalInt(value: string) {
  if (value.trim() === '') return null
  const number = Number(value)
  return Number.isInteger(number) && number > 0 ? number : null
}

/**
 * 调用方式与渠道顺序编辑器：模型详情（账号级）与 Key 抽屉（Key 级）共用。
 * 手动模式显示拖拽手柄，手柄获得焦点后可用上下方向键移动。
 */
export function RoutingEditor({
  channels,
  draft,
  onChange,
  scopeHint,
}: {
  channels: RoutingChannel[]
  draft: RoutingDraft
  onChange: (draft: RoutingDraft) => void
  scopeHint?: string
}) {
  const { data: defaults, isError } = useFormDefaults()
  const pending = isError ? '默认值加载失败' : '加载默认值…'
  const [dragging, setDragging] = useState<string | null>(null)
  const [announcement, setAnnouncement] = useState('')
  const hintID = useId()
  const manual = draft.mode === 'manual'
  const rows = orderedChannels(channels, draft)

  const onHandleKey = (event: KeyboardEvent, channel: RoutingChannel) => {
    const delta = event.key === 'ArrowUp' ? -1 : event.key === 'ArrowDown' ? 1 : 0
    if (!delta) return
    event.preventDefault()
    const order = moveChannel(draft.order, channel.id, delta)
    if (order === draft.order) return
    onChange({ ...draft, order })
    setAnnouncement(`${channel.name} 移到第 ${order.indexOf(channel.id) + 1} 位`)
    // 重新渲染后保持焦点在同一手柄上
    window.requestAnimationFrame(() => {
      document.querySelector<HTMLButtonElement>(`[data-handle="${channel.id}"]`)?.focus()
    })
  }
  const onDrop = (event: DragEvent, target: RoutingChannel) => {
    event.preventDefault()
    if (!dragging || dragging === target.id) return
    onChange({ ...draft, order: moveTo(draft.order, dragging, draft.order.indexOf(target.id)) })
    setDragging(null)
  }

  return (
    <div className="routing-editor">
      <div className="routing-mode">
        <Segmented
          label="调用方式"
          onChange={(mode) => onChange(changeMode(draft, mode, channels))}
          options={modeOptions}
          value={draft.mode}
        />
        {scopeHint && <span className="muted-copy" id={hintID}>{scopeHint}</span>}
      </div>
      {rows.length === 0 ? (
        <p className="muted-copy">暂无在线渠道</p>
      ) : (
        <ol className={`routing-list ${manual ? 'routing-list-manual' : ''}`} aria-label="渠道">
          {rows.map((channel, index) => {
            const used = !draft.excluded.includes(channel.id)
            const state = channelStateLabel(channel)
            return (
              <li
                className={`routing-row ${used ? '' : 'routing-row-off'} ${dragging === channel.id ? 'routing-row-dragging' : ''}`}
                draggable={manual}
                key={channel.id}
                onDragEnd={() => setDragging(null)}
                onDragOver={(event) => manual && event.preventDefault()}
                onDragStart={(event) => {
                  setDragging(channel.id)
                  event.dataTransfer.effectAllowed = 'move'
                }}
                onDrop={(event) => onDrop(event, channel)}
              >
                <label className="routing-use">
                  <input
                    aria-label={`使用 ${channel.name}`}
                    checked={used}
                    onChange={(event) => onChange(setUsed(draft, channel.id, event.target.checked))}
                    type="checkbox"
                  />
                </label>
                {manual && (
                  <button
                    aria-label={`排序 ${channel.name}，第 ${index + 1} 位，用上下方向键移动`}
                    className="routing-handle"
                    data-handle={channel.id}
                    onKeyDown={(event) => onHandleKey(event, channel)}
                    type="button"
                  >
                    <Icon name="grip" />
                  </button>
                )}
                <div className="routing-name">
                  <Link draggable={false} to={`/channel-hall/${channel.id}`}>{channel.name}</Link>
                  <span className="routing-owner muted-copy">
                    {channel.is_mine ? '由你提供' : '共享渠道'}
                  </span>
                  <span className="routing-badges">
                    {channel.is_mine && <Badge tone="accent">我的 · 免手续费</Badge>}
                    <Badge tone={state.tone}>{state.label}</Badge>
                  </span>
                  <FormatTags formats={channel.formats} />
                </div>
                <dl className="routing-metrics">
                  <div>
                    <dt>分组报价折扣</dt>
                    <dd>
                      {formatMultiplier(channel.multiplier)}
                    </dd>
                  </div>
                  <div>
                    <dt>典型费用</dt>
                    <dd>
                      {formatTypicalPrice(channel.typical_price)}
                    </dd>
                  </div>
                </dl>
                <RoutingMetrics rows={channel.routing_metrics} />
              </li>
            )
          })}
        </ol>
      )}
      <span aria-live="polite" className="visually-hidden">
        {announcement}
      </span>
      <Disclosure changed={advancedChanged(draft)} title="高级">
        <p className="muted-copy">超时留空使用平台默认；渠道明确设置时优先使用渠道值。</p>
        <div className="field-row">
          <TextField
            hint="留空使用平台默认"
            inputMode="numeric"
            label="最多尝试几个渠道"
            placeholder={defaults ? String(defaults.default_max_attempts) : pending}
            min={1}
            onChange={(event) => onChange({ ...draft, max_attempts: parseOptionalInt(event.target.value) })}
            type="number"
            value={draft.max_attempts ?? ''}
          />
          {([
            ['ttft_timeout_ms', '首字超时（毫秒）', '流式：等待响应体首字节', 600000],
            ['idle_timeout_ms', '空闲超时（毫秒）', '流式：两次数据之间的最长间隔', 3600000],
            ['request_timeout_ms', '请求超时（毫秒）', '非流式：从请求到读完整个响应', 3600000],
          ] as const).map(([key, label, hint, max]) => (
            <TextField
              hint={hint}
              inputMode="numeric"
              key={key}
              label={label}
              min={1000}
              max={max}
              onChange={(event) => onChange({ ...draft, [key]: parseOptionalInt(event.target.value) })}
              placeholder="平台默认"
              type="number"
              value={draft[key] ?? ''}
            />
          ))}
        </div>
      </Disclosure>
    </div>
  )
}
