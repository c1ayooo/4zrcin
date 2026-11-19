/**
 * How fresh a polled resource is, in three operator-facing states.
 *
 * The console polls rather than streams, so "live" is a claim about the last
 * successful read, not about a socket. Two missed periods mean the page is not
 * actually being refreshed (hidden tab, suspended timer), and the label must
 * stop saying "live" even though no request failed. A failure keeps the last
 * success time: the operator needs to know how old the data on screen is.
 */
export interface FreshnessState {
  updatedAt?: number
  error?: string
  loading: boolean
}

export type FreshnessTone = 'idle' | 'live' | 'stale' | 'lost'

export interface Freshness {
  tone: FreshnessTone
  label: string
  /** Full sentence for the title attribute; the label stays short. */
  detail: string
}

function clock(at: number): string {
  return new Date(at).toLocaleTimeString('zh-CN', { hour12: false })
}

function aged(ms: number): string {
  const seconds = Math.round(ms / 1000)
  if (seconds < 60) return `${seconds} 秒`
  return `${Math.round(seconds / 60)} 分钟`
}

/** Missed periods tolerated before the label admits the data is old. */
export const STALE_PERIODS = 2

export function freshnessOf(state: FreshnessState, intervalMs: number, now = Date.now()): Freshness {
  if (state.error) {
    return state.updatedAt
      ? {
          tone: 'lost',
          label: `数据流中断 · 上次成功 ${clock(state.updatedAt)}`,
          detail: `最近一次读取失败：${state.error}`,
        }
      : {
          tone: 'lost',
          label: '数据流中断 · 从未成功',
          detail: `还没有成功读取过；最近一次失败：${state.error}`,
        }
  }
  if (!state.updatedAt) {
    return state.loading
      ? { tone: 'idle', label: '连接中…', detail: '首次读取尚未返回。' }
      : { tone: 'idle', label: '尚未取到数据', detail: '读取已结束，但没有得到数据。' }
  }
  const age = Math.max(0, now - state.updatedAt)
  if (age > intervalMs * STALE_PERIODS) {
    return {
      tone: 'stale',
      label: `数据已 ${aged(age)} 未更新`,
      detail: `页面可能处于后台，或刷新已暂停；最后一次成功在 ${clock(state.updatedAt)}。`,
    }
  }
  return {
    tone: 'live',
    label: `实时 · ${clock(state.updatedAt)} 更新`,
    detail: `每 ${Math.round(intervalMs / 1000)} 秒读取一次；当前显示的是最后一次成功的结果。`,
  }
}
