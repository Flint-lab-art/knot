import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { __resetNotesForTest, useNotes } from '@/composables/useNotes'
import { __resetTagsForTest, useTags } from '@/composables/useTags'

const originalFetch = globalThis.fetch

function mockJSON(body: unknown, init?: ResponseInit) {
  const fn = vi.fn<() => Promise<Response>>(
    async () =>
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
        ...init,
      }),
  )
  globalThis.fetch = fn as unknown as typeof fetch
  return fn
}

beforeEach(() => {
  __resetNotesForTest()
  __resetTagsForTest()
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

const note = (id: string, title: string) => ({
  id,
  title,
  created_at: '2026-10-24T10:00:00Z',
  tags: [],
})

describe('useNotes', () => {
  it('首屏加载替换列表并记录游标', async () => {
    mockJSON({ notes: [note('1', 'a')], next_cursor: 'cur2' })
    const { notes, nextCursor, loading, load } = useNotes()
    await load({ limit: 20 })
    expect(notes.value).toHaveLength(1)
    expect(nextCursor.value).toBe('cur2')
    expect(loading.value).toBe(false)
  })

  it('带游标时追加而不是替换', async () => {
    mockJSON({ notes: [note('1', 'a')] })
    const { notes, load } = useNotes()
    await load()
    mockJSON({ notes: [note('2', 'b')] })
    await load({ cursor: 'cur2' })
    expect(notes.value.map((n) => n.id)).toEqual(['1', '2'])
  })

  it('没有 next_cursor 时置为 undefined', async () => {
    mockJSON({ notes: [] })
    const { nextCursor, load } = useNotes()
    await load()
    expect(nextCursor.value).toBeUndefined()
  })

  it('错误写入 error 并保留旧列表', async () => {
    mockJSON({ notes: [note('1', 'a')] })
    const { notes, error, load } = useNotes()
    await load()

    globalThis.fetch = (async () =>
      new Response(JSON.stringify({ code: 429, msg: 'slow' }), {
        status: 429,
      })) as unknown as typeof fetch
    await load({ cursor: 'x' })
    expect(error.value).not.toBeNull()
    // 追加失败不应清空已有数据
    expect(notes.value).toHaveLength(1)
  })

  it('reset 清空列表与错误', async () => {
    mockJSON({ notes: [note('1', 'a')], next_cursor: 'c' })
    const { notes, nextCursor, error, load, reset } = useNotes()
    await load()
    reset()
    expect(notes.value).toHaveLength(0)
    expect(nextCursor.value).toBeUndefined()
    expect(error.value).toBeNull()
  })

  it('请求参数包含 tag/limit', async () => {
    const fn = mockJSON({ notes: [] })
    const { load } = useNotes()
    await load({ tag: 'go', limit: 20 })
    const firstCall = fn.mock.calls[0] as unknown[] | undefined
    const url = String(firstCall?.[0])
    expect(url).toContain('/api/public/notes?')
    expect(url).toContain('tag=go')
    expect(url).toContain('limit=20')
  })
})

describe('useTags', () => {
  it('只加载一次，除非 force', async () => {
    const fn = mockJSON([{ id: 't1', name: 'go' }])
    const { tags, load } = useTags()
    await load()
    await load()
    expect(fn).toHaveBeenCalledTimes(1)
    expect(tags.value).toHaveLength(1)

    await load(true)
    expect(fn).toHaveBeenCalledTimes(2)
  })

  it('失败后仍可重试', async () => {
    globalThis.fetch = (async () =>
      new Response('boom', { status: 500 })) as unknown as typeof fetch
    const { error, load, tags } = useTags()
    await load()
    expect(error.value).not.toBeNull()

    mockJSON([{ id: 't1', name: 'go' }])
    await load()
    expect(tags.value).toHaveLength(1)
  })
})
