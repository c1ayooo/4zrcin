import type { ReactNode } from 'react'
import { cn, useTheme } from '@cyber/theme'

/**
 * 左上角品牌锁排: 单个字标，随主题切换 (`wordmark-white.svg` / `wordmark-black.svg`)。
 *
 * 图标包里的 mark 与 wordmark 都以「4」为主形，并排会重复，所以侧栏只用
 * 字标；方形 mark 留给浏览器标签（`public/favicon.svg`）和需要方标的场景。
 */
export function Brand({ badge, className }: { badge?: ReactNode; className?: string }) {
  const { isDark } = useTheme()
  return (
    <div className={cn('flex min-w-0 items-center gap-2.5', className)}>
      <img
        src={isDark ? '/static/logo/wordmark-white.svg' : '/static/logo/wordmark-black.svg'}
        alt="4zrcin"
        draggable={false}
        className="h-[38px] w-auto shrink-0 select-none"
      />
      {badge ? <div className="ml-auto shrink-0">{badge}</div> : null}
    </div>
  )
}
