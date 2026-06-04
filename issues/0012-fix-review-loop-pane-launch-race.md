---
decision_type: implementation
tags: [skills, review-loop, dispatch, tmux, fish, race]
---

# review-loop の pane 起動が fish 初期化レースでコマンド未実行になる

Created: 2026-06-04

影響する path: `skills/review-loop/review-loop.sh`（`launch_in_pane` / `wait_pane_shell`）と
`skills/dispatch/dispatch.sh`（`cmd_launch` の `sleep 0.5` → send-keys。同種の潜在レース）。

## 概要

`review-once` が新規 window を作り `launch_in_pane` でレビュアー起動コマンドを送った直後、pane を
キャプチャすると **コマンド文字列がプロンプト行に残ったまま Enter が効かず未実行** になる事象を観測した。
codex プロセスは起動せず `round-N-review.md` も生成されず、`wait-review` が 900s 沈黙する。
[[issues/closed/0006-implementation-review-loop-parallel-safety]] が「同一キーへの並行ライター」を
塞いだのに対し、本 issue は「**起動 1 回が確実に着弾するか**」という launch タイミングの欠陥を塞ぐ。

## 問題 / 根本原因

`launch_in_pane` は次の順で送出する:

```
wait_pane_shell "$pane_id" 30 || true
sleep 0.3
tmux send-keys -t "$pane_id" "cd '$work_dir'; $cmd" Enter
```

1. **fish 初期化レース（主因）**: `wait_pane_shell` は `#{pane_current_command}` がシェル名
   （`fish` 等）になった時点で return する。だが新規 window では fish は生成直後から
   `pane_current_command=fish` になる一方、対話初期化（`config.fish` 読込・welcome greeting 描画・
   readline 起動）は未完。`sleep 0.3` ではこの初期化を待ちきれず、`send-keys ... Enter` が
   初期化中に着弾し、**末尾の Enter が初期化処理に飲まれてコマンドが実行されない**。
   キャプチャに `Welcome to fish ...` が送信コマンドへ割り込んでいたことが、send-keys が
   「fish が welcome を描画中」に撃たれた証跡。`pane_current_command` がシェル名になっただけでは
   「入力受付可能」を意味しないのが核心。

2. **入力行の残骸との連結（副因）**: 別 session-id の未実行コマンドが入力行に残り、今回のコマンドと
   連結してコマンドライン全体が壊れていた。残骸の正確な流入経路は断定しきれない（`review-once` は
   round ごとに新規 window を作るため、同一 pane が session をまたいで再利用される明確な経路は
   コード上は無い）。ただし **送出直前に行をクリアすれば流入経路に依らず無害化**できるので、
   原因特定を待たず defense in depth として塞ぐ。

手動で `C-c` / `C-u` してから fish 完全起動後に同じコマンドを send-keys し直すと正常起動した、という
観測が (1)(2) の両方と整合する。

## 対応方針

- **Fix 1 — readiness を「Enter が効く」ことで確認する `wait_pane_ready`**: `wait_pane_shell`
  （シェル名）の後に、ユニークマーカーを `printf '%s\n' <marker>` で echo させ、その出力が
  `capture-pane` に **単独行で現れるまでポーリング**する。これで「プロンプトが Enter を受理し、
  シェルがコマンドを実際に実行できる」状態を **シェル非依存** に確認する（タイプされたコマンド行は
  `printf '%s\n' <marker>` なので、whole-line 完全一致では出力行とだけ一致する）。
- **Fix 2 — 送出前の入力行クリア**: マーカー送出前と本命コマンド送出前に `C-u` を送り、残骸との
  連結を機構的に防ぐ（空行への `C-u` は no-op なので副作用なし）。
- **Fix 3 — readiness の可視化**: `review-once` の `STARTED` 出力に `LAUNCH_READY: yes|no` を足し、
  ready 確認が取れないまま起動した場合に `WARNING` 行を出す（silent な 900s 沈黙を観測可能にする）。
- **Fix 4 — dispatch.sh にも同じハンドシェイクを移植**: `cmd_launch` も `sleep 0.5` のみで send-keys
  しており同種レースを潜在的に抱える（worktree 作成・git fetch・codex の attached-client 待ちで
  顕在化しにくいだけ）。`wait_pane_ready` 相当を移植し `sleep 0.5` を readiness 確認に置き換え、
  `LAUNCHED` 出力に `LAUNCH_READY: yes|no` を足す。人間が見る session のため probe のマーカー行が
  scrollback に 1 行残るが、起動の確実性を優先する（このトレードオフは許容と判断）。

`launch_in_pane`（review-loop）と `cmd_launch`（dispatch）は `wait_pane_ready` → `C-u` → 本命送出に
変更する。失敗時もベストエフォートで送出は試みる（window leak を避けるため die せず、`wait-review` の
TIMEOUT と `LAUNCH_READY: no` で表面化する）。

## 却下案

- **プロンプト記号の出現を待つ**: starship / fish テーマ / 複数行プロンプトでプロンプト末尾が
  一定しないため脆い。マーカー round-trip の方が確実。
- **`tmux wait-for -S` + シェルフックで ready シグナル**: fish / bash / zsh それぞれに rc フック
  注入が要りクロスシェルで複雑。マーカー echo は shell 非依存で済む。
- **`sleep` を延ばすだけ**: 初期化時間はマシン負荷依存で非決定的。固定待ちは不確実。
- **text と Enter を分割して間に待ちを挟む**: レースを縮めるのみで残骸（副因）を解消できず、
  初期化中着弾の根本対処にならない。
- **dispatch.sh は今回触らず follow-up に留める**（当初案）: probe のマーカー行が人間の見る
  scrollback を 1 行汚す副作用を嫌ったため。最終的に Fix 4 として同時適用する方を採った
  （起動の確実性 > scrollback の見た目）。

## selftest で守れる範囲

`wait_pane_ready` / `launch_in_pane` は tmux への副作用を伴うため、副作用なしの `selftest`
（純粋ロジックのみ）の対象にはできない。マーカーの whole-line 一致だけは純粋ロジックに切り出して
回帰ガードを置ける余地があるが、本質は live tmux でしか検証できないため、ライブ tmux スモークテスト
（新規 fish window に対し ready 確認 → コマンド着弾）で担保する。
