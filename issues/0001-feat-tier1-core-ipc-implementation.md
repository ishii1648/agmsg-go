---
decision_type: implementation
tags: [ipc, sqlite, cli, tier1]
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
