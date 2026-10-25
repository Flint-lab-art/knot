import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

import NoteCard, { formatDate } from '@/components/NoteCard.vue'
import NoteList from '@/views/NoteList.vue'
import NoteDetail from '@/views/NoteDetail.vue'
import { __resetNotesForTest } from '@/composables/useNotes'
import { __resetTagsForTest } from '@/composables/useTags'

const originalFetch = globalThis.fetch

/** 用路由响应返回不同 JSON，键是 URL 包含的片段。 */
function mockRoutes(routes: Record<string, { body: unknown; status?: number }>) {
  globalThis.fetch = vi.fn<typeof fetch>(async (input) => {
    const url = String(input)
    const key = Object.keys(routes).find((k) => url.includes(k))
    if (!key) return new Response('not found', { status: 404 })
    const { body, status = 200 } = routes[key]!
    return new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })
  }) as unknown as typeof fetch
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: NoteList },
      { path: '/notes/:id', name: 'note', component: NoteDetail },
      { path: '/:pathMatch(.*)*', name: 'notfound', component: { template: '<div>404</div>' } },
    ],
  })
}

async function mountAt(component: unknown, path: string) {
  const router = makeRouter()
  await router.push(path)
  await router.isReady()
  const wrapper = mount(component as never, { global: { plugins: [router] } })
  await flushPromises()
  return { wrapper, router }
}

beforeEach(() => {
  __resetNotesForTest()
  __resetTagsForTest()
  globalThis.fetch = originalFetch
})

afterEach(() => {
  globalThis.fetch = originalFetch
  vi.restoreAllMocks()
})

describe('NoteCard', () => {
  it('渲染标题、日期与标签', () => {
    const wrapper = mount(NoteCard, {
      props: {
        note: {
          id: 'abc',
          title: '标题',
          created_at: '2026-10-20T10:00:00Z',
          tags: ['go', 'notes'],
        },
      },
      global: { plugins: [makeRouter()] },
    })
    expect(wrapper.text()).toContain('标题')
    expect(wrapper.text()).toContain('go, notes')
    expect(wrapper.find('time').attributes('datetime')).toBe('2026-10-20T10:00:00Z')
  })

  it('没有标签时不显示分隔符', () => {
    const wrapper = mount(NoteCard, {
      props: { note: { id: 'abc', title: 't', created_at: '2026-10-20T10:00:00Z', tags: [] } },
      global: { plugins: [makeRouter()] },
    })
    expect(wrapper.text()).not.toContain('·')
  })
})

describe('formatDate', () => {
  it('格式化与容错', () => {
    expect(formatDate('2026-10-20T10:00:00Z')).toMatch(/^2026-10-2\d$/)
    expect(formatDate('not-a-date')).toBe('')
  })
})

describe('NoteList', () => {
  it('渲染卡片列表与标签条', async () => {
    mockRoutes({
      '/api/public/tags': { body: [{ id: 't1', name: 'go' }] },
      '/api/public/notes': {
        body: {
          notes: [{ id: 'n1', title: '第一篇', created_at: '2026-10-20T10:00:00Z', tags: ['go'] }],
          next_cursor: 'c2',
        },
      },
    })
    const { wrapper } = await mountAt(NoteList, '/')
    expect(wrapper.findAll('article').length).toBe(1)
    expect(wrapper.text()).toContain('第一篇')
    expect(wrapper.text()).toContain('go')
    // 有下一页时显示可点击的「下一页」
    const links = wrapper.findAll('a').map((a) => a.text())
    expect(links).toContain('下一页')
  })

  it('空态文案', async () => {
    mockRoutes({
      '/api/public/tags': { body: [] },
      '/api/public/notes': { body: { notes: [] } },
    })
    const { wrapper } = await mountAt(NoteList, '/')
    expect(wrapper.text()).toContain('还没有公开笔记')
  })

  it('按标签筛选时提示对应的空态', async () => {
    mockRoutes({
      '/api/public/tags': { body: [{ id: 't1', name: 'go' }] },
      '/api/public/notes': { body: { notes: [] } },
    })
    const { wrapper } = await mountAt(NoteList, '/?tag=go')
    expect(wrapper.text()).toContain('标签「go」下还没有公开笔记')
  })

  it('429 时显示限流提示而不是白屏', async () => {
    mockRoutes({
      '/api/public/tags': { body: [] },
      '/api/public/notes': { body: { code: 429, msg: 'slow down' }, status: 429 },
    })
    const { wrapper } = await mountAt(NoteList, '/')
    expect(wrapper.text()).toContain('请求过于频繁')
    expect(wrapper.find('button').exists()).toBe(true)
  })

  it('404 页面对不存在的路由', async () => {
    mockRoutes({})
    const { wrapper } = await mountAt(NoteList, '/?tag=x')
    expect(wrapper.text()).toMatch(/还没有公开笔记|不存在/)
  })
})

describe('NoteDetail', () => {
  it('渲染标题与正文', async () => {
    mockRoutes({
      '/api/public/notes/n1': {
        body: {
          id: 'n1',
          title: '阅读页',
          created_at: '2026-10-20T10:00:00Z',
          tags: ['go'],
          raw_md: '# 正文\n\n段落',
        },
      },
    })
    const { wrapper } = await mountAt(NoteDetail, '/notes/n1')
    expect(wrapper.text()).toContain('阅读页')
    expect(wrapper.find('.markdown h1').text()).toBe('正文')
    expect(wrapper.find('.markdown p').text()).toBe('段落')
  })

  it('非 public 返回 404 提示', async () => {
    mockRoutes({ '/api/public/notes/n1': { body: { code: 404, msg: 'not found' }, status: 404 } })
    const { wrapper } = await mountAt(NoteDetail, '/notes/n1')
    expect(wrapper.text()).toContain('笔记不存在或未公开')
    expect(wrapper.text()).toContain('回列表')
  })

  it('500 显示可重试错误', async () => {
    mockRoutes({ '/api/public/notes/n1': { body: 'oops', status: 500 } })
    const { wrapper } = await mountAt(NoteDetail, '/notes/n1')
    expect(wrapper.text()).toContain('暂时不可用')
    expect(wrapper.find('button').text()).toContain('重试')
  })

  it('正文里的 script 不会被渲染成节点', async () => {
    mockRoutes({
      '/api/public/notes/n1': {
        body: {
          id: 'n1',
          title: 'x',
          created_at: '2026-10-20T10:00:00Z',
          tags: [],
          raw_md: '<script>alert(1)</script>\n\n正常段落',
        },
      },
    })
    const { wrapper } = await mountAt(NoteDetail, '/notes/n1')
    expect(wrapper.find('.markdown script').exists()).toBe(false)
    expect(wrapper.find('.markdown').text()).toContain('正常段落')
  })
})
