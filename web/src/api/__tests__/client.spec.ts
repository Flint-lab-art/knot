import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  ApiError,
  buildQuery,
  classify,
  errorMessage,
  getJSON,
  isRetryable,
  parseRetryAfter,
} from '@/api/client'

// fetch 是全局的，测试里逐个替换。
const originalFetch = globalThis.fetch

function mockFetch(impl: typeof fetch) {
  const fn = vi.fn<typeof fetch>(impl)
  globalThis.fetch = fn as unknown as typeof fetch
  return fn
}

function jsonResponse(body: unknown, init: ResponseInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
}

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

describe('classify', () => {
  it('按状态码分类', () => {
    expect(classify(429)).toBe('rateLimited')
    expect(classify(404)).toBe('notFound')
    expect(classify(500)).toBe('server')
    expect(classify(503)).toBe('server')
    expect(classify(400)).toBe('client')
  })
})

describe('parseRetryAfter', () => {
  it('解析秒数', () => {
    expect(parseRetryAfter('7')).toBe(7)
    expect(parseRetryAfter(' 3 ')).toBe(3)
  })

  it('无效值返回 undefined', () => {
    expect(parseRetryAfter(null)).toBeUndefined()
    expect(parseRetryAfter('')).toBeUndefined()
    expect(parseRetryAfter('0')).toBeUndefined()
    expect(parseRetryAfter('-1')).toBeUndefined()
    // HTTP 日期格式不支持，退化为「尽快重试」。
    expect(parseRetryAfter('Wed, 21 Oct 2026 07:28:00 GMT')).toBeUndefined()
  })
})

describe('errorMessage', () => {
  it('各类错误给出不同文案', () => {
    expect(errorMessage(new ApiError('network', 'x'))).toContain('网络')
    expect(errorMessage(new ApiError('rateLimited', 'x', 429, 429, 5))).toContain('5 秒')
    expect(errorMessage(new ApiError('rateLimited', 'x', 429, 429))).toContain('稍后再试')
    expect(errorMessage(new ApiError('notFound', 'x', 404, 404))).toContain('不存在')
    expect(errorMessage(new ApiError('server', 'x', 500, 500))).toContain('暂时不可用')
  })

  it('不泄露服务端内部信息', () => {
    const msg = errorMessage(
      new ApiError('server', 'pq: relation "notes" does not exist', 500, 500),
    )
    expect(msg).not.toContain('relation')
    expect(msg).not.toContain('pq:')
  })
})

describe('isRetryable', () => {
  it('网络/服务端/限流可重试', () => {
    expect(isRetryable(new ApiError('network', 'x'))).toBe(true)
    expect(isRetryable(new ApiError('server', 'x', 500))).toBe(true)
    expect(isRetryable(new ApiError('rateLimited', 'x', 429))).toBe(true)
    expect(isRetryable(new ApiError('notFound', 'x', 404))).toBe(false)
    expect(isRetryable(new Error('x'))).toBe(false)
  })
})

describe('buildQuery', () => {
  it('跳过空值', () => {
    expect(buildQuery({ tag: 'go', cursor: undefined, limit: 20 })).toBe('?tag=go&limit=20')
    expect(buildQuery({ tag: '', limit: undefined })).toBe('')
    expect(buildQuery({})).toBe('')
  })

  it('对特殊字符编码', () => {
    expect(buildQuery({ tag: 'go lang/测试' })).toBe('?tag=go+lang%2F%E6%B5%8B%E8%AF%95')
  })
})

describe('getJSON', () => {
  beforeEach(() => {
    // 确保每个用例从干净状态开始
    globalThis.fetch = originalFetch
  })

  it('成功时解析 JSON 并带 /api/public 前缀', async () => {
    const fn = mockFetch(async () => jsonResponse({ ok: true }))
    await expect(getJSON('/tags')).resolves.toEqual({ ok: true })
    expect(fn).toHaveBeenCalledWith(
      '/api/public/tags',
      expect.objectContaining({ headers: expect.any(Object) }),
    )
  })

  it('404 抛 notFound', async () => {
    mockFetch(async () => jsonResponse({ code: 404, msg: 'not found' }, { status: 404 }))
    const err = await getJSON('/notes/x').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).kind).toBe('notFound')
    expect((err as ApiError).status).toBe(404)
  })

  it('429 带 Retry-After', async () => {
    mockFetch(
      async () =>
        new Response(JSON.stringify({ code: 429, msg: 'rate limit exceeded' }), {
          status: 429,
          headers: { 'Retry-After': '9' },
        }),
    )
    const err = (await getJSON('/notes').catch((e) => e)) as ApiError
    expect(err.kind).toBe('rateLimited')
    expect(err.retryAfter).toBe(9)
  })

  it('500 且错误体非 JSON 时按状态码兜底', async () => {
    mockFetch(async () => new Response('oops', { status: 500 }))
    const err = (await getJSON('/notes').catch((e) => e)) as ApiError
    expect(err.kind).toBe('server')
    expect(err.code).toBe(500)
  })

  it('网络失败抛 network', async () => {
    mockFetch(async () => {
      throw new TypeError('Failed to fetch')
    })
    const err = (await getJSON('/notes').catch((e) => e)) as ApiError
    expect(err.kind).toBe('network')
  })
})
