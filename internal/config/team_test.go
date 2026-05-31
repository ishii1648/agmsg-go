package config

import (
	"testing"

	"github.com/ishii1648/agmsg-go/internal/identity"
	"github.com/ishii1648/agmsg-go/internal/paths"
)

func tempLayout(t *testing.T) paths.Layout {
	t.Helper()
	return paths.New(t.TempDir())
}

func TestJoinAndLoad(t *testing.T) {
	l := tempLayout(t)
	reg := identity.Registration{Type: "claude-code", Project: "/repo/a"}

	added, err := Join(l, "alpha", "reviewer", reg)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if !added {
		t.Fatal("first Join should add")
	}

	tc, ok, err := LoadTeam(l, "alpha")
	if err != nil || !ok {
		t.Fatalf("LoadTeam: ok=%v err=%v", ok, err)
	}
	if tc.Name != "alpha" {
		t.Errorf("team name = %q, want alpha", tc.Name)
	}
	if len(tc.Agents["reviewer"].Registrations) != 1 {
		t.Fatalf("registrations = %d, want 1", len(tc.Agents["reviewer"].Registrations))
	}
}

func TestJoinDedupe(t *testing.T) {
	l := tempLayout(t)
	reg := identity.Registration{Type: "codex", Project: "/repo/a"}

	if _, err := Join(l, "alpha", "bob", reg); err != nil {
		t.Fatal(err)
	}
	added, err := Join(l, "alpha", "bob", reg) // 同一 (type, project)
	if err != nil {
		t.Fatal(err)
	}
	if added {
		t.Error("duplicate registration must not be added")
	}

	tc, _, _ := LoadTeam(l, "alpha")
	if len(tc.Agents["bob"].Registrations) != 1 {
		t.Errorf("registrations = %d, want 1 (dedupe failed)", len(tc.Agents["bob"].Registrations))
	}
}

func TestJoinMultipleRegistrations(t *testing.T) {
	l := tempLayout(t)
	// 同一 (name, team) に別 project から参加 → registrations が積まれる (design.md §6)。
	Join(l, "alpha", "bob", identity.Registration{Type: "codex", Project: "/repo/a"})
	Join(l, "alpha", "bob", identity.Registration{Type: "codex", Project: "/repo/b"})

	tc, _, _ := LoadTeam(l, "alpha")
	if len(tc.Agents["bob"].Registrations) != 2 {
		t.Errorf("registrations = %d, want 2", len(tc.Agents["bob"].Registrations))
	}
}

func TestJoinInvalidType(t *testing.T) {
	l := tempLayout(t)
	if _, err := Join(l, "alpha", "bob", identity.Registration{Type: "vim", Project: "/x"}); err == nil {
		t.Error("Join with invalid type must error")
	}
}

// TestTeamNameTraversal はパストラバーサルを試みる team 名が全入口で弾かれることを
// 検証する（AGMSG_HOME 外への読み書き防止）。
func TestTeamNameTraversal(t *testing.T) {
	l := tempLayout(t)
	reg := identity.Registration{Type: "codex", Project: "/x"}
	bad := []string{"../escape", "a/b", "..", ".", "", "foo/../bar"}
	for _, team := range bad {
		if _, err := Join(l, team, "bob", reg); err == nil {
			t.Errorf("Join(team=%q) must error", team)
		}
		if _, _, err := LoadTeam(l, team); err == nil {
			t.Errorf("LoadTeam(team=%q) must error", team)
		}
		if _, err := Leave(l, team, "bob", reg); err == nil {
			t.Errorf("Leave(team=%q) must error", team)
		}
	}
	// 正常な team 名は通る。
	if err := ValidateTeam("alpha-1"); err != nil {
		t.Errorf("ValidateTeam(alpha-1) = %v, want nil", err)
	}
}

func TestLeave(t *testing.T) {
	l := tempLayout(t)
	regA := identity.Registration{Type: "codex", Project: "/repo/a"}
	regB := identity.Registration{Type: "codex", Project: "/repo/b"}
	Join(l, "alpha", "bob", regA)
	Join(l, "alpha", "bob", regB)

	// 片方の registration だけ抜く → agent は残る。
	removed, err := Leave(l, "alpha", "bob", regA)
	if err != nil || !removed {
		t.Fatalf("Leave regA: removed=%v err=%v", removed, err)
	}
	tc, _, _ := LoadTeam(l, "alpha")
	if len(tc.Agents["bob"].Registrations) != 1 {
		t.Fatalf("after partial leave: registrations = %d, want 1", len(tc.Agents["bob"].Registrations))
	}

	// 最後の registration を抜く → agent エントリごと消える。
	if _, err := Leave(l, "alpha", "bob", regB); err != nil {
		t.Fatal(err)
	}
	tc, _, _ = LoadTeam(l, "alpha")
	if _, exists := tc.Agents["bob"]; exists {
		t.Error("agent entry should be removed when last registration leaves")
	}

	// 存在しないものを抜こうとしても false / no error。
	if removed, err := Leave(l, "alpha", "ghost", regA); err != nil || removed {
		t.Errorf("leave nonexistent: removed=%v err=%v", removed, err)
	}
}

func TestResolve(t *testing.T) {
	l := tempLayout(t)
	Join(l, "alpha", "reviewer", identity.Registration{Type: "claude-code", Project: "/repo/a"})
	Join(l, "beta", "helper", identity.Registration{Type: "claude-code", Project: "/repo/a"})
	Join(l, "alpha", "other", identity.Registration{Type: "codex", Project: "/repo/a"})

	// (claude-code, /repo/a) は 2 チームにまたがり 2 件解決する。
	res, err := Resolve(l, "claude-code", "/repo/a")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 2 {
		t.Fatalf("matches = %d, want 2", len(res.Matches))
	}
	// SortMatches で (team, name) 順に安定化されている。
	if res.Matches[0] != (identity.Identity{Name: "reviewer", Team: "alpha"}) {
		t.Errorf("matches[0] = %v", res.Matches[0])
	}
	if _, ok := res.Single(); ok {
		t.Error("ambiguous resolution must not be single")
	}

	// 一意に解決するケース。
	res2, _ := Resolve(l, "codex", "/repo/a")
	if id, ok := res2.Single(); !ok || id.Name != "other" {
		t.Errorf("single resolve: id=%v ok=%v", id, ok)
	}
}
