// Command agmsg は共有 SQLite を通信路にした CLI AI エージェント間 IPC バイナリ。
//
// このファイルはサブコマンド dispatch とプロセス境界（シグナル・終了コード）の
// 配線のみを担い、実体は internal/cli にある (design.md §11)。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/ishii1648/agmsg-go/internal/cli"
)

func main() {
	// SIGINT / SIGTERM で ctx をキャンセルし、watch 等の長命処理を正常終了させる。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Run(ctx, cli.DefaultEnv(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "agmsg: %v\n", err)
		os.Exit(1)
	}
}
