package handler

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// cursorPage 是解析后的游标分页参数。
type cursorPage struct {
	cursorAt time.Time
	cursorID *uuid.UUID
}

// encodeCursor 把 (modified_at, id) 编成不透明游标：RFC3339Nano + "/" + uuid。
//
// 客户端只原样回传，不解析；不透明是为了以后换排序键时不必改 API。
func encodeCursor(at time.Time, id uuid.UUID) string {
	raw := at.UTC().Format(time.RFC3339Nano) + "/" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor 解析 encodeCursor 的结果。
func decodeCursor(s string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("游标不是合法 base64url: %w", err)
	}
	atStr, idStr, ok := strings.Cut(string(raw), "/")
	if !ok {
		return time.Time{}, uuid.Nil, fmt.Errorf("游标缺少分隔符")
	}
	at, err := time.Parse(time.RFC3339Nano, atStr)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("游标时间格式错误: %w", err)
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return time.Time{}, uuid.Nil, fmt.Errorf("游标 id 格式错误: %w", err)
	}
	return at, id, nil
}
