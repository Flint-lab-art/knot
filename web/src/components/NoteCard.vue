<script lang="ts">
/** 把 RFC3339 转成 YYYY-MM-DD（本地时区）；非法时间返回空串。 */
export function formatDate(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}
</script>

<script setup lang="ts">
import { computed } from 'vue'

import type { PublicNote } from '@/types/api'

// 列表卡片：标题 + 时间 + 标签，不显示摘要（docs/WEB.md）。
const props = defineProps<{ note: PublicNote }>()

const date = computed(() => formatDate(props.note.created_at))
</script>

<template>
  <article class="card">
    <RouterLink class="card__title" :to="{ name: 'note', params: { id: note.id } }">
      {{ note.title }}
    </RouterLink>
    <p class="card__meta">
      <time :datetime="note.created_at">{{ date }}</time>
      <template v-if="note.tags.length">
        <span class="card__sep" aria-hidden="true">·</span>
        <span>{{ note.tags.join(', ') }}</span>
      </template>
    </p>
  </article>
</template>

<style scoped>
.card {
  border: 1px solid var(--color-border);
  border-radius: var(--radius-card);
  padding: 16px 20px;
  background: var(--color-bg);
  /* 默认无阴影；hover 只加深边框，不做位移（docs/WEB.md） */
  transition: border-color var(--duration) var(--ease);
}

.card:hover {
  border-color: var(--color-border-strong);
}

.card__title {
  display: block;
  color: var(--color-text);
  font-size: 19px;
  font-weight: 600;
  line-height: 1.4;
  overflow-wrap: anywhere;
}

.card__title:hover {
  text-decoration: none;
  color: var(--color-accent);
}

.card__meta {
  margin: 6px 0 0;
  font-size: var(--font-size-meta);
  color: var(--color-muted);
}

.card__sep {
  margin: 0 0.4em;
}

@media (max-width: 767px) {
  .card {
    padding: 14px 16px;
  }
}
</style>
