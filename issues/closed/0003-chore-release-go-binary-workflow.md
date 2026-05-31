---
decision_type: process
tags: [release, goreleaser, distribution, ci, macos]
closed_at: 2026-05-31
---

# リリース時に Go バイナリを生成・配布する工程を整える

Created: 2026-05-31

## 概要

`v*` タグ push を起点に、各 OS / arch 向けの単一バイナリ `agmsg` を自動ビルドして GitHub Release に添付する配布工程を用意する。あわせて、ソースビルドによる install 経路（`go install` / `make install`）を整え、macOS の Gatekeeper 警告を避けられるようにする。

影響する path: `.github/workflows/release.yml`、`.goreleaser.yaml`、`Makefile`、`internal/cli/cli.go`（`version` 注入）、`README.md`（インストール節）、`.gitignore`（`/dist/`）。雛形は `ishii1648/agent-telemetry` を参照。

## 根拠

design.md §2 / §6 / §13 が「`sqlite3` CLI 非依存・単一バイナリ・クロスビルド容易」を核心価値に据えている。`modernc.org/sqlite`（純 Go）採用で `CGO_ENABLED=0` のままクロスビルドできるため、配布工程を CI 化すれば「単一バイナリで簡単に配れる」という設計目標が完成する。bash 版に無かったビルド / 配布工程を、リリース手順として明示的に持つ必要がある（§13）。

## 問題

- 配布バイナリは未署名のため macOS で Gatekeeper 警告が出る。コード署名 / notarization は Apple Developer Program ($99/年) を要し CLI には過剰。
- windows は test matrix から外した方針（49d7b15）があり、リリース対象をどう揃えるか。
- ローカルビルドとリリースで version 文字列の出所をどう一致させるか。

## 対応方針

- GoReleaser（`~> v2`）+ `release.yml`（`v*` タグ起点）で darwin/linux × amd64/arm64 の tar.gz + checksums を生成・添付する。
- 署名は導入せず、`go install` / `make install`（ソースビルドで quarantine が付かない）を警告回避の主経路にする。未署名バイナリ利用時の `xattr -d com.apple.quarantine` 手順を README に明記する。
- version は `internal/cli.version` へ ldflags 注入し、`agmsg version` で報告する。`Makefile` は `git describe`、リリースは `v{{.Version}}` を渡す。
- prerelease(nightly) と Homebrew tap は当面見送り（利用側が増えてから再検討）。
