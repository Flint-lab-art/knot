package handler

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"knot/internal/store/pg"
)

func TestCursorRoundTrip(t *testing.T) {
	base := time.Date(2026, 10, 24, 10, 0, 0, 123456789, time.UTC)
	id := uuid.New()

	got, gotID, err := decodeCursor(encodeCursor(base, id))
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	if !got.Equal(base) {
		t.Fatalf("时间往返不一致: %v != %v", got, base)
	}
	if gotID != id {
		t.Fatalf("id 往返不一致: %v != %v", gotID, id)
	}

	// 非 UTC 输入也要还原同一时刻。
	shanghai := time.FixedZone("CST", 8*3600)
	at := time.Date(2026, 10, 24, 18, 0, 0, 0, shanghai)
	got, _, err = decodeCursor(encodeCursor(at, id))
	if err != nil || !got.Equal(at) {
		t.Fatalf("非 UTC 时间往返失败: %v %v", got, err)
	}

	valid := encodeCursor(base, id)
	bad := map[string]string{
		"非 base64": "@@@",
		"缺分隔符":     base64.RawURLEncoding.EncodeToString([]byte("noslash")),
		"时间格式错误":   base64.RawURLEncoding.EncodeToString([]byte("not-a-time/" + id.String())),
		"id 格式错误":  base64.RawURLEncoding.EncodeToString([]byte(base.Format(time.RFC3339Nano) + "/not-a-uuid")),
		"空串":       "",
		"被截断":      valid[:len(valid)-4],
		"空时间与 id":  base64.RawURLEncoding.EncodeToString([]byte("/")),
	}
	for name, cur := range bad {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeCursor(cur); err == nil {
				t.Fatalf("非法游标 %q 应报错", cur)
			}
		})
	}
}

// nextCursor 只在整页取满时给出；不满一页表示到底了。
func TestNextCursor(t *testing.T) {
	now := time.Now().UTC()
	mk := func(n int) []pg.Note {
		out := make([]pg.Note, n)
		for i := range out {
			out[i] = pg.Note{ID: uuid.New(), ModifiedAt: now.Add(-time.Duration(i) * time.Minute)}
		}
		return out
	}
	if got := nextCursor(nil, 2); got != "" {
		t.Fatalf("空页不应给游标，实际 %q", got)
	}
	if got := nextCursor(mk(1), 2); got != "" {
		t.Fatalf("不满一页不应给游标，实际 %q", got)
	}
	page := mk(2)
	got := nextCursor(page, 2)
	if got == "" {
		t.Fatal("取满一页应给游标")
	}
	// 游标必须指向本页最后一条。
	at, id, err := decodeCursor(got)
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	if !at.Equal(page[1].ModifiedAt) || id != page[1].ID {
		t.Fatalf("游标指向错误的记录: %v %v", at, id)
	}
}

func TestNoteIDs(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	ids := noteIDs([]pg.Note{{ID: a}, {ID: b}})
	if len(ids) != 2 || ids[0] != a || ids[1] != b {
		t.Fatalf("noteIDs = %v", ids)
	}
	if ids := noteIDs(nil); len(ids) != 0 {
		t.Fatalf("空输入应返回空切片: %v", ids)
	}
}

// 错误信息不回显原始输入。
func TestCursorErrorsDoNotEchoInput(t *testing.T) {
	secret := strings.Repeat("s", 40)
	if _, _, err := decodeCursor(secret); err == nil {
		t.Fatal("应报错")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("错误信息回显了输入: %v", err)
	}
}
