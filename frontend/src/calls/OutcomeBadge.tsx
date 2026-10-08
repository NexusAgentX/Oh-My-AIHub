import type { CallOutcome } from '../api/types'
import { Badge } from '../ui'
import { outcomeLabels, outcomeTone } from './labels'

export function OutcomeBadge({ outcome }: { outcome: CallOutcome }) {
  return <Badge tone={outcomeTone(outcome)}>{outcomeLabels[outcome]}</Badge>
}
