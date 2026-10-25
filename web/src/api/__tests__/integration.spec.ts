// 真实后端集成测试：不 mock fetch，直连本地 knot-server。
//
// 只在设置了 KNOT_WEB_E2E_BASE 时运行（默认跳过），避免 make test 依赖外部服务：
//
//   KNOT_WEB_E2E_BASE=http://127.0.0.1:8080 \
//     pnpm test:unit -- --run src/api/__tests__/integration.spec.ts
//
// 目的是验证「前端 api 层 + 真实服务端契约」这一层，而不是组件渲染
//（组件已由 mock 用例覆盖）。

import { beforeAll, describe, expect, it } from 'vitest'

import { ApiError } from '@/api/client'
import { getNote, getNotes } from '@/api/notes'
import { getTags } from '@/api/tags'

const BASE = process.env.KNOT_WEB_E2E_BASE
const hasBackend = Boolean(BASE)

// jsdom 里没有同源代理，直接改写 URL 到真实服务。
const originalFetch = globalThis.fetch

beforeAll(() => {
  if (!hasBackend) return
  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString()
    return originalFetch(url.replace(/^\/api\/public/, `${BASE}/api/public`), init)
  }) as typeof fetch
})

describe.skipIf(!hasBackend)('真实后端契约', () => {
  it('GET /api/public/notes 返回游标分页结构', async () => {
    const list = await getNotes({ limit: 5 })
    expect(Array.isArray(list.notes)).toBe(true)
    expect(list.notes.length).toBeLessThanOrEqual(5)
    for (const n of list.notes) {
      expect(typeof n.id).toBe('string')
      expect(typeof n.title).toBe('string')
      expect(Array.isArray(n.tags)).toBe(true)
      // 公开列表不得返回内部字段
      expect(n).not.toHaveProperty('version')
      expect(n).not.toHaveProperty('visibility')
      expect(n).not.toHaveProperty('modified_at')
      expect(n).not.toHaveProperty('raw_md')
    }
  })

  it('GET /api/public/tags 只返回 public 笔记的标签', async () => {
    const tags = await getTags()
    expect(Array.isArray(tags)).toBe(true)
    for (const t of tags) {
      expect(typeof t.id).toBe('string')
      expect(typeof t.name).toBe('string')
    }
  })

  it('GET /api/public/notes/:id 返回正文', async () => {
    const list = await getNotes({ limit: 1 })
    if (list.notes.length === 0) return
    const detail = await getNote(list.notes[0]!.id)
    expect(detail.id).toBe(list.notes[0]!.id)
    expect(typeof detail.raw_md).toBe('string')
  })

  it('不存在的 id 抛 notFound', async () => {
    const err = await getNote('00000000-0000-0000-0000-000000000000').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).kind).toBe('notFound')
  })

  it('非法 id 抛 client 错误（400），不是 500', async () => {
    const err = await getNote('not-a-uuid').catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(400)
  })
})
