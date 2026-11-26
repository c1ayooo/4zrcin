// Per-row actions of the nodes page (docs/14 §9): project placement, the
// project default, and destroy for tunnel nodes. Destroy asks the operator to
// type the node's name, because it deletes the node's files on a host the
// platform may never see again.
import { useState } from 'react'
import { MoreHorizontal } from 'lucide-react'
import {
  Button,
  Callout,
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
  Field,
  Input,
} from '@cyber/ui'
import { api } from '@/lib/api'
import type { NodeInfo } from '@/lib/types'

export interface RowMenuProps {
  node: NodeInfo
  /** The row's display name (the runtime row knows the enrollment name). */
  title: string
  /** The project's bound node ids and its dispatch default. */
  boundIds: string[]
  projectDefault: string
  onChanged: (notice: string) => void
}

function nextBinding(bound: string[], projectDefault: string, nodeID: string, enable: boolean, makeDefault: boolean) {
  const next = enable ? Array.from(new Set([...bound, nodeID])) : bound.filter((id) => id !== nodeID)
  const def = makeDefault && enable ? nodeID : next.includes(projectDefault) ? projectDefault : next[0] ?? ''
  return { next, def }
}

export function RowMenu({ node, title, boundIds, projectDefault, onChanged }: RowMenuProps) {
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [destroyOpen, setDestroyOpen] = useState(false)
  const [confirmName, setConfirmName] = useState('')

  const tunnel = Boolean(node.remote)
  const destroying = node.remote?.state === 'destroying'
  const destroyed = node.remote?.state === 'destroyed'
  const blocked = tunnel && node.remote?.dispatch_ready === false

  async function bind(enable: boolean, makeDefault = false) {
    setSaving(true)
    setError('')
    try {
      const { next, def } = nextBinding(boundIds, projectDefault, node.id, enable, makeDefault)
      await api.setNodes(next, def)
      onChanged(enable ? (makeDefault ? '已启用并设为项目默认' : '已启用到项目') : '已移出项目')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  async function forget() {
    setSaving(true)
    setError('')
    try {
      await api.forgetRemoteNode(nodeID)
      onChanged('已标记节点为已移除')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  async function destroy() {
    setSaving(true)
    setError('')
    try {
      await api.destroyRemoteNode(nodeID)
      setDestroyOpen(false)
      setConfirmName('')
      onChanged('已下发卸载指令：节点在线时几秒内执行，离线则下次回连时执行')
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  // Destroy targets the enrollment's node id; the list row may carry the
  // discovered scan_<id> form.
  const nodeID = node.remote ? node.id.replace(/^scan_/, '') : node.id
  const nodeName = title
  const canConfirm = confirmName.trim() === nodeName || confirmName.trim() === nodeID

  if (destroyed) {
    return <span className="text-[11px] text-ink-3">已销毁</span>
  }

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button size="xs" variant="ghost" disabled={saving} aria-label={`${nodeName} 的更多操作`}>
            <MoreHorizontal className="h-3.5 w-3.5" aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-44">
          <DropdownMenuItem disabled={saving || blocked} onSelect={() => void bind(!node.bound)}>
            {node.bound ? '移出项目' : '启用到项目'}
          </DropdownMenuItem>
          <DropdownMenuItem
            disabled={saving || blocked || !node.bound || node.project_default}
            onSelect={() => void bind(true, true)}
          >
            设为默认
          </DropdownMenuItem>
          {blocked && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuLabel className="text-[11px] leading-4 text-ink-3">
                隧道节点在文件合同上线前不能承接意图
              </DropdownMenuLabel>
            </>
          )}
          {tunnel && (
            <>
              <DropdownMenuSeparator />
              {destroying ? (
                <DropdownMenuItem disabled={saving} onSelect={() => void forget()}>
                  标记已移除
                </DropdownMenuItem>
              ) : (
                <DropdownMenuItem
                  disabled={saving}
                  className="text-destructive focus:text-destructive"
                  onSelect={() => setDestroyOpen(true)}
                >
                  销毁节点
                </DropdownMenuItem>
              )}
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={destroyOpen} onOpenChange={(open) => !saving && setDestroyOpen(open)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>销毁节点 {nodeName}</DialogTitle>
          </DialogHeader>
          <div className="space-y-3 text-sm leading-relaxed text-ink-2">
            <p>
              平台会吊销该节点的接入凭据、解除项目绑定，并向节点下发卸载指令：停止服务、删除节点上的可执行内容与安装目录。此操作不可恢复。
            </p>
            {error && <Callout tone="destructive">{error}</Callout>}
            <Field label={`输入节点名确认：${nodeName}`}>
              <Input
                value={confirmName}
                onChange={(e) => setConfirmName(e.target.value)}
                placeholder={nodeName}
                autoFocus
              />
            </Field>
          </div>
          <DialogFooter>
            <Button variant="outline" size="sm" disabled={saving} onClick={() => setDestroyOpen(false)}>
              取消
            </Button>
            <Button variant="destructive" size="sm" disabled={saving || !canConfirm} onClick={() => void destroy()}>
              {saving ? '下发中…' : '销毁节点'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
