package cli

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	rootfs "github.com/ishii1648/agmsg-go"
	"github.com/ishii1648/agmsg-go/internal/paths"
	"github.com/ishii1648/agmsg-go/internal/skills"
)

// skillsSubdir は埋め込み FS 内で skills ツリーが置かれているルート。
const skillsSubdir = "skills"

// cmdSkills は同梱の skills 層（dispatch / review-loop）をファイルシステムへ展開する。
//
//	agmsg skills install [--dest <dir>] [--force]
//	agmsg skills list
//
// これは infra 層（binary）が skills 層（policy）を「配るだけ」のエントリポイント。
// dispatch/review-loop の中身は解釈しない（design.md §2.1 / §13）。
func cmdSkills(_ context.Context, e Env, _ paths.Layout, f flags) error {
	action := ""
	if len(f.pos) > 0 {
		action = f.pos[0]
	}

	sub, err := fs.Sub(rootfs.SkillsFS, skillsSubdir)
	if err != nil {
		return fmt.Errorf("open embedded skills: %w", err)
	}

	switch action {
	case "install":
		return skillsInstall(e, f, sub)
	case "list":
		return skillsList(e, sub)
	case "":
		return fmt.Errorf("missing action; usage: agmsg skills {install|list}")
	default:
		return fmt.Errorf("unknown skills action %q; usage: agmsg skills {install|list}", action)
	}
}

func skillsInstall(e Env, f flags, sub fs.FS) error {
	dest, err := skillsDest(f)
	if err != nil {
		return err
	}
	// --force は boolFlags 登録済みなので、指定時は "true" が入る。
	force := f.get("force", "", "") == "true"

	results, err := skills.Install(sub, dest, force)
	if err != nil {
		return err
	}

	var wrote, skipped int
	for _, r := range results {
		if r.Skipped {
			skipped++
			fmt.Fprintf(e.Stdout, "  skip  %s (exists; use --force to overwrite)\n", r.Path)
		} else {
			wrote++
			fmt.Fprintf(e.Stdout, "  write %s\n", r.Path)
		}
	}
	fmt.Fprintf(e.Stdout, "installed skills to %s (%d written, %d skipped)\n", dest, wrote, skipped)
	return nil
}

func skillsList(e Env, sub fs.FS) error {
	var files []string
	err := fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, p := range files {
		fmt.Fprintln(e.Stdout, p)
	}
	return nil
}

// skillsDest は --dest を解決する。既定は ~/.claude/skills（Claude Code の skills 置き場）。
// これは DB の置き場所（AGMSG_HOME）とは別物なので paths.Layout には依らない。
func skillsDest(f flags) (string, error) {
	if v := f.get("dest", "", ""); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir for default --dest: %w", err)
	}
	return filepath.Join(home, ".claude", "skills"), nil
}
