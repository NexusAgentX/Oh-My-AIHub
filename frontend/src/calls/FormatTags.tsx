import type { Format } from '../api/types'
import { Icon } from '../ui'
import { formatLabels, formats as allFormats } from './labels'

/** 格式标签行（只读）；passed 中的格式显示绿色 ✓。 */
export function FormatTags({ formats, passed }: { formats: Format[]; passed?: Format[] }) {
  if (formats.length === 0) return <span className="muted">—</span>
  return (
    <span className="format-tags">
      {allFormats
        .filter((format) => formats.includes(format))
        .map((format) => {
          const ok = passed?.includes(format)
          return (
            <span className={`format-tag ${ok ? 'format-tag-ok' : ''}`} key={format}>
              {ok && <Icon name="check" size={12} />}
              {formatLabels[format]}
            </span>
          )
        })}
    </span>
  )
}
