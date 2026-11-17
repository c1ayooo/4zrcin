import { MessageSquare, X } from 'lucide-react'
import { Button } from '@cyber/ui'
import { DockResizeHandle, type DockSize } from './graph/DockResize'

/** Communication has one home; the graph keeps a navigation entry. */
export function GraphChatDock({dock,onClose,onOpenConversation}:{dock:DockSize;onClose:()=>void;onOpenConversation:()=>void}) {
  return <div data-graph-chat className="absolute right-3 top-[56px] z-20 max-w-[calc(100vw-1.5rem)] rounded-xl border border-hairline bg-card p-4 shadow-elevated" style={{width:dock.width}}>
    <DockResizeHandle dock={dock}/>
    <div className="flex items-center gap-2"><MessageSquare className="h-4 w-4"/><span className="tw-section-title">通信</span><Button className="ml-auto" size="icon-sm" variant="ghost" onClick={onClose} aria-label="关闭通信入口"><X className="h-4 w-4"/></Button></div>
    <p className="my-3 text-sm text-ink-3">在通信页查看对话、按角色与执行者筛选，并下发一次性提示或持久指令。</p>
    <Button onClick={onOpenConversation}>打开通信页</Button>
  </div>
}
