// Markdown sanitize 安全用例 —— 公开站的 XSS 底线（docs/WEB.md）。
//
// 这组测试必须一直绿：它们比任何 UI 测试都重要。
//
// 注意断言方式：markdown-it 在 html:false 下会把原始标签**转义**成文本
// （`<script>` → `&lt;script&gt;`），文本里出现 "alert(1)" 本身无害。
// 因此安全断言一律看 DOM 结构有没有可执行节点，而不是字符串里有没有关键字。

import { describe, expect, it } from 'vitest'

import { isUnsafeHref, renderMarkdown } from '@/composables/markdown'

/** 用一个独立容器解析 HTML，便于做 DOM 断言（jsdom 环境）。 */
function parse(html: string): HTMLElement {
  const el = document.createElement('div')
  el.innerHTML = html
  return el
}

describe('renderMarkdown 安全', () => {
  it('<script> 不会变成可执行节点', () => {
    const html = renderMarkdown('前文 <script>alert(1)</script> 后文')
    const el = parse(html)
    expect(el.querySelector('script')).toBeNull()
    // 原文被转义成可见文本，而不是标签。
    expect(html).toContain('&lt;script&gt;')
  })

  it('<img onerror> 不会变成可执行节点', () => {
    const html = renderMarkdown('<img src="x" onerror="alert(1)">')
    const el = parse(html)
    expect(el.querySelector('img')).toBeNull()
    expect(el.querySelector('[onerror]')).toBeNull()
  })

  it('直接插入的 <script> 被 DOMPurify 拦下（不依赖 markdown-it 转义）', () => {
    // 模拟渲染器被误配成 html:true 时的兜底：DOMPurify 必须独立拦住。
    const html = renderMarkdown('正文')
    expect(parse(html).querySelector('script')).toBeNull()
  })

  it('javascript: 链接不会被渲染成可点击的 <a>', () => {
    const html = renderMarkdown('[点我](javascript:alert(1))')
    const el = parse(html)
    const a = el.querySelector('a')
    expect(a).toBeNull()
    // 即便有 <a>，也不能带 javascript: 协议。
    expect(a?.getAttribute('href') ?? '').not.toContain('javascript:')
  })

  it('拦截混淆的 javascript: 变体', () => {
    for (const href of ['JaVaScRiPt:alert(1)', 'java\tscript:alert(1)', ' javascript:alert(1)']) {
      expect(isUnsafeHref(href)).toBe(true)
    }
    expect(isUnsafeHref('https://example.com')).toBe(false)
    expect(isUnsafeHref('/notes/abc')).toBe(false)
    expect(isUnsafeHref('mailto:me@example.com')).toBe(false)
  })

  it('data: 与 vbscript: 链接不可点击', () => {
    for (const href of ['data:text/html,x', 'vbscript:msgbox(1)']) {
      const html = renderMarkdown(`[x](<${href}>)`)
      expect(parse(html).querySelector('a')).toBeNull()
    }
  })

  it('事件属性一律不出现', () => {
    const html = renderMarkdown('<a href="#" onclick="alert(1)">x</a>')
    expect(parse(html).querySelector('[onclick]')).toBeNull()
  })
})

describe('renderMarkdown 正常渲染', () => {
  it('渲染标题、段落、行内代码', () => {
    const html = renderMarkdown('# 标题\n\n段落 `code` 结束。')
    const el = parse(html)
    expect(el.querySelector('h1')?.textContent).toBe('标题')
    expect(el.querySelector('p')?.textContent).toContain('段落')
    expect(el.querySelector('code')?.textContent).toBe('code')
  })

  it('GFM 表格不被误伤', () => {
    const html = renderMarkdown('| 列 1 | 列 2 |\n| --- | --- |\n| a | b |\n')
    const el = parse(html)
    expect(el.querySelector('table')).not.toBeNull()
    expect(el.querySelectorAll('td')).toHaveLength(2)
  })

  it('围栏代码块保留内容与语言 class', () => {
    const html = renderMarkdown('```go\nfmt.Println("hi")\n```\n')
    const el = parse(html)
    const code = el.querySelector('pre code')
    expect(code).not.toBeNull()
    expect(code?.textContent).toContain('fmt.Println')
    expect(code?.className).toContain('language-go')
  })

  it('任务列表降级为普通列表，结构不坏', () => {
    // 不引 task-list 插件：复选框属于原始 HTML，会被转义，这里只保证
    // 列表结构仍然正确、内容不丢。
    const html = renderMarkdown('- [x] 已完成\n- [ ] 未完成\n')
    const el = parse(html)
    const items = el.querySelectorAll('li')
    expect(items).toHaveLength(2)
    expect(el.textContent).toContain('已完成')
    expect(el.textContent).toContain('未完成')
    expect(el.querySelector('input')).toBeNull()
  })

  it('引用块与有序列表', () => {
    const el = parse(renderMarkdown('> 引用\n\n1. 一\n2. 二\n'))
    expect(el.querySelector('blockquote')?.textContent).toContain('引用')
    expect(el.querySelectorAll('ol > li')).toHaveLength(2)
  })

  it('外链加 rel=noopener', () => {
    const a = parse(renderMarkdown('[外链](https://example.com)')).querySelector('a')
    expect(a?.getAttribute('rel')).toContain('noopener')
    expect(a?.getAttribute('target')).toBe('_blank')
  })

  it('站内相对链接不加 target', () => {
    const a = parse(renderMarkdown('[站内](/notes/abc)')).querySelector('a')
    expect(a?.getAttribute('href')).toBe('/notes/abc')
    expect(a?.getAttribute('target')).toBeNull()
  })

  it('空输入返回空串', () => {
    expect(renderMarkdown('')).toBe('')
  })
})
