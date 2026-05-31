// Package cli は agmsg の各サブコマンドを実装する。
//
// 役割分担 (design.md §11):
//   - cmd/agmsg は dispatch のみ。
//   - cli は引数解析・identity 解決・出力整形といった「使い方」を持つが、
//     SQL は一切組み立てず internal/store 越しにのみ DB へ触れる。
//
// 識別子の解決方針: Tier 1 では (name, team) を明示フラグ／環境変数、もしくは
// 現在の (type, project) からの一意解決で確定する (design.md §6)。曖昧な場合は
// エラーにして利用者にフラグ指定を促す（policy をツールに埋めない / §2.1）。
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ishii1648/agmsg-go/internal/config"
	"github.com/ishii1648/agmsg-go/internal/identity"
	"github.com/ishii1648/agmsg-go/internal/paths"
	"github.com/ishii1648/agmsg-go/internal/store"
)

// 環境変数によるフォールバック。フラグ未指定時に参照する。
const (
	envName    = "AGMSG_NAME"
	envTeam    = "AGMSG_TEAM"
	envType    = "AGMSG_TYPE"
	envProject = "AGMSG_PROJECT"
)

const defaultType = "claude-code"

// tsLayout は created_at / read_at と揃えた ISO-8601 (UTC)。
const tsLayout = "2006-01-02T15:04:05Z"

// Env は I/O とクロックを注入可能にした実行環境（テスト容易性のため）。
type Env struct {
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time
}

// DefaultEnv は実プロセス用の Env を返す。
func DefaultEnv() Env {
	return Env{Stdout: os.Stdout, Stderr: os.Stderr, Now: time.Now}
}

func (e Env) nowISO() string { return e.Now().UTC().Format(tsLayout) }

// flags は `--key value` / `--key=value` を args 中の任意位置から抜き出した結果。
// 残りの位置引数を pos に残す。stdlib flag が位置引数の後ろのフラグを解さない
// 制約を避け、`send <to> <body> --from x` のような自然な並びを許すための簡易解析。
type flags struct {
	opts map[string]string
	pos  []string
}

func parseFlags(args []string) flags {
	f := flags{opts: map[string]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "--") {
			key := a[2:]
			if eq := strings.IndexByte(key, '='); eq >= 0 {
				f.opts[key[:eq]] = key[eq+1:]
				continue
			}
			// 次トークンを値として消費（次がフラグでない限り）。
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				f.opts[key] = args[i+1]
				i++
			} else {
				f.opts[key] = "" // 値なしフラグ
			}
			continue
		}
		f.pos = append(f.pos, a)
	}
	return f
}

func (f flags) get(key, env, def string) string {
	if v, ok := f.opts[key]; ok && v != "" {
		return v
	}
	if env != "" {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	return def
}

// agentType は --type / AGMSG_TYPE / 既定 claude-code を解決する。
func (f flags) agentType() string { return f.get("type", envType, defaultType) }

// project は --project / AGMSG_PROJECT / カレントディレクトリを解決する。
func (f flags) project() string {
	if v := f.get("project", envProject, ""); v != "" {
		return v
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// resolveSelf は送信者／受信者となる自アイデンティティ (name, team) を確定する。
//   - --name / --team（または env）が両方あればそれを使う。
//   - 無ければ現在の (type, project) から config を走査して一意解決する。
//   - 曖昧・未解決ならフラグ指定を促すエラーを返す。
func resolveSelf(l paths.Layout, f flags) (identity.Identity, error) {
	name := f.get("name", envName, "")
	team := f.get("team", envTeam, "")
	if name != "" && team != "" {
		return identity.Identity{Name: name, Team: team}, nil
	}

	res, err := config.Resolve(l, f.agentType(), f.project())
	if err != nil {
		return identity.Identity{}, err
	}
	// 片側だけフラグ指定された場合は解決結果を絞り込む。
	if name != "" || team != "" {
		var filtered []identity.Identity
		for _, m := range res.Matches {
			if (name == "" || m.Name == name) && (team == "" || m.Team == team) {
				filtered = append(filtered, m)
			}
		}
		res.Matches = filtered
	}

	if id, ok := res.Single(); ok {
		return id, nil
	}
	if len(res.Matches) == 0 {
		return identity.Identity{}, fmt.Errorf(
			"could not resolve identity for (type=%s, project=%s); run `agmsg join <team> <name>` first, or pass --name and --team",
			f.agentType(), f.project())
	}
	return identity.Identity{}, fmt.Errorf(
		"identity is ambiguous (%d matches); narrow it with --name and --team", len(res.Matches))
}

// openStore は DB ディレクトリを用意して store を開く。
func openStore(ctx context.Context, l paths.Layout) (*store.Store, error) {
	if err := l.EnsureDBDir(); err != nil {
		return nil, err
	}
	return store.Open(ctx, l.DBPath())
}

// formatMessage は design.md §7 の 1 行表現を返す:
//
//	<ts> | <team> | <from> → <to> | <body>
//
// body は改行を含みうる (design.md §5.1) が、watch / inbox は 1 行 = 1 レコードの
// ストリームなので、改行をそのまま流すと購読側のレコード境界が壊れる。body 内の
// 改行を可逆エスケープ（\\ → \\\\、改行 → \n / \r）して 1 行を保つ。
func formatMessage(m store.Message) string {
	return fmt.Sprintf("%s | %s | %s → %s | %s", m.CreatedAt, m.Team, m.From, m.To, escapeLine(m.Body))
}

// escapeLine は 1 行プロトコルを壊す制御文字を可逆にエスケープする。
func escapeLine(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}

// Run は引数列を受けてサブコマンドへ dispatch する。args は os.Args[1:] 相当。
func Run(ctx context.Context, e Env, args []string) error {
	l, err := paths.Default()
	if err != nil {
		return fmt.Errorf("resolve agmsg home: %w", err)
	}
	if len(args) == 0 {
		printUsage(e.Stderr)
		return fmt.Errorf("no subcommand given")
	}
	sub, rest := args[0], args[1:]
	f := parseFlags(rest)

	switch sub {
	case "send":
		return cmdSend(ctx, e, l, f)
	case "inbox":
		return cmdInbox(ctx, e, l, f)
	case "watch":
		return cmdWatch(ctx, e, l, f)
	case "join":
		return cmdJoin(ctx, e, l, f)
	case "leave":
		return cmdLeave(ctx, e, l, f)
	case "whoami":
		return cmdWhoami(ctx, e, l, f)
	case "help", "-h", "--help":
		printUsage(e.Stdout)
		return nil
	default:
		printUsage(e.Stderr)
		return fmt.Errorf("unknown subcommand %q", sub)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `agmsg — CLI AI エージェント間 IPC (Tier 1)

Usage:
  agmsg send  <to> <body> [--from <name>] [--team <team>]
  agmsg inbox             [--name <name>] [--team <team>]
  agmsg watch             [--name <name>] [--team <team>] [--interval <dur>]
  agmsg join  <team> <name> [--type <type>] [--project <path>]
  agmsg leave <team> <name> [--type <type>] [--project <path>]
  agmsg whoami            [--type <type>] [--project <path>]

識別子 (name, team) はフラグ／環境変数 (AGMSG_NAME, AGMSG_TEAM, AGMSG_TYPE,
AGMSG_PROJECT)、または現在の (type, project) からの一意解決で確定します。
DB / 名簿の置き場所は AGMSG_HOME で上書きできます（既定 ~/.agents/skills/agmsg）。
`)
}
