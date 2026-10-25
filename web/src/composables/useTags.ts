// 标签状态：列表页顶部标签条用。

import { ref } from 'vue'

import { getTags } from '@/api/tags'
import { ApiError } from '@/api/client'
import type { PublicTag } from '@/types/api'

const tags = ref<PublicTag[]>([])
const loading = ref(false)
const error = ref<unknown>(null)
let loaded = false

export function useTags() {
  /** 加载标签；已加载过则直接返回（标签变更是低频事件）。 */
  async function load(force = false): Promise<void> {
    if (loaded && !force) return
    loading.value = true
    error.value = null
    try {
      tags.value = await getTags()
      loaded = true
    } catch (err) {
      error.value = err
      if (!(err instanceof ApiError)) throw err
    } finally {
      loading.value = false
    }
  }

  return { tags, loading, error, load }
}

/** 仅测试用。 */
export function __resetTagsForTest(): void {
  tags.value = []
  loading.value = false
  error.value = null
  loaded = false
}
