// Package agmsg はモジュールルートに置かれ、skills 層（policy）のアセットを
// バイナリへ埋め込む役割だけを持つ。
//
// なぜルートパッケージか: go:embed は埋め込み元 .go ファイルの存在するディレクトリ
// より上位（親）を辿れない。skills 層の source of truth はリポジトリ直下 skills/ に
// 置く（infra 層 internal/ と視覚的に分離するため・design.md §13）ので、それを
// 埋め込めるのはモジュールルートに置いた本ファイルだけになる。internal/cli からは
// この SkillsFS を import して `agmsg skills install` で展開する。
//
// 層分離の不変条件: ここは「ファイルを抱えて配るだけ」であり、skills の policy 的
// 振る舞い（dispatch/review-loop のロジック）はバイナリに一切持ち込まない（§2.1）。
package agmsg

import "embed"

// SkillsFS は同梱する skills 層（dispatch / review-loop）のファイルツリー。
// all: プレフィックスで SKILL.md のようなドット無しファイルも確実に含める
// （既定の go:embed は _ や . 始まりを除外するが、ここでは全ファイルを対象にする）。
//
//go:embed all:skills
var SkillsFS embed.FS
