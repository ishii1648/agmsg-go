---
decision_type: spec
supersedes: [0005]
tags: [distribution, upgrade, scope, security]
closed_at: 2026-06-02
---

# upgrade サブコマンドを削除し、更新を go install に一本化する

Created: 2026-06-02

影響する path: `internal/upgrade/`（削除）、`internal/cli/upgrade.go`（削除）、`internal/cli/cli.go`（allowed flags / dispatch / usage から `upgrade` 除去）、`README.md`。

## 概要

自己更新コマンド `agmsg upgrade [--check]`（[[0005-feat-self-upgrade-subcommand]]）を CLI から削除する。
更新は主たる配布経路である `go install github.com/ishii1648/agmsg-go/cmd/agmsg@latest` の再実行に一本化する。
これは CLI の外部契約からサブコマンドを 1 つ取り除く spec 変更。

## 根拠

セキュリティレビュー（`.outputs` 相当のレビュー文書）を受けた攻撃面の棚卸しで、upgrade が

- **唯一のネットワーク向きコードパス**であり、レビューの懸案 [[0007-fix-upgrade-bound-download-extract-size]]
  （ダウンロード/展開の無制限＝DoS）と [[0009-design-upgrade-release-signing]]（checksums が同一信頼ドメイン・
  未署名＝供給網 Critical 級）を**単独で生む発生源**だった。
- agmsg の core scope（`send`/`inbox`/`watch`/`join`/`leave`/`whoami` の IPC プリミティブのみ、
  mechanism not policy / design.md §2.1）から外れた便利機能であり、核心価値（sqlite3 非依存・単一バイナリ・
  小さなコア）に何も足さない。
- 主たる入手経路が `go install ...@latest` である以上、更新手段としても**重複**しており、
  約 325 行＋テスト 124 行の保守対象（HTTP クライアント・tar 展開・checksum・symlink 解決・自己置換）を
  抱える費用対効果が悪い。

「配布から更新まで単一バイナリで完結」という 0005 の根拠より、攻撃面とコードを縮める利得が上回ると判断。

## 解決方法

- `internal/upgrade/`（`upgrade.go` / `upgrade_test.go`）と `internal/cli/upgrade.go` を削除。
- `internal/cli/cli.go` の allowed flags・dispatch switch・usage から `upgrade` を除去。
  `version` サブコマンド（独立した early return、`version` 変数を継続使用）は影響なし。
- `README.md` の運用補助節を「更新は `go install ...@latest` 再実行」に書き換え。
- リリース配布自体（GoReleaser + GitHub Releases、[[0003-chore-release-go-binary-workflow]]）は
  バイナリ直配布ユーザ向けに**維持**する（削除したのは自己更新クライアントのみ）。
- `go build` / `go vet` / `go test ./...` グリーンを確認。

## 採用しなかった代替

- **残してハードニング（0007 のサイズ上限実装）**: 攻撃面とメンテ負担が残るうえ、スコープ外機能の
  延命にしかならないため却下。
- **ビルドタグで opt-in 化（デフォルト無効）**: 二重メンテで中途半端。完全削除を採る。

Completed: 2026-06-02

[[0005-feat-self-upgrade-subcommand]] を supersede。
