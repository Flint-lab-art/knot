// Package handler 实现 /api/* 的 HTTP handler。
//
// 无 service 层：handler 直接调用 store 接口；公开接口在查询层强制
// visibility='public'（标签承载可见性是禁止的设计）。
package handler
