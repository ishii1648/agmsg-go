---
decision_type: implementation
tags: [distribution, upgrade, github-releases, cli]
---

# agmsg upgrade サブコマンドで自己更新を実装する

Created: 2026-05-31

## 概要

実行中の `agmsg` バイナリを GitHub Releases の最新版で置き換える `agmsg upgrade [--check]` を追加する。プラットフォームに合致した tar.gz を取得し、`checksums.txt` と SHA-256 を突き合わせてからアーカイブ内の `agmsg` を取り出し、実行ファイルのパスへ atomically に rename する。`--check` は最新版の確認だけ行い置き換えはしない。

影響する path: `internal/upgrade/upgrade.go`（新規・ダウンロード/検証/置換ロジック）、`internal/upgrade/upgrade_test.go`、`internal/cli/upgrade.go`（CLI ハンドラ）、`internal/cli/cli.go`（dispatch / allowed flags / usage）、`README.md`（運用補助コマンド節）。雛形は `ishii1648/agent-telemetry` の `internal/upgrade` を参照。

## 根拠

issue 0003 で単一バイナリ配布（GoReleaser + GitHub Releases）を整えたが、更新は手動 download か `go install` 再実行が必要だった。自己更新コマンドがあれば配布から更新までが完結する。design.md §2 / §6 の「`sqlite3` CLI 非依存・単一バイナリ・クロスビルド容易」という核心価値に、更新運用も単一バイナリで閉じる形で整合する。

## 対応方針

- ロジックは `internal/upgrade` パッケージに隔離し、`internal/cli` は薄い配線のみ（既存のサブコマンド分割方針に合わせる）。
- アセット名は GoReleaser の `name_template`（`{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}`）に合わせ `agmsg-go_<os>_<arch>.tar.gz`、アーカイブ内バイナリ名は `agmsg`。
- SHA-256 検証を必須化し、checksum 不一致なら置き換えない（改竄・破損検知）。
- darwin / linux のみ対象（`.goreleaser.yaml` の対象 OS と一致。windows はリリース対象外）。symlink は `EvalSymlinks` で実体解決してから rename し、symlink を通常ファイルで潰さない。
- GitHub token は `GITHUB_TOKEN` → `gh auth token` → 未認証の順でフォールバック（public repo なので未認証でも取得可、token があれば rate limit が緩む）。
- agent-telemetry 版にあった `warnLegacyBinary`（旧 hitl-metrics バイナリ警告）は agmsg-go に該当しないため移植しない。
