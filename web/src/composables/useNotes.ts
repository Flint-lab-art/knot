// 列表状态（docs/WEB.md「数据层」）：不引 Pinia，模块级 ref 足够。
//
// 筛选与分页的唯一真值是 URL（?tag=&cursor=），组件只负责读写 URL 并调用这里。

import { ref } from 'vue'

import { getNotes } from '@/api/notes'
import { ApiError } from '@/api/client'
import type { NoteQuery, PublicNote } from '@/types/api'

const notes = ref<PublicNote[]>([])
const nextCursor = ref<string | undefined>(undefined)
const loading = ref(false)
const error = ref<unknown>(null)

export function useNotes() {
  /** 按参数加载一页；追加由 cursor 决定（无 cursor 即重置）。 */
  async function load(params: NoteQuery = {}): Promise<void> {
    loading.value = true
    error.value = null
    try {
      const res = await getNotes(params)
      notes.value = params.cursor ? [...notes.value, ...res.notes] : res.notes
      nextCursor.value = res.next_cursor || undefined
    } catch (err) {
      error.value = err
      if (!(err instanceof ApiError)) throw err
    } finally {
      loading.value = false
    }
  }

  /** 清空：切换标签或重试时先重置，避免旧数据短暂残留。 */
  function reset(): void {
    notes.value = []
    nextCursor.value = undefined
    error.value = null
  }

  return { notes, nextCursor, loading, error, load, reset }
}

/** 仅测试用：重置模块级状态，避免用例互相污染。 */
export function __resetNotesForTest(): void {
  notes.value = []
  nextCursor.value = undefined
  loading.value = false
  error.value = null
}
