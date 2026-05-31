package identity

import "testing"

// TestIdentityEquality は (name, team) を同一性キーとし、
// type / project（メタデータ）は同一性に影響しないことを検証する (design.md §6)。
func TestIdentityEquality(t *testing.T) {
	a := Identity{Name: "reviewer", Team: "alpha"}
	b := Identity{Name: "reviewer", Team: "alpha"}
	c := Identity{Name: "reviewer", Team: "beta"}
	d := Identity{Name: "impl", Team: "alpha"}

	if a != b {
		t.Errorf("same (name, team) must be equal: %v != %v", a, b)
	}
	if a == c {
		t.Errorf("different team must differ: %v == %v", a, c)
	}
	if a == d {
		t.Errorf("different name must differ: %v == %v", a, d)
	}

	// map キーとして使える（値型同一性）。
	seen := map[Identity]bool{a: true}
	if !seen[b] {
		t.Error("equal identity must hit the same map key")
	}
}

func TestValidType(t *testing.T) {
	for _, ok := range ValidTypes {
		if !ValidType(ok) {
			t.Errorf("ValidType(%q) = false, want true", ok)
		}
		if err := ValidateType(ok); err != nil {
			t.Errorf("ValidateType(%q) = %v, want nil", ok, err)
		}
	}
	if ValidType("emacs") {
		t.Error("ValidType(emacs) = true, want false")
	}
	if ValidateType("emacs") == nil {
		t.Error("ValidateType(emacs) = nil, want error")
	}
}

func TestRegistrationMatch(t *testing.T) {
	r := Registration{Type: "codex", Project: "/repo/a"}
	if !r.Match("codex", "/repo/a") {
		t.Error("expected match on identical (type, project)")
	}
	if r.Match("claude-code", "/repo/a") {
		t.Error("type must participate in match")
	}
	if r.Match("codex", "/repo/b") {
		t.Error("project must participate in match")
	}
}

func TestResolutionSingle(t *testing.T) {
	var empty Resolution
	if _, ok := empty.Single(); ok {
		t.Error("empty resolution must not be single")
	}

	one := Resolution{Matches: []Identity{{Name: "a", Team: "t"}}}
	if id, ok := one.Single(); !ok || id.Name != "a" {
		t.Errorf("single resolution: got %v, %v", id, ok)
	}

	many := Resolution{Matches: []Identity{{Name: "a", Team: "t"}, {Name: "b", Team: "t"}}}
	if _, ok := many.Single(); ok {
		t.Error("multi resolution must not be single")
	}
}

func TestSortMatches(t *testing.T) {
	r := Resolution{Matches: []Identity{
		{Name: "z", Team: "beta"},
		{Name: "a", Team: "beta"},
		{Name: "m", Team: "alpha"},
	}}
	r.SortMatches()
	want := []Identity{
		{Name: "m", Team: "alpha"},
		{Name: "a", Team: "beta"},
		{Name: "z", Team: "beta"},
	}
	for i := range want {
		if r.Matches[i] != want[i] {
			t.Errorf("sorted[%d] = %v, want %v", i, r.Matches[i], want[i])
		}
	}
}
