#!/usr/bin/env python3
"""TUI 的 PTY 驱动验收（scripts/e2e-tui.py）。

用伪终端启动 `knot tui`，发送按键并断言屏幕内容。之所以不用 unittest
直接调 Model：这部分验证的是「真实终端下能跑起来、能渲染、能退出」，
单测已经覆盖了状态机逻辑，这里补的是集成层。

用法：
    python3 scripts/e2e-tui.py --config /path/to/client.toml --knot /path/to/knot

退出码 0 表示全部通过。
"""

import argparse
import os
import pty
import re
import select
import struct
import fcntl
import termios
import sys
import time

PASS = 0
FAIL = 0
ANSI = re.compile(
    r"\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][B0]|\x1b[=>]"
)


def ok(msg):
    global PASS
    PASS += 1
    print(f"  \033[32m✓\033[0m {msg}")


def bad(msg):
    global FAIL
    FAIL += 1
    print(f"  \033[31m✗\033[0m {msg}")


def check(desc, cond, detail=""):
    if cond:
        ok(desc)
    else:
        bad(f"{desc}{(' — ' + detail) if detail else ''}")


def strip(text):
    return ANSI.sub("", text)


class Tui:
    """一个跑在 pty 里的 knot tui 会话。"""

    def __init__(self, knot, config, cols=120, rows=30):
        env = dict(os.environ)
        env["KNOT_CONFIG"] = config
        self.pid, self.fd = pty.fork()
        if self.pid == 0:
            os.execvpe(knot, [knot, "tui"], env)
            os._exit(127)
        fcntl.ioctl(
            self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0)
        )
        self.raw = ""

    def pump(self, seconds, answer_queries=True):
        """读取输出；自动回答终端查询（背景色/光标位置），否则 Bubble Tea 会等。"""
        end = time.time() + seconds
        while time.time() < end:
            r, _, _ = select.select([self.fd], [], [], 0.05)
            if not r:
                continue
            try:
                chunk = os.read(self.fd, 65536)
            except OSError:
                return
            if not chunk:
                return
            text = chunk.decode("utf-8", "replace")
            self.raw += text
            if answer_queries:
                if "\x1b]11;?" in text:
                    os.write(self.fd, b"\x1b]11;rgb:1e1e/1e1e/1e1e\x1b\\")
                if "\x1b[6n" in text:
                    os.write(self.fd, b"\x1b[1;1R")

    def send(self, keys, settle=0.6):
        os.write(self.fd, keys)
        self.pump(settle)

    def screen(self):
        """把累积输出规范化成可断言的文本（去掉控制序列）。"""
        return strip(self.raw)

    def clear(self):
        self.raw = ""

    def wait_exit(self, timeout=5.0):
        end = time.time() + timeout
        while time.time() < end:
            wpid, _ = os.waitpid(self.pid, os.WNOHANG)
            if wpid == self.pid:
                return True
            self.pump(0.1)
        return False

    def kill(self):
        try:
            os.kill(self.pid, 9)
        except ProcessLookupError:
            pass
        try:
            os.waitpid(self.pid, 0)
        except ChildProcessError:
            pass


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--knot", required=True)
    ap.add_argument("--config", required=True)
    args = ap.parse_args()

    print("\n\033[34m== TUI 交互（PTY） ==\033[0m")
    tui = Tui(args.knot, args.config)
    try:
        tui.pump(3.0)
        first = tui.screen()

        # 首次渲染
        check("TUI 启动并渲染出列表", "笔记" in first or "没有笔记" in first,
              repr(first[-200:]))
        check("渲染出状态栏与快捷键", "q 退出" in first and "s 同步" in first)
        check("同步状态出现在状态栏", "已同步" in first or "同步中" in first)

        # 双栏：有竖直分隔线
        check("双栏布局有分隔线（120 列）", "│" in first)

        # 下移
        tui.clear()
        tui.send(b"j")
        moved = tui.screen()
        check("按 j 后仍正常渲染（无报错）", "加载中" not in moved or "正文" in moved)

        # 标签面板
        tui.clear()
        tui.send(b"t")
        panel = tui.screen()
        check("按 t 打开标签面板", "标签" in panel and "Esc 关闭" in panel,
              repr(panel[-160:]))

        # 关闭面板
        tui.clear()
        tui.send(b"\x1b")
        tui.pump(0.4)
        check("Esc 关闭标签面板后回到列表", "q 退出" in tui.screen())

        # 搜索
        tui.clear()
        tui.send(b"/")
        tui.send(b"no-such-title")
        typed = tui.screen()
        check("搜索框接受输入", "no-such-title" in typed, repr(typed[-160:]))
        tui.clear()
        tui.send(b"\x1b")
        tui.pump(0.4)
        check("Esc 取消搜索", "q 退出" in tui.screen())

        # 自动同步
        tui.clear()
        tui.send(b"s", settle=1.5)
        synced = tui.screen()
        check("按 s 触发同步并显示结果",
              "同步中" in synced or "已同步" in synced or "同步失败" in synced,
              repr(synced[-160:]))

        # 退出
        tui.clear()
        tui.send(b"q", settle=0.3)
        exited = tui.wait_exit(timeout=5.0)
        check("按 q 正常退出（进程结束）", exited)
    finally:
        tui.kill()

    # 窄终端：单栏
    print("\n\033[34m== TUI 窄终端（80 列） ==\033[0m")
    narrow = Tui(args.knot, args.config, cols=80, rows=24)
    try:
        narrow.pump(3.0)
        screen = narrow.screen()
        check("窄终端启动成功", "q 退出" in screen, repr(screen[-200:]))
        check("窄终端不渲染双栏分隔线", "│" not in screen)
        narrow.send(b"q", settle=0.3)
        check("窄终端按 q 正常退出", narrow.wait_exit(timeout=5.0))
    finally:
        narrow.kill()

    print()
    color = "\033[32m" if FAIL == 0 else "\033[31m"
    print(f"  {color}通过 {PASS} / 失败 {FAIL}\033[0m")
    return 0 if FAIL == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
