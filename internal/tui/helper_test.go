package tui

import "time"

// timeoutAfter 限制测试中等待 Run 返回的时间，防止事件循环把 make check 挂死。
const timeoutAfter = 3 * time.Second
