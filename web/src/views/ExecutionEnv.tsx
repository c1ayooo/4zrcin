// 执行环境 group (docs/34 §1.2): 节点 / 终端 / REPL / 工具与能力 / 代理 share one page.
// A container, not a rewrite: each child keeps its own <Page> header and logic.

import { TabBar, type TabItem } from '@/components/TabBar'
import { NodeProxyCard } from '@/components/NodeProxyCard'
import { useLoad } from '@/lib/hooks'
import { api } from '@/lib/api'
import { Nodes } from './Nodes'
import { ReplView, TerminalView } from './Terminal'
import { Shells } from './Shells'
import { Toolbox } from './Toolbox'

export type RuntimeTab = 'nodes' | 'terminal' | 'repl' | 'shells' | 'toolbox' | 'proxy'

const RUNTIME_TABS: readonly TabItem<RuntimeTab>[] = [
  { id: 'nodes', label: '节点' },
  { id: 'terminal', label: '终端' },
  { id: 'repl', label: 'REPL' },
  { id: 'shells', label: 'WebShell' },
  { id: 'toolbox', label: '工具与能力' },
  { id: 'proxy', label: '代理' },
]

export function ExecutionEnv({
  tab,
  onTab,
}: {
  tab: RuntimeTab
  onTab: (tab: RuntimeTab) => void
}) {
  // The proxy tab edits the node's scanner egress default, which rides the
  // instance settings view; fetch it once here so the card stays presentational.
  const settings = useLoad(() => api.settings(), [], settingsViewEqual)
  return (
    <div className="flex h-full min-h-0 min-w-0 flex-col bg-background">
      <TabBar tabs={RUNTIME_TABS} tab={tab} onTab={onTab} ariaLabel="执行环境" />
      <div className="isolate min-h-0 min-w-0 flex-1 overflow-y-auto">
        {tab === 'nodes' ? (
          <Nodes />
        ) : tab === 'terminal' ? (
          <TerminalView />
        ) : tab === 'repl' ? (
          <ReplView />
        ) : tab === 'shells' ? (
          <Shells />
        ) : tab === 'proxy' ? (
          <div className="mx-auto max-w-3xl px-4 py-4">
            <NodeProxyCard settings={settings} />
          </div>
        ) : (
          <Toolbox />
        )}
      </div>
    </div>
  )
}

// The settings view carries masked secrets and error text that changes on
// every fetch; comparing only the proxy projection keeps the card from
// re-rendering (and dropping operator input) on unrelated settings refreshes.
function settingsViewEqual(a: { node_proxy?: unknown }, b: { node_proxy?: unknown }): boolean {
  return JSON.stringify(a.node_proxy) === JSON.stringify(b.node_proxy)
}
