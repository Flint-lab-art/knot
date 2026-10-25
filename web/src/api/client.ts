// fetch 薄封装：统一 base URL、JSON 解析、错误码与 429。
//
// 页面只读这里的错误类型，不关心 HTTP 细节（docs/WEB.md「状态设计」）。

import type { ApiErrorBody } from '@/types/api'

/** 错误分类：页面据此展示不同文案。 */
export type ApiErrorKind = 'network' | 'rateLimited' | 'notFound' | 'server' | 'client'

/** 统一的 API 错误。 */
export class ApiError extends Error {
  readonly kind: ApiErrorKind
  readonly status: number
  readonly code: number
  /** 429 时服务端建议的等待秒数；无则 undefined。 */
  readonly retryAfter?: number

  constructor(kind: ApiErrorKind, message: string, status = 0, code = 0, retryAfter?: number) {
    super(message)
    this.name = 'ApiError'
    this.kind = kind
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
  }
}

/** 把 HTTP 状态码映射成错误分类。 */
export function classify(status: number): ApiErrorKind {
  if (status === 429) return 'rateLimited'
  if (status === 404) return 'notFound'
  if (status >= 500) return 'server'
  return 'client'
}

/** 解析 Retry-After（秒数）；不支持 HTTP 日期格式，退化为 undefined。 */
export function parseRetryAfter(raw: string | null): number | undefined {
  if (!raw) return undefined
  const secs = Number.parseInt(raw.trim(), 10)
  return Number.isFinite(secs) && secs > 0 ? secs : undefined
}

/** 用户可见的错误文案：不泄露服务端内部信息。 */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    switch (err.kind) {
      case 'network':
        return '网络连接失败，请检查网络后重试'
      case 'rateLimited':
        return err.retryAfter
          ? `请求过于频繁，请 ${err.retryAfter} 秒后再试`
          : '请求过于频繁，请稍后再试'
      case 'notFound':
        return '内容不存在'
      default:
        return '服务暂时不可用，请稍后重试'
    }
  }
  return '出现未知错误'
}

/** 是否是「稍后重试可能成功」的错误（用于显示重试按钮）。 */
export function isRetryable(err: unknown): boolean {
  return (
    err instanceof ApiError &&
    (err.kind === 'network' || err.kind === 'server' || err.kind === 'rateLimited')
  )
}

/** 查询参数拼装：空值不发送，避免 `?tag=` 这类噪声。 */
export function buildQuery(params: Record<string, string | number | undefined | null>): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue
    q.set(k, String(v))
  }
  const s = q.toString()
  return s ? `?${s}` : ''
}

const BASE = '/api/public'

/** 发 GET 请求并解析 JSON；非 2xx 抛 ApiError。 */
export async function getJSON<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response
  try {
    res = await fetch(`${BASE}${path}`, {
      headers: { Accept: 'application/json' },
      ...init,
    })
  } catch (err) {
    // fetch 只在网络层失败时 reject。
    throw new ApiError('network', err instanceof Error ? err.message : 'network error')
  }

  if (!res.ok) {
    const kind = classify(res.status)
    let body: Partial<ApiErrorBody> = {}
    try {
      body = (await res.json()) as Partial<ApiErrorBody>
    } catch {
      // 错误体不是 JSON 时用状态码兜底。
    }
    throw new ApiError(
      kind,
      body.msg ?? `HTTP ${res.status}`,
      res.status,
      body.code ?? res.status,
      parseRetryAfter(res.headers.get('Retry-After')),
    )
  }

  return (await res.json()) as T
}
