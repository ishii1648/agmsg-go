//go:build unix

package config

import (
	"os"
	"syscall"

	"github.com/ishii1648/agmsg-go/internal/paths"
)

// withTeamLock は team 単位の排他ロックを取得して fn を実行する。
// Join / Leave の read-modify-write 全体をこのロックで囲み、並行更新による
// lost update (TOCTOU) を防ぐ。
//
// ロックは flock(LOCK_EX)。fcntl(POSIX) ロックと違い別 fd 間（同一プロセス内の
// goroutine 含む）でも排他し、かつ fd close / プロセス終了でカーネルが自動解放
// するため、ホルダがクラッシュしても stale lock が残らない（mkdir / O_EXCL 系の
// 残留ロックによる local DoS を避ける狙い。issues/0008 参照）。
//
// 粒度は team 単位（ロックファイルが team ディレクトリ内）に留め、全体直列化は
// 避ける。unix(darwin/linux) 限定。他 OS が必要になれば lock_<os>.go を足す
// （paths.go の OS 局所化方針に揃える）。
func withTeamLock(l paths.Layout, team string, fn func() error) error {
	if err := ValidateTeam(team); err != nil {
		return err
	}
	// 新規 team でもロックファイルを置けるよう先にディレクトリを用意する。
	// MkdirAll は並行呼び出しに対して安全。
	if err := l.EnsureTeamDir(team); err != nil {
		return err
	}
	f, err := os.OpenFile(l.TeamLockPath(team), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
