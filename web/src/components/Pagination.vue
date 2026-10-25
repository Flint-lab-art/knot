<!-- eslint-disable vue/multi-word-component-names -- 文件名由 docs/WEB.md 约定 -->
<script setup lang="ts">
// 上一页 / 下一页。游标分页没有总页数，所以只显示「当前是第几页」。
// 用 RouterLink 保留 URL 可分享（docs/WEB.md「筛选与分页放 URL」）。
const props = defineProps<{
  tag?: string
  /** 当前页起始游标（第一页为空）。 */
  cursor?: string
  /** 是否有下一页。 */
  hasNext: boolean
  /** 上一页游标（由浏览历史推导）。 */
  prevCursor?: string
}>()
</script>

<template>
  <nav class="pager" aria-label="分页">
    <RouterLink
      v-if="props.prevCursor"
      class="pager__btn"
      :to="{ name: 'home', query: { tag: props.tag, cursor: props.prevCursor } }"
    >
      上一页
    </RouterLink>
    <span v-else class="pager__btn pager__btn--disabled" aria-disabled="true">上一页</span>

    <RouterLink
      v-if="props.hasNext && props.cursor"
      class="pager__btn"
      :to="{ name: 'home', query: { tag: props.tag, cursor: props.cursor } }"
    >
      下一页
    </RouterLink>
    <span v-else class="pager__btn pager__btn--disabled" aria-disabled="true">下一页</span>
  </nav>
</template>

<style scoped>
.pager {
  display: flex;
  justify-content: center;
  gap: 12px;
  margin: 32px 0 8px;
}

.pager__btn {
  padding: 7px 18px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  color: var(--color-muted);
  font-size: var(--font-size-meta);
  transition:
    color var(--duration) var(--ease),
    border-color var(--duration) var(--ease);
}

.pager__btn:hover:not(.pager__btn--disabled) {
  text-decoration: none;
  color: var(--color-accent);
  border-color: var(--color-accent);
}

.pager__btn--disabled {
  opacity: 0.45;
  cursor: default;
}
</style>
