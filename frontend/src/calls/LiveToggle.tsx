import { Button, Switch } from '../ui'
import type { StreamStatus } from './useCallStream'

/** 「实时」开关与连接状态；连接失败时给出重试。 */
export function LiveToggle({
  enabled,
  onChange,
  status,
  onRetry,
  disabledReason,
}: {
  enabled: boolean
  onChange: (enabled: boolean) => void
  status: StreamStatus
  onRetry: () => void
  disabledReason?: string
}) {
  return (
    <span className="live-toggle">
      <Switch checked={enabled} label="实时" onChange={onChange} disabled={Boolean(disabledReason)} />
      {disabledReason && <span className="muted" title={disabledReason}>{disabledReason}</span>}
      {enabled && status === 'open' && <span className="live-dot" role="status">已连接</span>}
      {enabled && status === 'connecting' && <span className="muted" role="status">连接中</span>}
      {enabled && status === 'error' && (
        <span className="live-error" role="alert">
          实时连接失败
          <Button onClick={onRetry} size="sm" type="button" variant="quiet">
            重试
          </Button>
        </span>
      )}
    </span>
  )
}
