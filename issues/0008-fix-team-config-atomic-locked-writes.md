---
decision_type: implementation
tags: [config, identity, concurrency, integrity]
---

# team config の書き込みをアトミック化し join/leave を直列化する

Created: 2026-06-02

影響する path: `internal/config/team.go`（`Save` / `Join` / `Leave`）。

## 概要

`teams/<team>/config.json` の書き込みは非アトミックでロックも無いため、並行する join/leave で
登録が失われ、書き込み中のクラッシュで JSON が破損して後続コマンドが落ちる。セキュリティ
レビューの "JSON config writes are not locked/atomic ... lost registrations or local DoS"（Medium）
に該当する、ローカル IPC ドメイン内の整合性欠陥。

## 根拠

- `Join` / `Leave` は read-modify-write（`LoadTeam` → スライスを `append`/フィルタ → `Save`）。
  2 プロセスが同一 team を同時に更新すると後勝ちで一方の登録が消える（TOCTOU）。
- `Save` は `os.WriteFile(path, b, 0o644)` 一発。truncate してから書くため、書き込み途中で
  プロセスが死ぬと**切れた JSON** が残り、以降の `LoadTeam`（`json.Unmarshal`）が失敗して
  関連コマンドがクラッシュする（malformed local JSON crashing commands）。
- agmsg は単一ワークステーション上で複数エージェントが同一 team に並行 join する利用形態
  （[[0004-design-skills-layer-on-ipc]] の dispatch / review-loop）が前提なので、競合は例外でなく常態。

## 問題

これは「同一ユーザの悪意あるプロセス」を仮定しなくても、正規の並行エージェントだけで踏みうる。
ローカルファイルパーミッションを分離境界とする前提（design.md）でも、書き込みの原子性と
直列化は別途担保する必要がある。

## 対応方針

- **アトミック化**: temp ファイルへ書いて `os.Rename` で差し替え（同一ディレクトリ内 rename は
  atomic）。途中クラッシュでも本体ファイルは常に valid JSON を保つ。
- **直列化**: team 単位のファイルロック（flock 系 / lock ファイルの `O_CREATE|O_EXCL`）で
  read-modify-write 全体を排他し、lost update を防ぐ。ロック粒度は team 単位に留め全体直列化は避ける。
- 排他の実装は [[0006-implementation-review-loop-parallel-safety]] の `mkdir` ロックと同系統の
  「アトミックなプリミティブで囲う」方針を踏襲する。
- 既存 `team_test.go` に並行 join/leave の lost-update 回帰と、不完全 JSON を書いた後の
  読み出し挙動のテストを追加。
- 付随: 現状 `0o644` のモードは private home 下では Low だが、atomic 化のついでに必要十分な
  モードへ寄せるか検討（本 issue のスコープ主眼ではない）。
