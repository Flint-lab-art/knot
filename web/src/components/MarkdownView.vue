<script setup lang="ts">
import { computed } from 'vue'

import { renderMarkdown } from '@/composables/markdown'

// Markdown 渲染组件：markdown-it + DOMPurify（见 composables/markdown.ts）。
// 用 v-html 是必要的 —— 内容已经过 sanitize，且这是唯一允许注入 HTML 的位置。
const props = defineProps<{ source: string }>()

const html = computed(() => renderMarkdown(props.source))
</script>

<template>
  <!-- eslint-disable-next-line vue/no-v-html -- 内容已由 DOMPurify 清理 -->
  <div class="markdown" v-html="html" />
</template>

<style scoped>
.markdown {
  font-size: var(--font-size-body);
  line-height: var(--line-height-body);
  overflow-wrap: break-word;
}

.markdown :deep(> :first-child) {
  margin-top: 0;
}

.markdown :deep(ul),
.markdown :deep(ol) {
  padding-left: 1.4em;
}

.markdown :deep(li) {
  margin: 0.3em 0;
}

.markdown :deep(code) {
  white-space: break-spaces;
}

.markdown :deep(pre) {
  /* 代码块横向滚动，不撑破 760px 内容宽（docs/WEB.md） */
  max-width: 100%;
}

.markdown :deep(input[type='checkbox']) {
  margin-right: 0.4em;
}
</style>
