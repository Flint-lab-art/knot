package server

import (
	"crypto/rand"
	"encoding/hex"
)

// newRequestID 生成 16 字节随机 id（32 位十六进制）。
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 只影响排障关联性，退回时间戳也不影响功能。
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}
