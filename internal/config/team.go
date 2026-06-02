// Package config は teams/<team>/config.json (チーム名簿) の読み書きを担う。
//
// 構造はオリジナル fujibee/agmsg と互換に保つ (design.md §5.3):
//
//	{
//	  "name": "<team>",
//	  "agents": {
//	    "<name>": { "registrations": [ { "type": ..., "project": ... } ] }
//	  },
//	  "created_at": "<ISO-8601>"
//	}
//
// agents は name でキーされ、各エントリは Registration の配列を持つ。
// 同一性 (name, team) は identity パッケージが定義し、ここは永続化のみ扱う。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ishii1648/agmsg-go/internal/identity"
	"github.com/ishii1648/agmsg-go/internal/paths"
)

// ValidateTeam は team 名が単一の安全なパス要素であることを保証する。
// team は teams/<team>/config.json のディレクトリ名になるため、未検証だと
// "../" やパス区切りで AGMSG_HOME 外の任意 config を読み書きできてしまう。
// 入口（Join / Leave / LoadTeam）で弾く。
func ValidateTeam(team string) error {
	if team == "" || team == "." || team == ".." ||
		strings.ContainsRune(team, '/') ||
		strings.ContainsRune(team, filepath.Separator) ||
		strings.ContainsRune(team, '\x00') ||
		filepath.Base(team) != team {
		return fmt.Errorf("invalid team name %q (must be a single path segment)", team)
	}
	return nil
}

// AgentEntry は config.json 内の 1 エージェント分の登録情報。
type AgentEntry struct {
	Registrations []identity.Registration `json:"registrations"`
}

// TeamConfig は teams/<team>/config.json 全体に対応する。
type TeamConfig struct {
	Name      string                `json:"name"`
	Agents    map[string]AgentEntry `json:"agents"`
	CreatedAt string                `json:"created_at"`
}

// LoadTeam は指定チームの config を読み込む。未作成なら ok=false を返す
// （エラーではない）。
func LoadTeam(l paths.Layout, team string) (TeamConfig, bool, error) {
	if err := ValidateTeam(team); err != nil {
		return TeamConfig{}, false, err
	}
	b, err := os.ReadFile(l.TeamConfigPath(team))
	if errors.Is(err, os.ErrNotExist) {
		return TeamConfig{}, false, nil
	}
	if err != nil {
		return TeamConfig{}, false, err
	}
	var tc TeamConfig
	if err := json.Unmarshal(b, &tc); err != nil {
		return TeamConfig{}, false, fmt.Errorf("parse %s: %w", l.TeamConfigPath(team), err)
	}
	if tc.Agents == nil {
		tc.Agents = map[string]AgentEntry{}
	}
	return tc, true, nil
}

// Save は config を team ディレクトリへアトミックに書き出す（インデント付き）。
//
// 同一ディレクトリに temp ファイルを書いてから os.Rename で差し替える。
// intra-directory rename は atomic なので、書き込み途中でプロセスが死んでも
// 本体ファイルは常に valid JSON を保ち（torn JSON で後続 LoadTeam が落ちない）、
// reader は旧 / 新どちらか完全な内容だけを見る。lost update の防止は呼び出し側
// （withTeamLock）が担う。mode は private home 下で必要十分な 0o600
// （os.CreateTemp の既定）。
func (tc TeamConfig) Save(l paths.Layout, team string) error {
	if err := l.EnsureTeamDir(team); err != nil {
		return err
	}
	b, err := json.MarshalIndent(tc, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	dir := filepath.Dir(l.TeamConfigPath(team))
	tmp, err := os.CreateTemp(dir, "config-*.tmp")
	if err != nil {
		return err
	}
	// rename 成功後は no-op（Remove は存在しない名前で error を返すだけ）。
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), l.TeamConfigPath(team))
}

// nowISO は created_at に使う UTC タイムスタンプ。
func nowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

// Join は (name) を team に登録し、(type, project) の Registration を加える。
// チーム config が無ければ新規作成する。同一 (type, project) の重複は無視する。
// 新規に登録された場合 added=true を返す。
func Join(l paths.Layout, team, name string, reg identity.Registration) (added bool, err error) {
	if err := identity.ValidateType(reg.Type); err != nil {
		return false, err
	}
	// load → mutate → save 全体を team ロックで囲み、並行 Join/Leave による
	// lost update を防ぐ。
	err = withTeamLock(l, team, func() error {
		tc, ok, err := LoadTeam(l, team)
		if err != nil {
			return err
		}
		if !ok {
			tc = TeamConfig{Name: team, Agents: map[string]AgentEntry{}, CreatedAt: nowISO()}
		}
		entry := tc.Agents[name]
		for _, r := range entry.Registrations {
			if r.Match(reg.Type, reg.Project) {
				return nil // 既に登録済み（added=false のまま）
			}
		}
		entry.Registrations = append(entry.Registrations, reg)
		tc.Agents[name] = entry
		if err := tc.Save(l, team); err != nil {
			return err
		}
		added = true
		return nil
	})
	return added, err
}

// Leave は team から (name) の (type, project) Registration を取り除く。
// 残り Registration が無くなれば agent エントリ自体を削除する。
// 何か取り除いた場合 removed=true を返す。
func Leave(l paths.Layout, team, name string, reg identity.Registration) (removed bool, err error) {
	// load → mutate → save 全体を team ロックで囲む（Join と同様）。
	err = withTeamLock(l, team, func() error {
		tc, ok, err := LoadTeam(l, team)
		if err != nil || !ok {
			return err
		}
		entry, exists := tc.Agents[name]
		if !exists {
			return nil
		}
		kept := entry.Registrations[:0]
		for _, r := range entry.Registrations {
			if r.Match(reg.Type, reg.Project) {
				removed = true
				continue
			}
			kept = append(kept, r)
		}
		if !removed {
			return nil
		}
		if len(kept) == 0 {
			delete(tc.Agents, name)
		} else {
			entry.Registrations = kept
			tc.Agents[name] = entry
		}
		return tc.Save(l, team)
	})
	if err != nil {
		return false, err
	}
	return removed, nil
}

// ListTeams は teams ディレクトリに存在するチーム名を返す。
// team の存在は config.json の有無で定義する。Leave がロック取得のために作る
// config.lock だけの空ディレクトリ（登録 0 件）は team として数えない。
func ListTeams(l paths.Layout) ([]string, error) {
	ents, err := os.ReadDir(l.TeamsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var teams []string
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(l.TeamConfigPath(e.Name())); err != nil {
			continue // config.json が無いディレクトリは team ではない
		}
		teams = append(teams, e.Name())
	}
	return teams, nil
}

// Resolve は全チームを走査し、(type, project) に一致する Registration を持つ
// エージェントの identity を集める (design.md §6 の whoami 相当)。
func Resolve(l paths.Layout, typ, project string) (identity.Resolution, error) {
	teams, err := ListTeams(l)
	if err != nil {
		return identity.Resolution{}, err
	}
	var res identity.Resolution
	for _, team := range teams {
		tc, ok, err := LoadTeam(l, team)
		if err != nil {
			return identity.Resolution{}, err
		}
		if !ok {
			continue
		}
		for name, entry := range tc.Agents {
			for _, r := range entry.Registrations {
				if r.Match(typ, project) {
					res.Matches = append(res.Matches, identity.Identity{Name: name, Team: team})
					break
				}
			}
		}
	}
	res.SortMatches()
	return res, nil
}
