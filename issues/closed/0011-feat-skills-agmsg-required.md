---
decision_type: process
tags: [skills, dispatch, review-loop, agmsg]
closed_at: 2026-06-02
---

# skills が agmsg binary を hard 依存にする（PATH に無ければ起動前に落とす）

Created: 2026-06-02

影響する path: `skills/dispatch/dispatch.sh`、`skills/dispatch/SKILL.md`、`skills/review-loop/review-loop.sh`（既に必須化済み）。

## 概要

`skills/` 配下の skill が agmsg を **hard 依存**として扱うよう統一する。agmsg が PATH に無い場合は
起動前に `die` する。`skills/review-loop` は既に必須化済み（agent 間連絡を agmsg に載せるため）。
本 issue は optional（無ければ silent skip）だった `skills/dispatch` を required に揃える process 変更。

## 根拠

dispatch の agmsg auto-join は「起動した agent を親 session から `agmsg send <name> --team <team>` で
到達可能にする」という中核機能であり、これが成立しないと dispatch で立てた agent と疎通できない。
従来は `agmsg` が無ければ join を silent skip して起動していたため、

- 「到達できると思って send したら相手が join していなかった」という静かな失敗を生む。
- review-loop は必須・dispatch は任意、と skill 間で前提がばらつき混乱の元になる。

「到達性は dispatch の前提」と割り切り、binary 不在も join 失敗も early die に揃える方が安全。

## 解決方法

- `skills/dispatch/dispatch.sh`:
  - `cmd_launch` のバリデーション段に `command -v agmsg >/dev/null 2>&1 || die ...` を追加。
  - auto-join を無効化する `--no-agmsg` フラグ・`no_agmsg` 変数・パース分岐を撤去（required と排他）。
  - auto-join の `command -v agmsg` ガードを外して無条件実行に。
  - `agmsg join` 失敗時の warn 継続（`tmux display-message`）を `die` に変更。
- `skills/dispatch/SKILL.md`: 「agmsg は必須」「PATH 不在・join 失敗で落とす」に追記、`--no-agmsg` と
  「auto-join した場合のみ」表記を除去。
- `skills/review-loop/review-loop.sh`: 既に `command -v agmsg || die` 済みのため変更なし（selftest PASS を確認）。

## 採用しなかった代替

- **binary 不在のみ die、join 失敗は warn 継続**: 到達性保証が目的なのに silent fail が残り中途半端。両方 die に揃える。
- **`--no-agmsg` を残す**: required と矛盾する逃げ道。後方互換より一貫性を採り削除。

Completed: 2026-06-02
