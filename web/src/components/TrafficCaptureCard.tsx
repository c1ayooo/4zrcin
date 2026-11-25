import { KeyRound, ShieldCheck, ShieldOff, TriangleAlert } from 'lucide-react'
import { Badge, Callout } from '@cyber/ui'
import { Panel } from '@/components/Page'
import type { TrafficCaptureSummary } from '@/lib/types'

/**
 * Read-only summary of the operator's `extensions.traffic` capture settings.
 *
 * MITM is executed by the harness proxy; the console never starts it and never
 * issues certificates. This card answers exactly two questions honestly:
 *
 *  - "HTTPS 会不会在流量页出现明文？" — only with capture_mode=record AND
 *    decrypt_https; relay always passes CONNECT through untouched;
 *  - "浏览器提示的证书是哪一张？" — via the CA fingerprint, never the file.
 *
 * The decrypt_https flag is shown as configured even in relay mode, with the
 * no-op called out: the server reports that combination in `reason`.
 */
export function TrafficCaptureCard({ capture }: { capture?: TrafficCaptureSummary }) {
  // An older server has no capture projection: render nothing instead of
  // inventing a state that could be wrong.
  if (!capture) return null
  const recording = capture.capture_mode === 'record'
  const decrypting = recording && capture.decrypt_https
  return (
    <Panel
      title="流量捕获"
      extra={<Badge variant="outline" size="sm">只读 · cyber.yaml extensions.traffic</Badge>}
      bodyClassName="space-y-3"
      data-testid="traffic-capture"
    >
      <div className="flex flex-wrap items-center gap-2">
        {recording ? (
          <ShieldCheck className="h-4 w-4 text-success" aria-hidden="true" />
        ) : (
          <ShieldOff className="h-4 w-4 text-ink-3" aria-hidden="true" />
        )}
        <Badge size="sm" variant={recording ? 'success' : 'muted'}>
          {recording ? 'record（拦截 + MITM）' : 'relay（默认透传）'}
        </Badge>
        {decrypting && <Badge size="sm" variant="info">HTTPS 解密已启用</Badge>}
        <span className="text-[11px] text-ink-3">控制台只读展示，配置由操作员直接写在 cyber.yaml，改后重启 scan 生效。</span>
      </div>

      {!capture.configured && (
        <p className="text-[12px] leading-relaxed text-ink-2">
          未配置捕获：代理按 relay 透传，流量页里 HTTPS 请求只有地址元数据，正文保持密文。
          需要明文时在 <code className="mx-1 rounded bg-surface-2 px-1 font-mono text-[11px]">extensions.traffic</code> 配
          <code className="mx-1 rounded bg-surface-2 px-1 font-mono text-[11px]">capture_mode: record</code> 与
          <code className="mx-1 rounded bg-surface-2 px-1 font-mono text-[11px]">decrypt_https: true</code>，
          并自行生成、信任 CA 证书。
        </p>
      )}

      {capture.reason && (
        <Callout tone="warning" icon={<TriangleAlert className="h-4 w-4" aria-hidden="true" />}>
          {capture.reason}
        </Callout>
      )}

      <dl className="grid grid-cols-1 gap-x-4 gap-y-1.5 text-[12px] leading-relaxed sm:grid-cols-2">
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">捕获模式</dt>
          <dd className="tw-break min-w-0 text-ink-2">{capture.capture_mode || '未设置（默认 relay）'}</dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5">
          <dt className="shrink-0 text-ink-3">HTTPS 解密</dt>
          <dd className={`tw-break min-w-0 ${capture.decrypt_https ? 'text-ink-2' : 'text-ink-3'}`}>
            {capture.decrypt_https ? (recording ? '已启用' : '已配置 · relay 模式下不生效') : '未启用'}
          </dd>
        </div>
        <div className="flex min-w-0 items-baseline gap-1.5 sm:col-span-2">
          <dt className="flex shrink-0 items-center gap-1 text-ink-3"><KeyRound className="h-3 w-3" aria-hidden="true" />CA 证书指纹</dt>
          <dd className="tw-break min-w-0 font-mono text-[11px] text-ink-2">
            {capture.ca_fingerprint || (capture.ca_cert_path ? '无法读取证书文件' : '未配置')}
            {capture.ca_cert_path && <span className="ml-1 font-sans text-ink-3">（{capture.ca_cert_path}）</span>}
          </dd>
        </div>
      </dl>

      <p className="border-t border-hairline pt-2 text-[11px] leading-relaxed text-ink-3">
        证书由操作员生成并自行加入系统/浏览器信任（指纹须与上方一致）；控制台不代发证书、不存储证书文件。
        relay 模式下即使流量记录开启，HTTPS 正文也不可见。
      </p>
    </Panel>
  )
}
