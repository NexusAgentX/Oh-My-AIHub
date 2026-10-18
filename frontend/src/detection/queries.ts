import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiGet, apiSend } from '../api/client'
import type { components } from '../api/schema.gen'
export type Job = components['schemas']['DetectionJob']
export type SearchSettings = components['schemas']['DetectionSearchSettings']
export const detectionKeys = { all: ['detection'] as const, list: (cursor: string) => ['detection', 'jobs', cursor] as const, job: (id: string) => ['detection', 'job', id] as const, parameters: ['detection', 'parameters'] as const }
export function useDetectionJobs(cursor: string) {
  return useQuery({ queryKey: detectionKeys.list(cursor), queryFn: () => apiGet<'listDetectionJobs'>('/api/detection/jobs', { cursor }) })
}
export function useDetectionJob(id: string) {
  return useQuery({ queryKey: detectionKeys.job(id), queryFn: () => apiGet<'getDetectionJob'>(`/api/detection/jobs/${encodeURIComponent(id)}`), refetchInterval: (query) => query.state.data?.job.execution_active ? 2000 : false })
}
export function useDetectionArtifacts(id: string, updatedAt: string, active: boolean, enabled = true) {
  return useQuery({ queryKey: ['detection', 'artifacts', id, updatedAt],enabled,refetchInterval: active ? 2000 : false, queryFn: () => apiGet<'listDetectionArtifacts'>(`/api/detection/jobs/${encodeURIComponent(id)}/artifacts`) })
}
export function useDetectionAction() {
  const client = useQueryClient()
  return useMutation({ mutationFn: async ({ id, action, key, scenarios, target }: { id?: string; action: 'start' | 'cancel' | 'resume' | 'retest'; key?: string; scenarios?: string[]; target?: { draft_id?:string; target_id: string; target_kind: 'group' | 'group_model' } }) => {
    const path = `/api/detection/jobs/${encodeURIComponent(id ?? '')}`
    if (action === 'start' && target) return apiSend<'startDetectionJob'>('POST', '/api/detection/jobs', { ...target, api_key: key ?? '' })
    if (action === 'cancel') return apiSend<'cancelDetectionJob'>('POST', `${path}/cancel`)
    if (action === 'retest') return apiSend<'retestDetectionJob'>('POST', `${path}/retest`, { api_key: key ?? '', scenarios: scenarios ?? [] })
    return apiSend<'resumeDetectionJob'>('POST', `${path}/resume`, { api_key: key ?? '' })
  }, onSettled: (_data, _error, variables) => { variables.key = undefined }, onSuccess: () => client.invalidateQueries({ queryKey: detectionKeys.all }) })
}
export function useDetectionParameters() { return useQuery({ queryKey: detectionKeys.parameters, queryFn: () => apiGet<'getDetectionParameters'>('/api/admin/detection/search') }) }
export function useUpdateDetectionSearch() {
  const client = useQueryClient()
  return useMutation({ mutationFn: ({ target, value }: { target: 'context' | 'max_output'; value: SearchSettings }) => apiSend<'updateDetectionSearch'>('PUT', `/api/admin/detection/search/${target}`, value), onSuccess: () => client.invalidateQueries({ queryKey: detectionKeys.parameters }) })
}

export function useSavedDetectionReport(id: string) { return useQuery({ queryKey: ['detection', 'saved-report', id], queryFn: () => apiGet<'getDetectionReport'>(`/api/detection/reports/${encodeURIComponent(id)}`) }) }
export function useDetectionReports(id: string, cursor: string, version: string) { return useQuery({ queryKey: ['detection', 'reports', id, cursor,version], queryFn: () => apiGet<'listDetectionReports'>(`/api/detection/jobs/${encodeURIComponent(id)}/reports`, { cursor }) }) }
