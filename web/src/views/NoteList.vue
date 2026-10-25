<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'

import { errorMessage, isRetryable } from '@/api/client'
import NoteCard from '@/components/NoteCard.vue'
import Pagination from '@/components/Pagination.vue'
import TagBar from '@/components/TagBar.vue'
import { useNotes } from '@/composables/useNotes'
import { useTags } from '@/composables/useTags'

// 列表页：筛选与分页的唯一真值是 URL（docs/WEB.md）。
const route = useRoute()
const { notes, nextCursor, loading, error, load, reset } = useNotes()
const { tags, load: loadTags } = useTags()

// 游标分页没有页码，「上一页」需要知道来路：会话内维护访问过的游标序列
// （第一页记为 undefined）。刷新后序列为空，此时只给「下一页」，
// 不编造一个错误的返回位置。
const visited = ref<(string | undefined)[]>([undefined])

const activeTag = computed(() =>
  typeof route.query.tag === 'string' ? route.query.tag : undefined,
)
const cursor = computed(() =>
  typeof route.query.cursor === 'string' ? route.query.cursor : undefined,
)
const prevCursor = computed(() => {
  const idx = visited.value.indexOf(cursor.value)
  return idx > 0 ? visited.value[idx - 1] : undefined
})
const showEmpty = computed(() => !loading.value && !error.value && notes.value.length === 0)

/** 记录一次访问：前进时追加，后退时截断。 */
function trackCursor(next: string | undefined, prev: string | undefined) {
  const list = visited.value
  const idx = list.indexOf(prev)
  if (idx >= 0) {
    list.length = idx + 1
  }
  if (list.indexOf(next) < 0) {
    visited.value = [...list, next]
  }
}

async function loadFromRoute() {
  reset()
  await load({ tag: activeTag.value, cursor: cursor.value, limit: 20 })
}

watch(
  () => [route.query.tag, route.query.cursor] as const,
  ([nextTag, nextCursor], [prevTag, prevCursorVal]) => {
    if (nextTag !== prevTag) {
      visited.value = [undefined]
    } else {
      trackCursor(
        typeof nextCursor === 'string' ? nextCursor : undefined,
        typeof prevCursorVal === 'string' ? prevCursorVal : undefined,
      )
    }
    void loadFromRoute()
  },
)

onMounted(async () => {
  await Promise.all([loadTags(), loadFromRoute()])
})
</script>

<template>
  <section class="list">
    <TagBar :tags="tags" :active="activeTag" />

    <p v-if="loading" class="state" role="status">加载中…</p>

    <div v-else-if="error" class="state state--error" role="alert">
      <p>{{ errorMessage(error) }}</p>
      <button v-if="isRetryable(error)" class="retry" type="button" @click="loadFromRoute">
        重试
      </button>
    </div>

    <p v-else-if="showEmpty" class="state">
      {{ activeTag ? `标签「${activeTag}」下还没有公开笔记` : '还没有公开笔记' }}
    </p>

    <template v-else>
      <div class="cards">
        <NoteCard v-for="note in notes" :key="note.id" :note="note" />
      </div>
      <Pagination
        :tag="activeTag"
        :cursor="nextCursor"
        :prev-cursor="prevCursor"
        :has-next="Boolean(nextCursor)"
      />
    </template>
  </section>
</template>

<style scoped>
.list {
  padding-bottom: 48px;
}

.cards {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.state {
  margin: 48px 0;
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
