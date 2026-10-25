// 公开笔记接口（docs/API.md「公开只读接口」）。

import { buildQuery, getJSON } from './client'
import type { NoteQuery, PublicNoteDetail, PublicNoteList } from '@/types/api'

/** GET /api/public/notes：元数据列表，游标分页。 */
export function getNotes(params: NoteQuery = {}): Promise<PublicNoteList> {
  const query = buildQuery({ tag: params.tag, cursor: params.cursor, limit: params.limit })
  return getJSON<PublicNoteList>(`/notes${query}`)
}

/** GET /api/public/notes/:id：完整内容；非 public 或不存在都抛 404。 */
export function getNote(id: string): Promise<PublicNoteDetail> {
  return getJSON<PublicNoteDetail>(`/notes/${encodeURIComponent(id)}`)
}
