import { useCallback, useEffect, useState } from 'react'
import { Badge, Callout, Skeleton } from '@cyber/ui'
import { Panel } from '@/components/Page'
import { api } from '@/lib/api'
import { AuditTrail } from '@/components/AuditTrail'
import { ScopeRulesCard } from '@/components/ScopeRulesCard'
import type { ProjectInfo, ProjectPolicyView } from '@/lib/types'

/** The derived fence mode, as the operator reads it. */
const FENCE_LABELS: Record<ProjectPolicyView['fence_mode'], { label: string; hint: string; tone: 'default' | 'warning' | 'destructive' | 'secondary' }> = {
  unfenced: {
    label: '未设围栏 · 仅记录',
    hint: '所有流量只记录不拦截。高危命令仍始终审计；随时可以在下方配置围栏。',
    tone: 'secondary',
  },
  shadow: {
    label: '试运行（shadow）',
    hint: '按规则判定并记录结论，但不实际拦截——适合新规则上线前先看看会怎么判。',
    tone: 'warning',
  },
  enforced: {
    label: '强制执行（白名单）',
    hint: '规则行内放行并记录；范围外请求拒绝并入库，目标进入待审——批准后自动入围栏。',
    tone: 'destructive',
  },
  draft: {
    label: '规则已存为草稿',
    hint: '规则表已保存但未启用：管线只记录不拦。启用开关在下方规则编辑器里。',
    tone: 'secondary',
  },
}

/**
 * The in-project 集中配置入口 (docs/03 §5): fence status and the rule editor
 * in one place, the audit guarantee stated as a fact, and the host-level
 * capture projection. Saving inside the editor refreshes the aggregate so the
 * fence badge never outlives the rules it summarizes.
 */
export function PolicyPanel({ project, onDirty }: {
  project: ProjectInfo
  onDirty?: (id: string, dirty: boolean) => void
}) {
  const [policy, setPolicy] = useState<ProjectPolicyView | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const reload = useCallback(async () => {
    try {
      const view = await api.projectPolicy(project.id)
      setPolicy(view)
      setError('')
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }, [project.id])

  useEffect(() => {
    void reload()
  }, [reload])

  const fence = policy ? FENCE_LABELS[policy.fence_mode] : null

  return (
    <div className="space-y-4">
      {error && <Callout tone="destructive">{error}</Callout>}
      {loading && !policy && <Skeleton className="h-36 w-full rounded-xl" />}

      {fence && (
        <Panel title="范围与拦截">
          <div className="mb-3 flex flex-wrap items-center gap-2">
            <Badge variant={fence.tone}>{fence.label}</Badge>
            <span className="text-[12px] text-ink-3">{fence.hint}</span>
          </div>
          <ScopeRulesCard
            project={project}
            onSaved={reload}
            onDirty={onDirty ?? (() => {})}
          />
        </Panel>
      )}

      {policy?.audit.guaranteed && (
        <Panel title="命令审计（保底）">
          <p className="mb-2 text-[12px] text-ink-3">执行端内置规则，始终开启，不可被项目配置关闭。</p>
          <Callout tone="info">
            高危命令（破坏类、凭据外传类等）始终审计并入库，reason 带内置标记。项目策略只能把它升级为
            挂审/拒绝，不能降低——关掉围栏只放行流量，不影响审计强度。待审批的工具调用在
            「访问与交接」收件箱处理。
          </Callout>
          <div className="mt-3">
            <AuditTrail projectId={project.id} />
          </div>
        </Panel>
      )}

      {policy?.capture && (
        <Panel title="流量捕获（主机级）">
          <p className="mb-2 text-[12px] text-ink-3">
            一个捕获实例服务全部项目；修改请到「设置 → 流量捕获」。
          </p>
          <dl className="grid gap-x-6 gap-y-1.5 text-[12px] sm:grid-cols-[9rem_1fr]">
            <dt className="text-ink-3">捕获模式</dt>
            <dd className="text-ink">
              {policy.capture.capture_mode || 'relay'}（relay 透传时不做 MITM，只记录元数据）
            </dd>
            <dt className="text-ink-3">HTTPS 解密</dt>
            <dd className="text-ink">{policy.capture.decrypt_https ? '开启' : '关闭'}</dd>
            {policy.capture.ca_fingerprint && (
              <>
                <dt className="text-ink-3">CA 指纹</dt>
                <dd className="break-all font-mono text-[11px] text-ink">{policy.capture.ca_fingerprint}</dd>
              </>
            )}
            {policy.capture.reason && (
              <>
                <dt className="text-ink-3">说明</dt>
                <dd className="text-ink-3">{policy.capture.reason}</dd>
              </>
            )}
          </dl>
        </Panel>
      )}
    </div>
  )
}
