<script setup lang="ts">
import type { PublicTag } from '@/types/api'

// 顶部标签条：单选，点击切换 ?tag=（docs/WEB.md）。
// 用 RouterLink 而不是点击回调，保证「新标签页打开」与无障碍行为正常。
const props = defineProps<{ tags: PublicTag[]; active?: string }>()

function queryFor(tag?: string) {
  return tag ? { tag } : {}
}
</script>

<template>
  <nav class="tagbar" aria-label="标签筛选">
    <RouterLink
      class="pill"
      :class="{ 'pill--active': !props.active }"
      :to="{ name: 'home', query: queryFor() }"
    >
      全部
    </RouterLink>
    <RouterLink
      v-for="tag in props.tags"
      :key="tag.id"
      class="pill"
      :class="{ 'pill--active': props.active === tag.name }"
      :to="{ name: 'home', query: queryFor(tag.name) }"
    >
      {{ tag.name }}
    </RouterLink>
  </nav>
</template>

<style scoped>
.tagbar {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 20px 0 24px;
}

.pill {
  display: inline-block;
  padding: 5px 14px;
  border-radius: var(--radius-pill);
  border: 1px solid var(--color-border);
  color: var(--color-muted);
  font-size: var(--font-size-meta);
  transition:
    color var(--duration) var(--ease),
    background var(--duration) var(--ease),
    border-color var(--duration) var(--ease);
}

.pill:hover {
  text-decoration: none;
  color: var(--color-text);
  border-color: var(--color-border-strong);
}

.pill--active {
  color: var(--color-accent);
  border-color: var(--color-accent);
  background: var(--color-accent-soft);
}

.pill--active:hover {
  color: var(--color-accent);
}
</style>
