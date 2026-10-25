// Command knot 是 knot 的 CLI / TUI 入口。
package main

import (
	"fmt"
	"os"

	"knot/internal/cli"
)

func main() {
	if err := cli.NewRootCmd(os.Stdout, os.Stderr).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "knot:", err)
		os.Exit(1)
	}
}
