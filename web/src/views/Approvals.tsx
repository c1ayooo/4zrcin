// 审批中心：项目内所有"等操作员点头"的事项的集中地。两族待办：
//   - 工具与命令：围栏或策略拦下的工具调用（含命令预设命中的下载/删除/上传），
//     经宿主的 Pending/Resolve 生命周期，批准只解除那一次调用；
//   - 资产待审：范围外被拒的主机，批准入围栏并恢复测试，拒绝标记不在范围。
// 审批散在概览收件箱、运行页和通信页侧栏时，操作员找不到"该去哪点"——这一页
// 不产生新的审批语义，只是把两个既有队列放进同一屏。
import { CheckCheck } from 'lucide-react'
import { Page, Panel } from '@/components/Page'
import { GuardrailCard } from '@/components/GuardrailCard'
import { FencePendingCard } from '@/components/FencePendingCard'

export function Approvals() {
  return (
    <Page
      title="审批"
      subtitle="集中处理本项目的待审批事项：被拦的工具与命令、范围外待审的资产。批准只解除被拦的那一次调用或那一台主机，不改变授权边界。"
      actions={<CheckCheck className="h-4 w-4 text-ink-3" aria-hidden="true" />}
    >
      <div className="space-y-3 pb-4">
        <Panel title="工具与命令" bodyClassName="p-3">
          <GuardrailCard autoLoad />
        </Panel>
        <Panel title="资产待审" bodyClassName="p-3">
          <FencePendingCard />
        </Panel>
      </div>
    </Page>
  )
}
