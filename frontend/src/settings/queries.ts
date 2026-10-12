import { useQuery } from '@tanstack/react-query'
import { apiGet } from '../api/client'

export const formDefaultsKey = ['settings', 'form-defaults'] as const

export function useFormDefaults() {
  return useQuery({ queryKey: formDefaultsKey, queryFn: () => apiGet<'getFormDefaults'>('/api/form-defaults') })
}
