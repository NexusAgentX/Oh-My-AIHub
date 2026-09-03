import { useEffect, useMemo, useState } from 'react'
import { api, ApiError } from '../api/client'
import type { APIKey, MarketOffer } from '../api/contracts'
import { ConfirmDialog } from '../channels/presentation'
import { InlineError } from '../ui/FormControls'

export function AddOfferToKeyDialog({
  offer,
  onCancel,
  onAdded,
}: {
  offer: MarketOffer | null
  onCancel: () => void
  onAdded: (key: APIKey) => void
}) {
  const [keys, setKeys] = useState<APIKey[]>([])
  const [selectedKeyID, setSelectedKeyID] = useState('')
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!offer) return
    let active = true
    setLoading(true)
    setError('')
    void api
      .apiKeys()
      .then((loaded) => {
        if (!active) return
        const available = loaded.filter((key) => key.status !== 'deleted')
        setKeys(available)
        setSelectedKeyID(
          available.find(
            (key) =>
              !key.pools.some((pool) =>
                pool.members.some((member) => member.offer_id === offer.offer_id),
              ),
          )?.id ?? '',
        )
      })
      .catch((caught) => {
        if (active) {
          setError(caught instanceof ApiError ? caught.message : 'API Key 加载失败')
        }
      })
      .finally(() => {
        if (active) setLoading(false)
      })
    return () => {
      active = false
    }
  }, [offer])

  const selectedKey = useMemo(
    () => keys.find((key) => key.id === selectedKeyID),
    [keys, selectedKeyID],
  )

  const add = async () => {
    if (!offer || !selectedKey) return
    setBusy(true)
    setError('')
    try {
      const updated = await api.addAPIKeyPoolMember(
        selectedKey.id,
        selectedKey.version,
        {
          model_id: offer.model_id,
          protocol: offer.protocol,
          offer_id: offer.offer_id,
          priority: 0,
        },
      )
      onAdded(updated)
    } catch (caught) {
      if (caught instanceof ApiError && caught.code === 'conflict') {
        try {
          const latest = await api.apiKey(selectedKey.id)
          setKeys((current) =>
            current.map((key) => (key.id === latest.id ? latest : key)),
          )
          if (
            latest.pools.some((pool) =>
              pool.members.some((member) => member.offer_id === offer.offer_id),
            )
          ) {
            onAdded(latest)
            return
          }
          setError('Key 配置已更新，请复核后再次加入')
        } catch {
          setError('Key 配置已更新，请关闭后重试')
        }
      } else {
        setError(caught instanceof ApiError ? caught.message : '加入模型池失败')
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <ConfirmDialog
      busy={busy || loading}
      confirmDisabled={!selectedKey}
      confirmLabel="加入模型池"
      description={offer ? `${offer.model_name} · ${offer.channel_display_name}` : undefined}
      onCancel={onCancel}
      onConfirm={() => void add()}
      open={Boolean(offer)}
      title="选择 API Key"
    >
      <InlineError>{error}</InlineError>
      <label className="field">
        <span className="field-label">API Key</span>
        <select
          className="input"
          disabled={loading}
          onChange={(event) => setSelectedKeyID(event.target.value)}
          value={selectedKeyID}
        >
          <option value="">{loading ? '正在加载' : '选择 Key'}</option>
          {keys.map((key) => {
            const included = Boolean(
              offer &&
                key.pools.some((pool) =>
                  pool.members.some((member) => member.offer_id === offer.offer_id),
                ),
            )
            return (
              <option disabled={included} key={key.id} value={key.id}>
                {key.display_name}{included ? ' · 已加入' : ''}
              </option>
            )
          })}
        </select>
      </label>
    </ConfirmDialog>
  )
}
