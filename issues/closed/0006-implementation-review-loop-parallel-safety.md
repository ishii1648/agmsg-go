---
decision_type: implementation
tags: [skills, review-loop, concurrency, agmsg, parallel]
closed_at: 2026-06-01
---

# review-loop を並列 worktree 実行・二重起動に対して安全にする

Created: 2026-06-01

影響する path: `skills/review-loop/review-loop.sh`、`skills/review-loop/SKILL.md`。

## 概要

review-loop は実行ごとの名前空間キーを `session_id` 一本に頼っているが、その `session_id`
には**エントロピーも衝突検出もなく**、同一キーへの並行ライターを止める機構もない。このため
(1) 複数 worktree からの並列実行、(2) 同一セッション内での round 二重起動 の両方で verdict が
誤受信・破壊される。本 issue でこれを機構的に塞ぐ（Fix A〜D）。

## 問題 / 根本原因

review-loop が触る共有リソースはすべて `team = session_id` だけで分離されている。

| リソース | 実体 | 分離キー |
|---|---|---|
| agmsg DB | `~/.agents/skills/agmsg`（全 worktree で 1 つの SQLite） | `team`(=session_id) + 固定 identity `implementer`/`reviewer` |
| manifest | `~/.review-loop/$session_id/`（repo 非依存・グローバル） | `session_id` |
| レビュー出力 | `$repo_root/.outputs/.../$session_id/` | repo_root + session_id |

その `session_id` は SKILL.md Step 2 でエージェントが `reviewloop-$(date +%Y%m%d-%H%M%S)` と
**手組み**＝秒解像度・乱数なし・衝突検出なし。identity 名は定数なので **team が衝突した瞬間に
inbox が共有される**。さらに `agmsg inbox`（`store.TakeUnread`）は **(team, to) 宛ての未読を
全件まとめて破壊的にドレイン**し、ペイロードでの絞り込みは無い（`internal/store/store.go`）。
`wait-review` は取り出した中の最後の `REVIEW_RESULT` 行を採るだけで、round 識別も実行識別もしない。

### 壊れる経路（2 つとも同根）

- **クロス worktree**: worktree A/B が同一秒に起動 → `session_id`=`team` 一致。A の
  `wait-review` の `agmsg inbox` が B の reviewer の `REVIEW_RESULT` まで claim → B は TIMEOUT、
  A は **別 diff の verdict を自分のものとして誤受信**。manifest も相互に上書き。
- **同一セッション二重起動（実観測したインシデント）**: `review-once` に冪等ガードが無く、
  round 1 を 2 回呼ぶと window が 2 つでき `panes["1"]` は後勝ち上書き、旧 pane は orphan で生存。
  codex が 2 体とも同じ `round-1-review.md` を truncate し、`notify-verdict` を 2 回 →
  同一 team/round に矛盾する 2 通。`inbox` が両方ドレイン、`tail -1`(id 最大)が勝つので
  **pane 表示とファイル実体が食い違う**。

→ 「チャネルのキーが粗い + 同一キーへの並行ライターを止めない + verdict にラウンド/実行の識別子が無い」
という一つの設計欠落の別の顔。

## 対応方針（defense in depth）

- **Fix A — `session_id` を衝突不能化**: `review-once` が `--session-id` 省略時に
  `reviewloop-<repo_basename>-<utc_stamp>-<rand>` を自前採番して `SESSION_ID:` を echo。SKILL.md は
  「手組みせず review-once の出力値を以降に使う」へ変更。エージェントの手組み（インシデントの遠因）を排除。
  これにより Fix B のロックが残留しても新 run は別 session_id を得るため自己限定的になる。
- **Fix B — `review-once` に (session, round) 冪等ロック**: window 作成前に
  `mkdir $REVIEW_LOOP_DIR/$session_id/round-$round.lock`（アトミック）で launch を排他し、
  既存 pane が生存中なら `STATUS: ALREADY_RUNNING` で新規起動しない。並列ツール 2 連射でも 2 体目を弾く。
- **Fix C — round 別 inbox + payload に round**: 受信 identity を `implementer-r<round>` に分離し、
  `REVIEW_RESULT: <verdict> round=<N>` を送る。round ごとに inbox が分かれるので残留・クロスラウンドの
  誤受信が起きない。`wait-review` は body の `round=` を突合する。
- **Fix D — verdict↔ファイル整合チェック**: `wait-review` 受信時に `round-N-review.md` 末尾の
  `REVIEW_RESULT` と突合し、食い違えば `CONSISTENCY: INCONSISTENT` を立てて警告（二重起動・クロストークの
  可視症状を検知）。

selftest に回帰ガード（ロック再入拒否 / round payload 生成 / round 別宛先名）を追加。

## 却下案

- **agmsg 側に round/sid フィルタ付き非破壊 inbox を足す**: binary コア層（mechanism）に skills 固有
  ロジックを漏らすため却下（[[0004-design-skills-layer-on-ipc]] の層分離不変条件）。round 分離は
  既存の identity 名で表現でき、binary 変更は不要。
- **session_id 採番をエージェント側に残しエントロピーだけ足す**: 手組み自体がインシデントの遠因
  （二重起動）なので、採番を script に寄せて構造的に外す方を採る。
