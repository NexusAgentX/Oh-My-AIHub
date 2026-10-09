import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiGet, apiSend, pathSegment, request } from '../api/client'
import type { RequestBody, ResponseBody } from '../api/types'
import type { Kind } from './model'

const topicURL = (id: string) => '/api/forum/topics/' + pathSegment(id)
const replyURL = (id: string) => '/api/forum/replies/' + pathSegment(id)
const boardURL = (id: string) => '/api/forum/boards/' + pathSegment(id)
export const forumApi = {
  createTopic: (body: RequestBody<'createForumTopic'>) => apiSend<'createForumTopic'>('POST', '/api/forum/topics', body),
  updateTopic: ({ id, body }: { id: string; body: RequestBody<'updateForumTopic'> }) => apiSend<'updateForumTopic'>('PATCH', topicURL(id), body),
  deleteTopic: (id: string) => apiSend<'deleteForumTopic'>('DELETE', topicURL(id)),
  status: ({ id, status }: { id: string; status: RequestBody<'updateForumTopicStatus'>['status'] }) => apiSend<'updateForumTopicStatus'>('PATCH', topicURL(id) + '/status', { status }),
  createReply: ({ id, body }: { id: string; body: RequestBody<'createForumReply'> }) => apiSend<'createForumReply'>('POST', topicURL(id) + '/replies', body),
  updateReply: ({ id, body }: { id: string; body: RequestBody<'updateForumReply'> }) => apiSend<'updateForumReply'>('PATCH', replyURL(id), body),
  deleteReply: (id: string) => apiSend<'deleteForumReply'>('DELETE', replyURL(id)),
  createBoard: (body: RequestBody<'createForumBoard'>) => apiSend<'createForumBoard'>('POST', '/api/forum/boards', body),
  updateBoard: ({ id, body }: { id: string; body: RequestBody<'updateForumBoard'> }) => apiSend<'updateForumBoard'>('PATCH', boardURL(id), body),
  deleteBoard: (id: string) => apiSend<'deleteForumBoard'>('DELETE', boardURL(id)),
  upload(file: File) {
    const body = new FormData()
    body.append('file', file)
    return request<ResponseBody<'uploadForumAttachment'>>('/api/forum/attachments', { method: 'POST', body })
  },
}
export function useBoards() {
  return useQuery({ queryKey: ['forum', 'boards'], queryFn: () => apiGet<'listForumBoards'>('/api/forum/boards') })
}
export function useTopics(kind: Kind, page: number, q: string, boardID: string) {
  return useQuery({ queryKey: ['forum', 'topics', kind, page, q, boardID], queryFn: () => apiGet<'listForumTopics'>('/api/forum/topics', { kind, page, q, board_id: boardID, limit: 20 }) })
}
export function useTopic(id: string) {
  return useQuery({ queryKey: ['forum', 'topic', id], queryFn: () => apiGet<'getForumTopic'>(topicURL(id)), enabled: Boolean(id) })
}
export function useReplies(id: string, page: number) {
  return useQuery({ queryKey: ['forum', 'replies', id, page], queryFn: () => apiGet<'listForumReplies'>(topicURL(id) + '/replies', { page, limit: 20 }), enabled: Boolean(id) })
}
export function useForumAction<T, R>(mutationFn: (input: T) => Promise<R>) {
  const client = useQueryClient()
  return useMutation({ mutationFn, onSuccess: () => client.invalidateQueries({ queryKey: ['forum'] }) })
}
