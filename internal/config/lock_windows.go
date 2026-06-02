//go:build windows

package config

import (
	"os"

	"golang.org/x/sys/windows"

	"github.com/ishii1648/agmsg-go/internal/paths"
)

// withTeamLock は team 単位の排他ロックを取得して fn を実行する（Windows 実装）。
// 役割・契約は lock_unix.go と同一。Windows には flock が無いため LockFileEx
// (LOCKFILE_EXCLUSIVE_LOCK) を使う。flock 同様、ハンドルのクローズ / プロセス
// 終了でロックが解放されるため stale lock が残らない。design.md の
// 「クロスコンパイル維持」目標に沿って darwin/linux/windows をカバーする。
func withTeamLock(l paths.Layout, team string, fn func() error) error {
	if err := ValidateTeam(team); err != nil {
		return err
	}
	if err := l.EnsureTeamDir(team); err != nil {
		return err
	}
	f, err := os.OpenFile(l.TeamLockPath(team), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	h := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	// 先頭 1 バイトを排他ロック（EOF を越える範囲でも advisory lock として有効）。
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, ol); err != nil {
		return err
	}
	defer windows.UnlockFileEx(h, 0, 1, 0, ol)
	return fn()
}
