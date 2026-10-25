// 与 docs/API.md 的公开接口 DTO 对齐。
//
// 公开接口只返回这些字段：不出现 version / visibility / modified_at。
// 任何字段变动都要先改 docs/API.md 与 Go 端的 dto.PublicNote，再同步这里。

/** 公开笔记的元数据（列表项）。 */
export interface PublicNote {
  id: string
  title: string
  /** RFC3339 UTC 字符串，展示时再本地化。 */
  created_at: string
  tags: string[]
}

/** 公开笔记的完整内容（阅读页）。 */
export interface PublicNoteDetail extends PublicNote {
  raw_md: string
}

/** 公开接口的标签。 */
export interface PublicTag {
  id: string
  name: string
}

/** 游标分页响应：next_cursor 缺失/为空表示没有下一页。 */
export interface PublicNoteList {
  notes: PublicNote[]
  next_cursor?: string
}

/** 列表页查询参数。 */
export interface NoteQuery {
  tag?: string
  cursor?: string
  limit?: number
}

/** 服务端统一错误体 {"code":..,"msg":..}。 */
export interface ApiErrorBody {
  code: number
  msg: string
}
