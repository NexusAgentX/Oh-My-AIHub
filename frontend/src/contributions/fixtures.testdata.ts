import type { components } from '../api/schema.gen'

const at = '2026-10-19T02:00:00Z'
export const draftID = '80000000-0000-4000-8000-000000000528'
export const targetID = '80000000-0000-4000-8000-000000000529'
export const quote: components['schemas']['ContributionQuote'] = { id: '80000000-0000-4000-8000-000000000530', group_model_id: targetID, version: 1, currency: 'CNY', billing_unit: 'per_million_tokens', prices: { input: '', output: '0', cache_write: '', cache_read: '', details: {} }, tiers: [], unit_prices: [], created_at: at }
const changes: components['schemas']['ContributionChanges'] = { suppliers: [], groups: [], addresses: [], group_addresses: [], governance: [], quotes: [quote], models: [{ base_revision: 1, value: { id: targetID, group_id: '80000000-0000-4000-8000-000000000531', model_id: 'demo-chat', display_name: '示例模型', upstream_model: '', model_type: 'text', context_window: null, max_output_tokens: null, notes: '', enabled: true, is_reference: false, quote_id: quote.id, revision: 1, capability_digest: 'synthetic', updated_at: at } }] }
export const contribution: components['schemas']['ContributionDetail'] = {
  draft: { id: draftID, owner_id: '80000000-0000-4000-8000-000000000532', title: '示例模型报价修订', kind: 'group_model', target_id: targetID, state: 'draft', revision: 1, changes, base: structuredClone(changes), report_ids: [], task_ids: [], pending_prices: [], review_reason: '', reviewed_by: null, submitted_at: null, published_at: null, created_at: at, updated_at: at },
  stage: 'report_required', evaluation: { ready: false, targets: [{ id: targetID, kind: 'group_model', model_id: 'demo-chat', state: 'report_required', report: null }] }, linked_channels: 2, addresses: [],
}
