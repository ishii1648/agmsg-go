// Package identity は agmsg のアイデンティティモデル (design.md §6) を表現する。
//
// エージェントは (name, team) の組で識別される。project path と type
// (claude-code / codex / gemini / antigravity) は同一性判定には使わない
// メタデータであり、Registration として保持する。
//
// 同一性ロジックを文字列比較に散らさず、この 1 パッケージに型として固定する
// ことが目的（bash 版弱点 (1)/(2) の構造的封じ込め、design.md §3.1）。
package identity

import (
	"fmt"
	"sort"
)

// Identity はエージェントの同一性キー (name, team)。値型として比較できる。
type Identity struct {
	Name string
	Team string
}

func (id Identity) String() string {
	return fmt.Sprintf("%s@%s", id.Name, id.Team)
}

// Registration は同一性に影響しないメタデータ。
// 同じ (name, team) に対し、複数 project / type から参加すると積まれる。
type Registration struct {
	Type    string `json:"type"`
	Project string `json:"project"`
}

// ValidTypes は受理するエージェント種別 (design.md §6)。
var ValidTypes = []string{"claude-code", "codex", "gemini", "antigravity"}

// ValidType は t が既知のエージェント種別かを返す。
func ValidType(t string) bool {
	for _, v := range ValidTypes {
		if v == t {
			return true
		}
	}
	return false
}

// ValidateType は未知の種別ならエラーを返す。
func ValidateType(t string) error {
	if !ValidType(t) {
		return fmt.Errorf("unknown agent type %q (valid: %v)", t, ValidTypes)
	}
	return nil
}

// Match はこの Registration が指定の (type, project) と一致するかを返す。
func (r Registration) Match(typ, project string) bool {
	return r.Type == typ && r.Project == project
}

// Resolution は (type, project) からの identity 解決結果を表す。
type Resolution struct {
	Matches []Identity
}

// Single は一意に解決できた場合の identity を返す。
// 0 件・複数件なら ok=false。
func (r Resolution) Single() (Identity, bool) {
	if len(r.Matches) == 1 {
		return r.Matches[0], true
	}
	return Identity{}, false
}

// SortMatches は解決結果を (team, name) 順で安定化する（出力の決定性のため）。
func (r *Resolution) SortMatches() {
	sort.Slice(r.Matches, func(i, j int) bool {
		if r.Matches[i].Team != r.Matches[j].Team {
			return r.Matches[i].Team < r.Matches[j].Team
		}
		return r.Matches[i].Name < r.Matches[j].Name
	})
}
