import { useMemo } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom'

export type UrlField<T> = { defaultValue: T; parse: (value: string) => T; format?: (value: T) => string }
export const stringField = (defaultValue = ''): UrlField<string> => ({ defaultValue, parse: value => value })
export const enumField = <T extends string>(values: readonly T[], defaultValue: T): UrlField<T> => ({
  defaultValue, parse: value => values.includes(value as T) ? value as T : defaultValue,
})
type Schema = Record<string, { defaultValue: unknown; parse: (value: string) => unknown }>
type State<S extends Schema> = { [K in keyof S]: S[K]['defaultValue'] }

/** 查看范围写入 URL；省略默认值，保留不属于本表单的参数。 */
export function useUrlState<S extends Schema>(schema: S) {
  const [search, setSearch] = useSearchParams()
  const raw = JSON.stringify(Object.keys(schema).map(key => search.get(key)))
  const value = useMemo(() => {
    const values = JSON.parse(raw) as (string | null)[]
    return Object.fromEntries(Object.entries(schema).map(([key, field], index) =>
      [key, values[index] === null ? field.defaultValue : field.parse(values[index])],
    )) as State<S>
  }, [schema, raw])
  const setValue = (patch: Partial<State<S>>) => setSearch(current => {
    const next = new URLSearchParams(current)
    for (const [key, value] of Object.entries(patch)) {
      const field = schema[key]
      if (!field) continue
      if (value == null || value === '' || Object.is(value, field.defaultValue)) next.delete(key)
      else next.set(key, String(value))
    }
    next.delete('cursor')
    return next
  }, { replace: true })
  return [value, setValue] as const
}

/** 自己打开的浮层可返回关闭；深链关闭留在当前宿主，避免跳出应用。 */
export function useQueryOverlay(param: string) {
  const location = useLocation()
  const navigate = useNavigate()
  const search = new URLSearchParams(location.search)
  const value = search.get(param)
  const setValue = (id: string | null) => {
    const next = new URLSearchParams(location.search)
    const origin = `${location.pathname}${location.search}`
    if (id) {
      next.set(param, id)
      navigate({ search: next.toString() }, { state: { ...location.state, overlayFrom: origin } })
    } else {
      next.delete(param)
      const target = `${location.pathname}${next.size ? `?${next}` : ''}`
      if (location.state?.overlayFrom === target) navigate(-1)
      else navigate(target, { replace: true, state: location.state?.from ? { from: location.state.from } : null })
    }
  }
  return [value, setValue] as const
}

export function useBack(to: string, label: string) {
  const { state } = useLocation()
  const from = state?.from
  return from && typeof from.to === 'string' && from.to.startsWith('/') && !from.to.startsWith('//')
    ? { to: from.to, label: typeof from.label === 'string' ? from.label : label }
    : { to, label }
}

export function useRouteClose(fallback: string) {
  const location = useLocation()
  const navigate = useNavigate()
  return () => {
    const origin = location.state?.overlayFrom
    if (typeof origin === 'string' && origin.startsWith('/') && !origin.startsWith('//')) navigate(-1)
    else navigate(`${fallback}${location.search}`, { replace: true })
  }
}
