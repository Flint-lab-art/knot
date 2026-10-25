// Markdown 渲染 + HTML sanitize（docs/WEB.md「渲染与安全」）。
//
// 这是公开站的 XSS 底线：markdown-it 关闭 raw HTML，DOMPurify 再兜一层。
// 任何一条失效都不能单独依赖另一条 —— 配置变更必须同步更新
// src/composables/__tests__/markdown.spec.ts 的四条安全用例。

import MarkdownIt from 'markdown-it'
import DOMPurify from 'dompurify'

const md = new MarkdownIt({
  // 禁用内联 HTML：即便 DOMPurify 被绕过，<script> 也不会成为节点。
  html: false,
  // 链接安全：把 javascript: 等协议拦在渲染阶段。
  linkify: true,
  breaks: false,
  typographer: false,
})

// 覆盖默认的链接渲染：外链加 rel/target，危险协议直接降级成纯文本。
const defaultLinkOpen =
  md.renderer.rules.link_open ??
  ((tokens, idx, options, _env, self) => self.renderToken(tokens, idx, options))

md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  const href = String(tokens[idx]?.attrGet('href') ?? '')
  if (isUnsafeHref(href)) {
    // 危险协议：不生成 <a>，退化成普通文本。
    return ''
  }
  if (isExternal(href)) {
    tokens[idx]?.attrSet('target', '_blank')
    tokens[idx]?.attrSet('rel', 'noopener noreferrer')
  }
  return defaultLinkOpen(tokens, idx, options, env, self)
}

/** 判断链接是否使用了可执行协议。 */
export function isUnsafeHref(href: string): boolean {
  // 去掉空白与控制字符后再判断，绕过 "java\tscript:" 之类的写法。
  // 用逐字符过滤而不是正则字符类：控制字符范围会被 lint 规则拦下，
  // 而且这里本来也不需要正则。
  let normalized = ''
  for (const ch of href) {
    const code = ch.codePointAt(0) ?? 0
    if (code <= 0x20 || code === 0x7f) continue
    normalized += ch
  }
  const lower = normalized.toLowerCase()
  return (
    lower.startsWith('javascript:') || lower.startsWith('vbscript:') || lower.startsWith('data:')
  )
}

function isExternal(href: string): boolean {
  return /^https?:\/\//i.test(href)
}

/** 把 Markdown 渲染成可直接插入的、已 sanitize 的 HTML。 */
export function renderMarkdown(source: string): string {
  const raw = md.render(source)
  return DOMPurify.sanitize(raw, {
    // 禁止表单与框架类标签；保留 GFM 需要的 table/code/input(checkbox)。
    FORBID_TAGS: ['script', 'style', 'iframe', 'object', 'embed', 'form', 'base', 'link', 'meta'],
    FORBID_ATTR: ['style', 'srcset'],
    // 允许任务列表复选框需要的属性。
    ADD_ATTR: ['checked', 'disabled', 'target', 'rel'],
  })
}
