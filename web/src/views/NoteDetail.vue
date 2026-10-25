<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'

import { ApiError, errorMessage, isRetryable } from '@/api/client'
import { getNote } from '@/api/notes'
import { formatDate } from '@/components/NoteCard.vue'
import MarkdownView from '@/components/MarkdownView.vue'
import type { PublicNoteDetail } from '@/types/api'

// 阅读页：正文最大 760px 居中，不套卡片（docs/WEB.md）。
const route = useRoute()

const note = ref<PublicNoteDetail | null>(null)
const loading = ref(false)
const error = ref<unknown>(null)

const id = computed(() => String(route.params.id ?? ''))
const notFound = computed(() => error.value instanceof ApiError && error.value.kind === 'notFound')
const date = computed(() => (note.value ? formatDate(note.value.created_at) : ''))

async function load() {
  loading.value = true
  error.value = null
  try {
    note.value = await getNote(id.value)
  } catch (err) {
    note.value = null
    error.value = err
  } finally {
    loading.value = false
  }
}

watch(id, () => void load(), { immediate: true })
</script>

<template>
  <article class="note">
    <RouterLink class="back" :to="{ name: 'home' }">← 返回列表</RouterLink>

    <p v-if="loading" class="state" role="status">加载中…</p>

    <div v-else-if="notFound" class="state" role="alert">
      <p>笔记不存在或未公开</p>
      <RouterLink class="back" :to="{ name: 'home' }">回列表</RouterLink>
    </div>

    <div v-else-if="error" class="state" role="alert">
      <p>{{ errorMessage(error) }}</p>
      <button v-if="isRetryable(error)" class="retry" type="button" @click="load">重试</button>
    </div>

    <template v-else-if="note">
      <header class="note__header">
        <h1 class="note__title">{{ note.title }}</h1>
        <p class="note__meta">
          <time :datetime="note.created_at">{{ date }}</time>
          <template v-if="note.tags.length">
            <span class="note__sep" aria-hidden="true">·</span>
            <span>{{ note.tags.join(', ') }}</span>
          </template>
        </p>
      </header>

      <MarkdownView class="note__body" :source="note.raw_md" />

      <footer v-if="note.tags.length" class="note__tags">
        标签：
        <RouterLink
          v-for="tag in note.tags"
          :key="tag"
          class="note__tag"
          :to="{ name: 'home', query: { tag } }"
        >
          {{ tag }}
        </RouterLink>
      </footer>
    </template>
  </article>
</template>

<style scoped>
.note {
  max-width: var(--content-width);
  margin: 0 auto;
  padding-bottom: 64px;
}

.back {
  display: inline-block;
  margin: 24px 0 8px;
  font-size: var(--font-size-meta);
  color: var(--color-muted);
}

.back:hover {
  color: var(--color-accent);
}

.note__header {
  padding: 8px 0 20px;
  border-bottom: 1px solid var(--color-border);
  margin-bottom: 28px;
}

.note__title {
  margin: 0 0 8px;
  font-size: var(--font-size-h1);
  font-weight: 700;
  line-height: 1.25;
  overflow-wrap: anywhere;
}

.note__meta {
  margin: 0;
  font-size: var(--font-size-meta);
  color: var(--color-muted);
}

.note__sep {
  margin: 0 0.4em;
}

.note__body {
  /* 正文直接铺在页面上，不套卡片 */
  max-width: var(--content-width);
}

.note__tags {
  margin-top: 48px;
  padding-top: 20px;
  border-top: 1px solid var(--color-border);
  font-size: var(--font-size-meta);
  color: var(--color-muted);
}

.note__tag {
  display: inline-block;
  margin-right: 10px;
}

.state {
  margin: 64px 0;
  text-align: center;
  color: var(--color-muted);
  font-size: var(--font-size-meta);
}

.state--error p {
  margin: 0 0 12px;
}

.retry {
  padding: 7px 18px;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-control);
  background: transparent;
  color: var(--color-text);
  font: inherit;
  font-size: var(--font-size-meta);
  cursor: pointer;
  transition: border-color var(--duration) var(--ease);
}

.retry:hover {
  border-color: var(--color-accent);
  color: var(--color-accent);
}
</style>
