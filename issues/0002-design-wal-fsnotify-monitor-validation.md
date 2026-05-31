---
decision_type: design
tags: [watch, fsnotify, sqlite-wal, cross-platform]
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
