package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"testing/fstest"
)

// fixtureFS は install のテスト用に最小の skills ツリーを模す。
func fixtureFS() fstest.MapFS {
	return fstest.MapFS{
		"dispatch/dispatch.sh":       {Data: []byte("#!/usr/bin/env bash\necho hi\n")},
		"dispatch/SKILL.md":          {Data: []byte("# dispatch\n")},
		"review-loop/review-loop.sh": {Data: []byte("#!/usr/bin/env bash\necho rl\n")},
		"review-loop/SKILL.md":       {Data: []byte("# review-loop\n")},
	}
}

func TestInstallWritesFilesWithModes(t *testing.T) {
	dest := t.TempDir()
	results, err := Install(fixtureFS(), dest, false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("got %d results, want 4: %+v", len(results), results)
	}

	cases := []struct {
		rel     string
		wantExe bool
	}{
		{"dispatch/dispatch.sh", true},
		{"dispatch/SKILL.md", false},
		{"review-loop/review-loop.sh", true},
		{"review-loop/SKILL.md", false},
	}
	for _, c := range cases {
		fi, err := os.Stat(filepath.Join(dest, c.rel))
		if err != nil {
			t.Errorf("stat %s: %v", c.rel, err)
			continue
		}
		// パーミッションビットの検証は Windows では意味を持たないためスキップ。
		if runtime.GOOS == "windows" {
			continue
		}
		exe := fi.Mode().Perm()&0o111 != 0
		if exe != c.wantExe {
			t.Errorf("%s: exec bit = %v, want %v (mode %v)", c.rel, exe, c.wantExe, fi.Mode().Perm())
		}
	}
}

func TestInstallSkipsExistingWithoutForce(t *testing.T) {
	dest := t.TempDir()
	if _, err := Install(fixtureFS(), dest, false); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	// 既存ファイルを書き換えて、force=false ではスキップ（=保持）されることを確認。
	target := filepath.Join(dest, "dispatch", "SKILL.md")
	if err := os.WriteFile(target, []byte("USER EDIT\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Install(fixtureFS(), dest, false)
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	var skipped int
	for _, r := range results {
		if r.Skipped {
			skipped++
		}
	}
	if skipped != 4 {
		t.Errorf("skipped = %d, want 4 (all existing)", skipped)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "USER EDIT\n" {
		t.Errorf("force=false overwrote user edit: %q", got)
	}
}

func TestInstallForceOverwrites(t *testing.T) {
	dest := t.TempDir()
	if _, err := Install(fixtureFS(), dest, false); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	target := filepath.Join(dest, "dispatch", "SKILL.md")
	if err := os.WriteFile(target, []byte("USER EDIT\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results, err := Install(fixtureFS(), dest, true)
	if err != nil {
		t.Fatalf("force Install: %v", err)
	}
	for _, r := range results {
		if r.Skipped {
			t.Errorf("force=true should not skip, but %s skipped", r.Path)
		}
	}
	got, _ := os.ReadFile(target)
	if string(got) != "# dispatch\n" {
		t.Errorf("force=true did not overwrite: %q", got)
	}
}
