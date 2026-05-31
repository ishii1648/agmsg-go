package cli

import (
	"context"

	"github.com/ishii1648/agmsg-go/internal/paths"
	"github.com/ishii1648/agmsg-go/internal/upgrade"
)

// cmdUpgrade は実行中の agmsg を GitHub Releases の最新版で置き換える。
// IPC プリミティブそのものではないが、単一バイナリ配布（issue 0003）を自己更新で
// 完結させる運用補助コマンド。--check は最新版の確認だけ行い置き換えはしない。
func cmdUpgrade(_ context.Context, e Env, _ paths.Layout, f flags) error {
	return upgrade.Run(upgrade.Options{
		CurrentVersion: version,
		CheckOnly:      f.get("check", "", "") == "true",
		Out:            e.Stdout,
	})
}
