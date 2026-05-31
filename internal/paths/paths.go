// Package paths は agmsg のファイルレイアウト（~/.agents/skills/agmsg 配下）を
// 解決する。共有 SQLite ファイルや teams config の物理パスを 1 箇所に集約し、
// 上位層がパス文字列を組み立てないようにする。
//
// design.md §4 / §5 のレイアウトに従う:
//
//	<root>/db/messages.db          メッセージ DB（WAL）
//	<root>/teams/<team>/config.json  チーム名簿
//
// root は既定で ~/.agents/skills/agmsg。環境変数 AGMSG_HOME で上書きできる
// （テスト・複数インスタンス分離のため）。
//
// OS 差はビルドタグで局所化する方針（design.md §11）だが、Tier 1 が扱う
// ~/.agents は darwin / linux で同一規約のため、現時点では分岐を持たない。
// 実際の OS 差が生じた時点で paths_<os>.go を追加する。
package paths

import (
	"os"
	"path/filepath"
)

// EnvHome は root を上書きする環境変数名。
const EnvHome = "AGMSG_HOME"

// Layout は agmsg のファイルレイアウトを表す。root を起点に各パスを導出する。
type Layout struct {
	root string
}

// Default は AGMSG_HOME（未設定なら ~/.agents/skills/agmsg）を root とする
// Layout を返す。ホームディレクトリが解決できない場合のみエラーを返す。
func Default() (Layout, error) {
	if h := os.Getenv(EnvHome); h != "" {
		return Layout{root: h}, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, err
	}
	return Layout{root: filepath.Join(home, ".agents", "skills", "agmsg")}, nil
}

// New は明示した root を起点とする Layout を返す（主にテスト用）。
func New(root string) Layout { return Layout{root: root} }

// Root は起点ディレクトリを返す。
func (l Layout) Root() string { return l.root }

// DBPath は messages.db の絶対パスを返す。
func (l Layout) DBPath() string {
	return filepath.Join(l.root, "db", "messages.db")
}

// DBDir は DB を置くディレクトリを返す。
func (l Layout) DBDir() string {
	return filepath.Join(l.root, "db")
}

// TeamsDir は全チーム config を収めるディレクトリを返す。
func (l Layout) TeamsDir() string {
	return filepath.Join(l.root, "teams")
}

// TeamConfigPath は指定チームの config.json の絶対パスを返す。
func (l Layout) TeamConfigPath(team string) string {
	return filepath.Join(l.root, "teams", team, "config.json")
}

// EnsureDBDir は DB ディレクトリを作成する（既存なら何もしない）。
func (l Layout) EnsureDBDir() error {
	return os.MkdirAll(l.DBDir(), 0o755)
}

// EnsureTeamDir は指定チームの config ディレクトリを作成する。
func (l Layout) EnsureTeamDir(team string) error {
	return os.MkdirAll(filepath.Join(l.root, "teams", team), 0o755)
}
