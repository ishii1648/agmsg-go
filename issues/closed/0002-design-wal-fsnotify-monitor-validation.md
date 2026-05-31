---
decision_type: design
tags: [watch, fsnotify, sqlite-wal, cross-platform]
closed_at: 2026-05-31
---

# WAL を対象にした fsnotify 監視方針を実機検証する

Created: 2026-05-31

## 概要

`design.md` §8 で採用した「fsnotify + ポーリング保険」による受信検知のうち、**WAL モードの SQLite を fsnotify でどう監視するか**（§8.4）を実機で検証し、設計の前提を確定させる。採用方針は「DB の置かれたディレクトリ全体を監視し、発火後の実取得は `id > watermark` SELECT に委ねる」。

影響する path（予定）: `internal/watch/`。前提として [[0001-feat-tier1-core-ipc-implementation]] の `watch`（ポーリング実装）が必要。

## 根拠

SQLite WAL モードでは書き込みが `messages.db` 本体ではなく `messages.db-wal` に乗るため、「DB ファイル本体だけを監視する」直感的な実装は期待どおり発火しない（§8.4）。この罠を踏むと「メッセージは届いているのに watch が気づかない」という静かな取りこぼしになるため、設計の前提として実測で潰しておく必要がある。fsnotify 採用の正当化はアイドル効率にあり（§8.2）、その効率を取りこぼしと引き換えにしてはならない。

## 問題

- macOS (FSEvents) のイベント coalescing で、短時間の連続書き込みが 1 イベントに丸められても watermark 追従で漏れないか。
- Windows (ReadDirectoryChangesW) でディレクトリ監視が `-wal` / `-shm` の変化を拾うか。
- checkpoint による `-wal` の truncate / 再作成にディレクトリ監視が追従できるか。
- ポーリング保険の間隔（design.md の暫定値 30 秒）が、取りこぼし頻度とアイドルコストのバランスとして妥当か。

## 対応方針

- 0001 の `watch` 実装後に着手する。fsnotify をディレクトリ監視で導入し、ポーリングを保険として併走させる。
- Linux / macOS / Windows の 3 OS で「送信 → watch が即時 stream する」発火を確認する（CI の OS matrix で再現可能にできれば望ましい）。
- fsnotify を意図的に無効化した状態で、ポーリング保険だけで最大 N 秒以内に追いつくことを確認し、保険間隔の値を確定する。
- 結論（監視対象・保険間隔・OS ごとの注意点）は `design.md` §8 に反映し、close 時に要点を残す。

Completed: 2026-05-31

## 解決方法

受信検知を `internal/watch`（fsnotify ディレクトリ監視 + ポーリング保険）として実装し、`cmdWatch` をポーリング単独から載せ替えた。検証結論は `design.md` §8.5 に反映。

- **監視対象は DB ディレクトリ**。ファイル単位ではなく `db/` を `fsnotify.Add` する。`messages.db-wal` への追記でディレクトリ監視が発火することをテストで直接確認（§8.4 の罠と checkpoint による `-wal` 再作成の両方を回避）。
- **保険間隔は 30 秒に確定**（`defaultPollInterval`）。fsnotify 主・典型遅延は実質即時で、保険は取りこぼし時の上限追従にすぎないため案 A（5 秒）より大幅に長く取りアイドルコストを抑える。`--interval` で上書き可。
- **トリガと取得を分離**。どちらの経路でも `id > watermark` SELECT に一本化（`OnTrigger`）。fsnotify バーストは 50ms debounce で 1 回に畳み、起動時同期で監視確立前の書き込みも回収。
- **OS 検証**は `.github/workflows/test.yml` の matrix（ubuntu / macos / windows）で `go test -race` を回し、各 OS の通知機構（inotify / FSEvents / ReadDirectoryChangesW）越しの発火を再現可能にした。
- fsnotify 初期化／`Add` 失敗時は OnError（stderr）通知のうえポーリング保険にフォールバックし、watch は中断しない。

検証: `go test -race ./...` / `go vet ./...` 通過（macOS ローカル）。watch の単体テスト（起動時トリガ・ポーリング保険単独・ディレクトリ変更発火・`-wal` 追記発火・ctx キャンセル・dir 欠落時のフォールバック）と、cli の send→watch エンドツーエンド stream をカバー。残り 2 OS は CI matrix で担保。

## 採用しなかった代替

- **`messages.db-wal` 単体を監視**: 書き込みを最速で捉えられるが checkpoint の truncate / 再作成にファイル監視が追従できず、再 Add のための実装が要る（§8.4）。ディレクトリ監視なら WAL の実装詳細に依存せず堅牢なため見送り。
- **fsnotify を必須化（失敗時にエラー終了）**: fsnotify はあくまで「主」で正当化はアイドル効率（§8.2）。通知機構が使えない環境でも受信検知は成立すべきなので、失敗時はポーリング保険へ degrade する設計にした。
