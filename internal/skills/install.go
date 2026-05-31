// Package skills は埋め込まれた skills 層（dispatch / review-loop）を
// ファイルシステム上へ展開する。
//
// このパッケージは「ファイルを書き出すだけ」であり、skills の中身（policy）を
// 解釈しない。infra 層（binary）と skills 層（policy）の分離を壊さないため、
// ここに dispatch/review-loop 固有の知識を持ち込まないこと（design.md §2.1 / §13）。
package skills

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Result は 1 ファイル分の展開結果。
type Result struct {
	// Path は dest からの相対パス（例: "dispatch/dispatch.sh"）。
	Path string
	// Skipped は force=false で既存ファイルがあり上書きしなかった場合に true。
	Skipped bool
}

// Install は fsys（埋め込まれた skills ツリーのルート、通常は埋め込み FS を
// "skills" で Sub したもの）を dest 配下へ展開する。
//
//   - ディレクトリは必要に応じて作成する。
//   - *.sh は実行ビット付き（0755）、それ以外は 0644 で書き出す。embed.FS は
//     パーミッションを保持しない（全て 0444 相当）ため、拡張子から復元する。
//   - force=false のとき既存ファイルは上書きせず Skipped として報告する。
//
// 戻り値は走査順（安定）の Result スライス。
func Install(fsys fs.FS, dest string, force bool) ([]Result, error) {
	var results []Result

	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		target := filepath.Join(dest, filepath.FromSlash(p))

		if d.IsDir() {
			if mkErr := os.MkdirAll(target, 0o755); mkErr != nil {
				return fmt.Errorf("create dir %s: %w", target, mkErr)
			}
			return nil
		}

		// 親ディレクトリが（ツリーに dir エントリとして現れない経路でも）存在するよう保証する。
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return fmt.Errorf("create dir %s: %w", filepath.Dir(target), mkErr)
		}

		if !force {
			if _, statErr := os.Stat(target); statErr == nil {
				results = append(results, Result{Path: p, Skipped: true})
				return nil
			}
		}

		data, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			return fmt.Errorf("read embedded %s: %w", p, readErr)
		}

		mode := fileMode(p)
		if wErr := os.WriteFile(target, data, mode); wErr != nil {
			return fmt.Errorf("write %s: %w", target, wErr)
		}
		// WriteFile は既存ファイルの mode を変えないことがあるため、明示的に揃える。
		if chErr := os.Chmod(target, mode); chErr != nil {
			return fmt.Errorf("chmod %s: %w", target, chErr)
		}
		results = append(results, Result{Path: p, Skipped: false})
		return nil
	})
	if err != nil {
		return results, err
	}
	return results, nil
}

// fileMode は拡張子から実行可否を判定する。スクリプト（.sh）は実行可能にする。
func fileMode(p string) os.FileMode {
	if strings.HasSuffix(p, ".sh") {
		return 0o755
	}
	return 0o644
}
