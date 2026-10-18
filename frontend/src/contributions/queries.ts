import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiGet, apiSend } from '../api/client'
import type { components } from '../api/schema.gen'
export type Draft = components['schemas']['ContributionDraft']
export type Changes = Draft['changes']
export type Kind = Draft['kind']
export type NewInput = components['schemas']['ContributionNew']
export function useContributions(queue: boolean, cursor: string) {
  return useQuery({
    queryKey: ['contributions', 'list', queue, cursor],
    queryFn: () =>
      queue
        ? apiGet<'reviewContributions'>('/api/admin/contributions', { cursor })
        : apiGet<'listContributions'>('/api/contributions', { cursor }),
  })
}
export function useContribution(id: string) {
  return useQuery({
    queryKey: ['contributions', 'detail', id],
    queryFn: () =>
      apiGet<'getContribution'>(`/api/contributions/${encodeURIComponent(id)}`),
  })
}
export function useTargets(
  kind: 'supplier' | 'group' | 'group_model' | 'address',
  cursor: string,
  enabled = true,
  parentID?: string,
) {
  return useQuery({
    enabled,
    queryKey: ['contributions', 'targets', kind, cursor, parentID],
    queryFn: () =>
      apiGet<'contributionTargets'>('/api/contributions/targets', {
        kind,
        cursor,
        parent_id: parentID,
      }),
  })
}
export function useNewContribution() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (value: NewInput) =>
      apiSend<'newContribution'>('POST', '/api/contributions', value),
    onSuccess: () => client.invalidateQueries({ queryKey: ['contributions'] }),
  })
}
export function useSaveContribution(id: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (value: components['schemas']['ContributionSave']) =>
      apiSend<'saveContribution'>(
        'PUT',
        `/api/contributions/${encodeURIComponent(id)}`,
        value,
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ['contributions'] }),
  })
}
export function useContributionAction(id: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: ({
      action,
      revision,
      reason,
    }: {
      action: 'submit' | 'withdraw' | 'approve' | 'reject'
      revision: number
      reason?: string
    }) => {
      const base = `/api/${action === 'approve' || action === 'reject' ? 'admin/' : ''}contributions/${encodeURIComponent(id)}`
      const body = { revision, reason }
      switch (action) {
        case 'submit':
          return apiSend<'submitContribution'>('POST', `${base}/submit`, body)
        case 'withdraw':
          return apiSend<'withdrawContribution'>(
            'POST',
            `${base}/withdraw`,
            body,
          )
        case 'approve':
          return apiSend<'approveContribution'>('POST', `${base}/approve`, body)
        case 'reject':
          return apiSend<'rejectContribution'>('POST', `${base}/reject`, body)
      }
    },
    onSettled: () => Promise.all([client.invalidateQueries({queryKey:['contributions']}),client.invalidateQueries({queryKey:['market']}),client.invalidateQueries({queryKey:['group-channels']})]),
  })
}
export function useAddContributionChild(id: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (value: components['schemas']['ContributionChild']) =>
      apiSend<'addContributionChild'>(
        'POST',
        `/api/contributions/${encodeURIComponent(id)}/children`,
        value,
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ['contributions'] }),
  })
}
export function useContributionIcon() {
  return useMutation({
    mutationFn: (website_url: string) =>
      apiSend<'fetchContributionIcon'>('POST', '/api/contributions/icon', {
        website_url,
      }),
  })
}
export function useContributionReportChoices(taskID: string, cursor: string) {
  return useQuery({
    queryKey: ['contributions', 'reports', taskID, cursor],
    enabled: !!taskID,
    staleTime: 0,
    queryFn: () =>
      apiGet<'listDetectionReports'>(
        `/api/detection/jobs/${encodeURIComponent(taskID)}/reports`,
        { cursor },
      ),
  })
}

export function useReuseContributionAddress(id: string) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: (value: components['schemas']['ContributionReuseAddress']) =>
      apiSend<'reuseContributionAddress'>(
        'POST',
        `/api/contributions/${encodeURIComponent(id)}/addresses`,
        value,
      ),
    onSuccess: () => client.invalidateQueries({ queryKey: ['contributions'] }),
  })
}
