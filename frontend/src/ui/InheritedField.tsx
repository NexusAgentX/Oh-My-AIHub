import type { ComponentProps } from 'react'
import { TextField } from './Form'
import { Badge } from './Badge'
import { Button } from './Button'

export function InheritedField({ value, inherited, source = '平台默认', onRestore, ...props }: Omit<ComponentProps<typeof TextField>, 'value' | 'placeholder'> & { value: string | number | null; inherited: string; source?: string; onRestore: () => void }) {
  const custom = value !== null && value !== ''
  return <div className="inherited-field"><TextField {...props} value={value ?? ''} placeholder={inherited} /><div className="inherited-field-source"><Badge tone={custom ? 'info' : 'neutral'}>{custom ? '已覆盖' : source}</Badge>{custom && <Button size="sm" type="button" variant="quiet" onClick={onRestore}>恢复继承</Button>}</div></div>
}
