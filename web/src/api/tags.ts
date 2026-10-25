// 公开标签接口。

import { getJSON } from './client'
import type { PublicTag } from '@/types/api'

/** GET /api/public/tags：只返回至少关联一篇 public 笔记的标签。 */
export function getTags(): Promise<PublicTag[]> {
  return getJSON<PublicTag[]>('/tags')
}
