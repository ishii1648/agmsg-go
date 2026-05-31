---
decision_type: implementation
tags: [ipc, sqlite, cli, tier1]
closed_at: 2026-05-31
---

# Tier 1 最小コア（IPC プリミティブ）を Go で実装する

Created: 2026-05-31

## 概要

`design.md` §10 の Tier 1 最小コアを実装する。対象サブコマンドは `send` / `inbox` / `watch` / `join` / `leave` / `whoami`。これだけで「送る・受け取る・購読する」という IPC が完結する単位。SQLite ドライバは `modernc.org/sqlite`（CGO 不要・単一バイナリ）を採用する（§5.2）。

影響する path（予定）: `cmd/agmsg/`, `internal/store/`, `internal/identity/`, `internal/paths/`。

## 根拠

agmsg-go のスコープは IPC プリミティブのみ（mechanism, not policy / §2.1）。その最小単位が Tier 1 であり、ここを固めれば bash 版オリジナルの送受信を Go 単一バイナリで置換できる。orchestration や Tier 2/3 の補助コマンドはこの後でよい。

bash 版最大の構造的弱点である「手動 SQL エスケープ」を最初の実装から構造的に潰す（placeholder バインドを `internal/store` に集約し、上位層は文字列 SQL を組み立てない / §3.1・§11）。

## 問題

- `messages` テーブルのスキーマ・インデックスを bash 版と互換に保つ（§5.1）。同一 `messages.db` を bash 版・Go 版で読めることが望ましい。
- `(name, team)` を同一性キーとする identity モデルと registration の扱い（§6）。
- `watch` は `id > watermark` の差分 stream（§7・§8.3）。本 issue では受信検知は単純ポーリングで可とし、fsnotify 化は別 issue（[[0002-design-wal-fsnotify-monitor-validation]]）に切り出す。

## 対応方針

- `internal/store`: `CREATE TABLE` / INDEX、INSERT / 未読 SELECT / 既読化を placeholder バインドで実装。SQL を扱う唯一の層にする。
- `internal/identity`: `(name, team)` キーと registration スライス。
- `internal/paths`: `~/.agents/...` のパス解決（OS 差はビルドタグで局所化）。
- `cmd/agmsg`: サブコマンド dispatch のみ。各コマンドは `internal/cli` に置く。
- `watch` の受信検知はまずポーリングで実装し、fsnotify + ポーリング保険は 0002 で載せ替える。
- 各層に `go test` を付ける（store の placeholder 安全性・identity 同一性判定を最低限カバー）。

Tier 2/3 コマンド・配信モードのフック連携・ライフサイクル管理（二重起動防止・孤児回収 / §9）は本 issue のスコープ外。必要になった時点で別 issue に起こす。

Completed: 2026-05-31

## 解決方法

`modernc.org/sqlite`（純 Go・CGO 不要 / §5.2）で Tier 1 を実装。パッケージは design.md §11 の構成に従う。

- `internal/store` — SQL を扱う唯一の層。Insert / Unread / TakeUnread / MaxID / Since を全て placeholder バインドで実装し、bash 版最大の弱点（手動エスケープ / §3.1 弱点(1)）を構造的に封じた。スキーマは §5.1 と互換（`IF NOT EXISTS` で bash 版 DB にも冪等）。`TakeUnread` は取得＋既読化を単一トランザクション化。
- `internal/identity` — `(name, team)` を値型の同一性キー、`(type, project)` を Registration（メタデータ）とし、解決結果の一意判定まで型に固定（§6）。
- `internal/config` — `teams/<team>/config.json` の読み書き（オリジナル互換構造）。Join/Leave の dedupe と、全チーム走査による identity 解決（whoami 相当）。
- `internal/cli` + `cmd/agmsg` — send / inbox / watch / join / leave / whoami。識別子はフラグ／env／一意解決で確定し、曖昧時はエラーで利用側にフラグ指定を促す（policy を埋めない / §2.1）。dispatch は main のみ。
- `watch` は単純ポーリングで実装（`id > watermark` の差分 stream / §7）。fsnotify 化は [[0002-design-wal-fsnotify-monitor-validation]] に委譲。

検証: `go test -race ./...` / `go vet ./...` 通過。store の placeholder 安全性・identity 同一性判定・CLI 結合フロー（join→send→inbox、曖昧解決のエラー）をカバー。実バイナリで send/inbox/watch/whoami と config.json 互換出力を確認。

### 採用しなかった代替

- `internal/paths` の OS 別ビルドタグ分割（§11 で言及）は、Tier 1 が扱う `~/.agents` に darwin/linux 間の実差が無いため見送り。実際の OS 差が生じた時点で `paths_<os>.go` を追加する。
- stdlib `flag` は位置引数の後ろのフラグを解さず `send <to> <body> --from x` を阻むため、`--key value` / `--key=value` を任意位置から抜く簡易パーサを `internal/cli` に置いた。
