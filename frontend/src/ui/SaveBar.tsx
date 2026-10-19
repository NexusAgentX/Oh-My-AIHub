import { Button } from './Button'

/** 每条保存状态只描述当前表单；其它对象在自己的编辑器中提交。 */
export function SaveBar({ dirtyCount, busy, disabled, onSave, onDiscard, savedAt }: { dirtyCount: number; busy?: boolean; disabled?: boolean; onSave?: () => void; onDiscard: () => void; savedAt?: string }) {
  return <div className="save-bar"><span role="status">{dirtyCount ? `${dirtyCount} 处未保存` : savedAt ? `已保存 · ${savedAt}` : '没有未保存修改'}</span><div className="save-bar-actions"><Button type="button" variant="quiet" disabled={!dirtyCount || busy} onClick={onDiscard}>放弃</Button><Button type={onSave ? 'button' : 'submit'} onClick={onSave} loading={busy} disabled={!dirtyCount || disabled}>保存</Button></div></div>
}
