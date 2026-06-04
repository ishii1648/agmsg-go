#!/usr/bin/env bash
# review-loop — 元セッション主導の反復レビューループ用ユーティリティ。
#
#   元 coding session（実装役）が自分の逆エージェントをレビュアーとして同一 tmux session の
#   pane に起動し（review-once）、レビュー完了を待ち（wait-review）、結果を読んで自分で修正する。
#   ループ制御・修正・収束判定は元セッション（SKILL.md 手順）が担う。本スクリプトはレビュアーの
#   起動と完了待ちのみを提供する。
#
# agent 間連絡は agmsg（このリポジトリの IPC binary）に載せる:
#   - reviewer は verdict を `agmsg send implementer "REVIEW_RESULT: <verdict>" --from reviewer --team <team>`
#     で実装役へ送る。実装役は `agmsg inbox --name implementer --team <team>` のポーリングで受信する。
#   - team は per-session の ephemeral team（= session-id）。明示的な identity フラグで送受信するため
#     `agmsg join` は不要（送信側・受信側とも --from/--name と --team を明示する）。
#   - tmux は「対話 agent を起こす」launcher として残る（プロセス起動は agmsg では代替できない）。
#
# サブコマンド:
#   review-once <repo_root> --session-id <id> --reviewer <claude|codex> --round <N>
#               [--base <ref>] [--note <file>]
#       現在の tmux session に reviewer 用 window を追加してレビュアーを起動する。
#   wait-review <repo_root> --session-id <id> --round <N> [--timeout <sec>]
#       reviewer から implementer 宛ての REVIEW_RESULT メッセージを待ち、VERDICT を stdout に返す。
#   notify-verdict --team <team> --out <review_file> [--to <name>] [--from <name>]
#       review_file から verdict を抽出し agmsg メッセージで送る（codex wrapper から呼ばれる）。
#   cleanup <session-id>
#       reviewer window と manifest を削除する（元 session 自体は閉じない）。
#   selftest
#       純粋ロジック（収束判定/レビュアープロンプト生成/レビュアーコマンド生成）の自己テスト。
#
# 設計判断・取り込み経緯は agmsg-go の issues/ に記録（skills 層の取り込みは issues/0004）。
# 実装上の知見:
#   - 完了検知・verdict 受け渡しは agmsg メッセージ（プロセス終了/capture-pane 非依存）。
#   - レビュアープロンプトに判定パターン REVIEW_RESULT: <verdict> をリテラルで隣接させない
#     （codex exec はプロンプトを stdout=review_file にエコーするため、verdict 抽出が誤検知する）。
#     selftest に回帰ガードを置く。
#   - tmux-sidebar 等の pane 自動追加による send-keys 誤爆を防ぐため pane_id を固定する。
#   - 現在の worktree 上で動く（新規 worktree を作らない）。claude レビュアーは -p/--print 不使用。

set -euo pipefail

REVIEW_LOOP_DIR="${REVIEW_LOOP_DIR:-$HOME/.review-loop}"
MARKER_TIMEOUT="${REVIEW_LOOP_MARKER_TIMEOUT:-900}"   # verdict メッセージ待ちのタイムアウト(秒)
REVIEW_VERDICT_RE='REVIEW_RESULT:[[:space:]]*(APPROVED|CHANGES_REQUESTED)'

# 役割名（agmsg identity の name）。実装役 / レビュアーで固定。
IMPLEMENTER_NAME="implementer"
REVIEWER_NAME="reviewer"

# round ごとに実装役の受信 identity を分ける（残留 / クロスラウンドの verdict 誤受信を防ぐ）。
# 同一 team へ複数 round や二重起動の verdict が混じっても、round 別 inbox なので取り違えない。
# $1: round
implementer_round_name() { echo "${IMPLEMENTER_NAME}-r$1"; }

# このスクリプト自身の絶対パス（codex wrapper から notify-verdict を呼ぶため）。
SELF="$(cd "$(dirname "$0")" >/dev/null 2>&1 && pwd)/$(basename "$0")"

die() {
  echo "STATUS: ERROR"
  echo "MESSAGE: $1"
  tmux display-message -d 5000 "review-loop: ERROR: $1" 2>/dev/null || true
  exit 1
}

# pane へ送るコマンドは値を single quote で埋め込むため、`'` を含む値は quote が壊れて
# 任意 shell 断片として解釈されうる。埋め込む全ての値（パス・team・session-id 等）を拒否する。
# $1: ラベル, $2: 値
rl_reject_squote() {
  case "$2" in *\'*) die "$1 に single quote を含む値は未対応です: $2" ;; esac
}

# ============================================================
# 純粋ロジック（selftest 対象 — 副作用なし）
# ============================================================

# stdin から最後に出現した REVIEW_RESULT 行の判定語を大文字で echo する（無ければ空）。
# review_file（codex の stdout 捕捉先）や agmsg inbox 出力のいずれにも使える。
rl_verdict_word() {
  grep -oiE 'REVIEW_RESULT:[[:space:]]*(APPROVED|CHANGES_REQUESTED)' 2>/dev/null \
    | tail -n 1 | grep -oiE '(APPROVED|CHANGES_REQUESTED)' | tr '[:lower:]' '[:upper:]' || true
}

# レビュー出力ファイルから収束を判定する。最後の REVIEW_RESULT 行が APPROVED なら収束(exit 0)。
rl_review_converged() {
  local out_file="$1"
  [ -f "$out_file" ] || return 1
  [ "$(rl_verdict_word < "$out_file")" = "APPROVED" ]
}

# verdict メッセージ本文を組み立てる。round を併記して受信側が round を突合できるようにする。
# $1: verdict, $2: round
rl_verdict_body() { echo "REVIEW_RESULT: $1 round=$2"; }

# stdin から最後に出現した round=<N> の数値を echo する（無ければ空）。verdict↔round の突合に使う。
rl_verdict_round() {
  grep -oiE 'round=[0-9]+' 2>/dev/null | tail -n 1 | grep -oiE '[0-9]+' || true
}

# レビュアーへのプロンプトを stdout に生成する。
# $1: round, $2: base_ref, $3: 前ラウンドのレビューファイル(空可), $4: agent(claude|codex),
# $5: out_file(claude のとき書き出し先), $6: note_file(レビュー観点・空可), $7: team(claude の送信先 team),
# $8: agmsg_home(実装役と同じ AGMSG_HOME。claude が同じ DB へ送るため・空可)
rl_build_reviewer_prompt() {
  local round="$1" base_ref="$2" prev_review="${3:-}" agent="${4:-codex}" out_file="${5:-}" note_file="${6:-}" team="${7:-}" agmsg_home="${8:-}"
  # claude は Bash ツール(bash)で送信するため、AGMSG_HOME を env-prefix で渡す（実装役の wait-review と
  # 同じ DB を参照させる）。未設定なら prefix なし（両者とも既定の AGMSG_HOME）。
  local send_prefix=""
  [ -n "$agmsg_home" ] && send_prefix="AGMSG_HOME='$agmsg_home' "
  # round 別の受信 identity（実装役は round ごとに別 inbox を待つ）。
  local impl_round_name; impl_round_name=$(implementer_round_name "$round")
  cat <<EOF
あなたはコードレビュー担当です。このリポジトリの現在のブランチに加えられた変更をレビューしてください。コードは変更せず、レビューに徹してください。

- 変更全体を把握するには次を実行してください: \`git diff ${base_ref}...HEAD\` および \`git status\` / \`git diff\`（未コミットの作業ツリー変更も対象に含める）
- バグ・正しさの問題・抜けたエッジケース・既存コードとの不整合を優先して指摘してください
- 各指摘は「ファイル:行 — 問題 — 推奨対応」の形式で具体的に書いてください
- スタイルの好みではなく、修正すべき実質的な問題に絞ってください
EOF
  if [ -n "$note_file" ] && [ -f "$note_file" ]; then
    cat <<EOF

特に次の観点を重視してレビューしてください:
$(cat "$note_file")
EOF
  fi
  if [ -n "$prev_review" ] && [ -f "$prev_review" ]; then
    cat <<EOF

これは round ${round} の再レビューです。前回(round $((round - 1)))のレビュー指摘は以下です。実装役が対応済みか確認し、未対応・新規の問題のみを今回の指摘として挙げてください:

--- 前回のレビュー ---
$(cat "$prev_review")
--- 前回のレビューここまで ---
EOF
  fi
  # 重要: 判定行のフォーマットを説明する際、`REVIEW_RESULT:` の直後に判定語（APPROVED/CHANGES_REQUESTED）を
  # 隣接させて書かない。codex exec はプロンプトを stdout(=review_file)にエコーするため、プロンプト内に
  # 判定パターンがリテラルで含まれると verdict 抽出がプロンプトのエコーを誤検知する。selftest に回帰ガード。
  if [ "$agent" = claude ] && [ -n "$out_file" ]; then
    # claude は対話 TUI のため stdout を捕捉できない。レビュー結果をファイルに書き出させ、
    # 完了通知は claude 自身に agmsg send させる（実装役の待ち受けと deadlock しないため）。
    cat <<EOF

レビュー結果は、あなたの応答ではなく必ずファイル \`${out_file}\` に書き出してください（Write ツール等を使用）。
そのファイルの最後の行を判定行にしてください。判定行は \`REVIEW_RESULT:\` で始め、続けて半角スペースのあと判定語を1つ書きます。
判定語は、修正すべき実質的な問題が残っていなければ「承認」を表す語、対応すべき指摘が残っていれば「変更要求」を表す語にします:
  - 承認のときの判定語 … APPROVED
  - 変更要求のときの判定語 … CHANGES_REQUESTED

そして最後に、その判定語を使って次のコマンドを **必ず実行** してください（実装役へ完了を通知します。<判定語> を上記の実際の語に置換すること。`round=${round}` の部分はそのまま含めること）:

  ${send_prefix}agmsg send '${impl_round_name}' "REVIEW_RESULT: <判定語> round=${round}" --from '${REVIEWER_NAME}' --team '${team}'

（このメッセージで実装役が完了検知と収束判定を行います。宛先名と round はこの round 専用なので改変しないこと。コマンド先頭に環境変数の指定が付いている場合はそのまま含めて実行すること）
EOF
  else
    # codex(headless) は stdout がそのまま review_file になる。verdict 行を1行出力させ、
    # exec 終了後に wrapper（notify-verdict）がそれを抽出して agmsg send する。
    cat <<'EOF'

レビューの最後に、判定行をちょうど1行出力してください。判定行は `REVIEW_RESULT:` で始め、続けて半角スペースのあと判定語を1つ書きます。
判定語は、修正すべき実質的な問題が残っていなければ「承認」を表す語、対応すべき指摘が残っていれば「変更要求」を表す語にします:
  - 承認のときの判定語 … APPROVED
  - 変更要求のときの判定語 … CHANGES_REQUESTED
（この判定行を起動側が抽出し、実装役へ通知します）
EOF
  fi
}

# レビュアー起動コマンドを stdout に生成する。
# $1: agent, $2: work_dir, $3: prompt_file, $4: out_file(codex の stdout 捕捉先),
# $5: claude_sid, $6: team, $7: self_path(notify-verdict 呼び出し用 review-loop.sh パス),
# $8: agmsg_home(実装役と同じ AGMSG_HOME を notify-verdict へ引き渡す・空可), $9: round(codex notify の round 引き渡し)
rl_build_reviewer_cmd() {
  local agent="$1" work_dir="$2" prompt_file="$3" out_file="${4:-}" claude_sid="${5:-}" team="${6:-}" self_path="${7:-}" agmsg_home="${8:-}" round="${9:-}"
  case "$agent" in
    codex)
      # headless。read-only で機構的にコード変更を禁止。stdout(レビュー本文)だけを out_file に捕捉し、
      # stderr(codex の hook/進捗ログ)は <out_file>.log に分離する。exec 終了後に wrapper が verdict を
      # 抽出して agmsg send する（`;` で連鎖し exec の exit code によらず必ず通知を試みる）。
      # fish pane へ send-keys されるため、env-prefix(`VAR=val cmd`)や `$(...)` を使わず bash サブコマンドに委ねる。
      # notify-verdict(bash) は --home で受けた AGMSG_HOME を export してから送信するため、reviewer と
      # 実装役が同じ DB を参照する（pane へ env を伝播できない問題を回避）。--round で round 別 inbox へ送る。
      local notify="bash '$self_path' notify-verdict --team '$team' --out '$out_file'"
      [ -n "$round" ] && notify="$notify --round '$round'"
      [ -n "$agmsg_home" ] && notify="$notify --home '$agmsg_home'"
      printf "codex exec -C '%s' -s read-only - < '%s' > '%s' 2> '%s.log'; %s" \
        "$work_dir" "$prompt_file" "$out_file" "$out_file" "$notify"
      ;;
    claude)
      # interactive 起動（`-p`/`--print` は使わない＝subscription 課金）。各ラウンド stateless（前回レビューは
      # プロンプトに同梱）のため新規 session-id で起動し、verdict は claude 自身に agmsg send させる（プロンプト指示）。
      printf "claude --session-id '%s' < '%s'" "$claude_sid" "$prompt_file"
      ;;
    *)
      die "未知の reviewer agent: $agent"
      ;;
  esac
}

# base ref を解決する（origin/HEAD → main → master → HEAD~1）。
rl_resolve_base_ref() {
  local repo="$1"
  local base
  base=$(git -C "$repo" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's|refs/remotes/||')
  if [ -n "$base" ]; then echo "$base"; return; fi
  if git -C "$repo" show-ref --verify --quiet refs/heads/main 2>/dev/null; then echo "main"; return; fi
  if git -C "$repo" show-ref --verify --quiet refs/heads/master 2>/dev/null; then echo "master"; return; fi
  echo "HEAD~1"
}

# レビュー出力ディレクトリ（repo, session_id）。
out_dir_for() { echo "$1/.outputs/claude/review-loop/$2"; }

# session-id を衝突不能に採番する。`reviewloop-<repo_basename>-<utc_stamp>-<rand>`。
# 秒解像度のタイムスタンプだけでは並列 worktree / 同一秒起動で衝突するため、必ず乱数尾部を付ける。
# 文字種は session-id 制約（英数 . _ -）に収まるようサニタイズする。
# $1: repo_root
rl_mint_session_id() {
  local repo="$1" base rand stamp
  base=$(basename "$repo" 2>/dev/null | tr -c 'A-Za-z0-9._-' '-' | sed 's/-\{2,\}/-/g; s/^-//; s/-$//')
  [ -z "$base" ] && base="repo"
  stamp=$(date -u +%Y%m%d-%H%M%S)
  # openssl があれば 6 hex、無ければ $RANDOM 2 連で代替（どちらも英数のみ）。
  if command -v openssl >/dev/null 2>&1; then
    rand=$(openssl rand -hex 3)
  else
    rand=$(printf '%04x%04x' "$((RANDOM))" "$((RANDOM))")
  fi
  echo "reviewloop-${base}-${stamp}-${rand}"
}

# (session, round) の launch 排他ロックをアトミックに取得する（mkdir はアトミック）。
# 取得できれば 0、既に存在すれば 1。ロックは launch 中のみ保持し review-once 終了時に解放する。
# session-id は Fix A で run ごとに一意なので、万一ロックが残留しても新 run は別 session-id で影響を受けない。
# $1: lock_dir
rl_try_lock() { mkdir "$1" 2>/dev/null; }

# pane が生存し、かつレビュアー(agent / node 等)を実行中かを判定する（シェルに戻っていれば終了済み扱い）。
# 生存中なら 0。二重起動の検出に使う。
# $1: pane_id
rl_pane_alive() {
  local pane_id="$1" cmd
  [ -n "$pane_id" ] || return 1
  cmd=$(tmux display-message -p -t "$pane_id" '#{pane_current_command}' 2>/dev/null) || return 1
  case "$cmd" in
    fish|bash|zsh|sh|dash|-fish|-bash|-zsh|-sh) return 1 ;;  # シェルに戻っている＝レビュアー終了
    "") return 1 ;;
    *) return 0 ;;
  esac
}

# ============================================================
# manifest
# ============================================================

manifest_path() { echo "$REVIEW_LOOP_DIR/$1/manifest.json"; }

write_manifest() {
  local session_id="$1" repo_root="$2" work_dir="$3" base_ref="$4" tmux_session="$5" reviewer="$6" team="$7"
  mkdir -p "$REVIEW_LOOP_DIR/$session_id"
  jq -n \
    --arg sid "$session_id" --arg repo "$repo_root" --arg wd "$work_dir" \
    --arg base "$base_ref" --arg ts "$tmux_session" --arg rev "$reviewer" --arg team "$team" \
    '{session_id:$sid, repo_root:$repo, work_dir:$wd,
      base_ref:$base, tmux_session:$ts, reviewer:$rev, team:$team,
      panes:{}, rounds:[], state:"running", created_at:(now|todate)}' \
    > "$(manifest_path "$session_id")"
}

update_manifest() {
  local session_id="$1" filter="$2"
  local mf; mf=$(manifest_path "$session_id")
  [ -f "$mf" ] || return 0
  local tmp; tmp=$(mktemp)
  if jq "$filter" "$mf" > "$tmp" 2>/dev/null; then
    mv "$tmp" "$mf"
  else
    rm -f "$tmp"
  fi
}

manifest_get() {
  local session_id="$1" filter="$2"
  local mf; mf=$(manifest_path "$session_id")
  [ -f "$mf" ] || { echo ""; return 0; }
  jq -r "$filter // empty" "$mf" 2>/dev/null || true
}

# ============================================================
# tmux ペイン操作
# ============================================================

# pane の current command がシェルに戻る（＝レビュアーが終了して入力受付状態）まで待つ。
wait_pane_shell() {
  local pane_id="$1" timeout="${2:-30}" waited=0 cmd
  while [ "$waited" -lt "$timeout" ]; do
    cmd=$(tmux display-message -p -t "$pane_id" '#{pane_current_command}' 2>/dev/null || echo "")
    case "$cmd" in
      fish|bash|zsh|sh|dash|-fish|-bash|-zsh|-sh) return 0 ;;
    esac
    sleep 1; waited=$((waited + 1))
  done
  return 1
}

# pane が「コマンドを実際に実行できる対話プロンプト状態」になるまで待つ。
# wait_pane_shell は pane_current_command がシェル名になった時点で返るが、新規 window 直後は
# fish の対話初期化（config.fish 読込・welcome greeting 描画・readline 起動）が未完で、
# send-keys の末尾 Enter が初期化処理に飲まれてコマンドが実行されないレースがある（issues/0012）。
# ここではユニークマーカーを echo させ、その出力が capture-pane に単独行で現れることで
# 「Enter が効きシェルがコマンドを実行できる」状態を機構的に確認する（shell 非依存）。
# 確認できれば 0、timeout 内に確認できなければ 1。
# $1: pane_id, $2: timeout(sec, 既定 30)
wait_pane_ready() {
  local pane_id="$1" timeout="${2:-30}" marker out i attempts
  # まずシェル名に戻る（前コマンド終了 / シェル起動）のを待つ。
  wait_pane_shell "$pane_id" "$timeout" || return 1
  # 衝突しないマーカー（英数のみ。grep -F の素にするため記号を含めない）。
  marker="RLREADY${RANDOM}${RANDOM}"
  # 入力行に前 run の残骸があれば消してからマーカーを送る（残骸との連結・誤実行を防ぐ）。
  tmux send-keys -t "$pane_id" C-u 2>/dev/null || true
  tmux send-keys -t "$pane_id" "printf '%s\\n' $marker" Enter
  attempts=$((timeout * 3))   # 0.33s 間隔で timeout 秒ぶん試行する
  i=0
  while [ "$i" -lt "$attempts" ]; do
    out=$(tmux capture-pane -p -t "$pane_id" 2>/dev/null || echo "")
    # マーカー単独の行（= printf の出力）が見えたら ready。タイプされたコマンド行は
    # "printf '%s\n' RLREADY..." なので whole-line 完全一致(-x)では出力行とだけ一致する。
    if printf '%s\n' "$out" | grep -qxF "$marker"; then
      return 0
    fi
    sleep 0.33; i=$((i + 1))
  done
  return 1
}

# REPL に留まる claude を終了させる（Ctrl-D、効かなければ二重 Ctrl-C にフォールバック）。
terminate_claude() {
  local pane_id="$1"
  [ -n "$pane_id" ] || return 0
  tmux send-keys -t "$pane_id" C-d 2>/dev/null || true
  if wait_pane_shell "$pane_id" 15; then return 0; fi
  tmux send-keys -t "$pane_id" C-c 2>/dev/null || true
  tmux send-keys -t "$pane_id" C-c 2>/dev/null || true
  wait_pane_shell "$pane_id" 15 || true
}

# 既存の pane でコマンドを起動する（プロンプトが Enter を受理できる状態を確認してから送る）。
# ready 確認が取れたら 0、取れないままベストエフォートで送出したら 1 を返す（呼び出し側が
# LAUNCH_READY として可視化する）。ready が取れなくても die せず送出は試みる（window leak 回避。
# 起動失敗は wait-review の TIMEOUT と LAUNCH_READY: no で表面化する）。
launch_in_pane() {
  local pane_id="$1" work_dir="$2" cmd="$3" ready=0
  wait_pane_ready "$pane_id" 30 && ready=1 || true
  # マーカー実行後は入力行が空のはずだが、保険でもう一度クリアしてから本命を送る。
  tmux send-keys -t "$pane_id" C-u 2>/dev/null || true
  tmux send-keys -t "$pane_id" "cd '$work_dir'; $cmd" Enter
  [ "$ready" -eq 1 ]
}

# 指定 tmux session に新しい window を作り pane_id を返す（pane_id を固定して send-keys 誤爆を防ぐ）。
create_role_window() {
  local tmux_session="$1" name="$2" work_dir="$3" pid
  pid=$(tmux new-window -P -F '#{pane_id}' -t "=${tmux_session}:" -n "$name" -c "$work_dir") || return 1
  tmux set-option -t "$pid" allow-rename off 2>/dev/null || true
  echo "$pid"
}

# ============================================================
# サブコマンド: review-once
# ============================================================

cmd_review_once() {
  local repo_root="" session_id="" reviewer="" round="" base_ref="" note_file=""

  while [ $# -gt 0 ]; do
    case "$1" in
      --session-id) session_id="$2"; shift 2 ;;
      --reviewer)   reviewer="$2"; shift 2 ;;
      --round)      round="$2"; shift 2 ;;
      --base)       base_ref="$2"; shift 2 ;;
      --note)       note_file="$2"; shift 2 ;;
      *) if [ -z "$repo_root" ]; then repo_root="$1"; fi; shift ;;
    esac
  done

  [ -z "$repo_root" ]  && die "repo-root が指定されていません"
  [ -z "$reviewer" ]   && die "--reviewer が指定されていません"
  [ -z "$round" ]      && die "--round が指定されていません"
  [ ! -d "$repo_root" ] && die "リポジトリが見つかりません: $repo_root"
  # Fix A: --session-id 省略時は衝突不能な値を自前採番する（並列 worktree / 同一秒起動でも衝突しない）。
  # 採番値は呼び出し側が下の SESSION_ID: 出力から拾い、round2+ / wait-review / cleanup で再利用する。
  [ -z "$session_id" ] && session_id=$(rl_mint_session_id "$repo_root")
  case "$reviewer" in claude|codex) ;; *) die "--reviewer は claude または codex: $reviewer" ;; esac
  case "$round" in (''|*[!0-9]*) die "--round は整数で指定してください: $round" ;; esac
  [ "$round" -lt 1 ] && die "--round は 1 以上で指定してください"

  command -v jq >/dev/null 2>&1 || die "jq が利用できません"
  command -v agmsg >/dev/null 2>&1 || die "agmsg が利用できません（PATH に必要 — agent 間連絡に使用）"
  command -v "$reviewer" >/dev/null 2>&1 || die "$reviewer が利用できません"
  tmux display-message -p '#{session_name}' >/dev/null 2>&1 || die "tmux セッション外では動作しません"
  git -C "$repo_root" rev-parse --git-dir >/dev/null 2>&1 || die "git リポジトリではありません: $repo_root"

  local work_dir="$repo_root"
  [ -z "$base_ref" ] && base_ref=$(rl_resolve_base_ref "$work_dir")

  # pane へ送るコマンドに single quote で埋め込む値はすべて `'` を拒否する。
  # SELF（notify-verdict 呼び出しに埋め込む review-loop.sh パス）も対象。
  rl_reject_squote "work_dir" "$work_dir"
  rl_reject_squote "review-loop.sh パス" "$SELF"
  # session_id は out_dir / review_prompt / review_out / team(= agmsg の team 名)の素になり、
  # codex / claude 双方の pane コマンドに埋め込まれる。空白・`;`・quote 等が混ざるとコマンドが
  # 壊れる/injection になるため、shell-safe な文字種（英数・. _ -）に限定する。
  case "$session_id" in
    ''|*[!A-Za-z0-9._-]*) die "session-id は英数と . _ - のみ使用できます: $session_id" ;;
  esac

  # 実装役(この review-once / wait-review)と同じ AGMSG_HOME を reviewer 側へ伝播する。
  local agmsg_home="${AGMSG_HOME:-}"
  rl_reject_squote "AGMSG_HOME" "$agmsg_home"

  # per-session の ephemeral team（= session-id）。明示 identity フラグで送受信するため join 不要。
  local team="$session_id"

  local out_dir; out_dir=$(out_dir_for "$work_dir" "$session_id")
  mkdir -p "$out_dir"
  local review_out="$out_dir/round-${round}-review.md"
  local review_prompt="$out_dir/round-${round}-review-prompt.txt"
  local prev_review=""
  if [ "$round" -gt 1 ] && [ -f "$out_dir/round-$((round - 1))-review.md" ]; then
    prev_review="$out_dir/round-$((round - 1))-review.md"
  fi

  # claude レビュアーのみ session-id と書き出し先をプロンプトに渡す。
  local claude_sid="" prompt_out_file=""
  if [ "$reviewer" = claude ]; then
    claude_sid=$(uuidgen | tr '[:upper:]' '[:lower:]')
    rl_reject_squote "claude session-id" "$claude_sid"
    prompt_out_file="$review_out"
  fi

  rl_build_reviewer_prompt "$round" "$base_ref" "$prev_review" "$reviewer" "$prompt_out_file" "$note_file" "$team" "$agmsg_home" > "$review_prompt"

  # --- Fix B: (session, round) 二重起動ガード ---
  # codex 2 体が同一 round-N-review.md を奪い合い verdict が壊れる事故の再発防止。
  # launch をロックで排他し、その内側で既存 pane の生存を確認する（TOCTOU を避けるためロックが先）。
  mkdir -p "$REVIEW_LOOP_DIR/$session_id"
  # lock_dir はあえて local にしない。EXIT trap は関数 return 後（スクリプト exit 時）に評価されるため、
  # local だとその時点でスコープ外となり set -u 下で "lock_dir: unbound variable" になって exit 1 する。
  # スクリプトは 1 回 1 サブコマンドの短命プロセスなのでグローバルでも干渉しない。
  lock_dir="$REVIEW_LOOP_DIR/$session_id/round-${round}.lock"
  if ! rl_try_lock "$lock_dir"; then
    echo "STATUS: LAUNCH_IN_PROGRESS"
    echo "SESSION_ID: $session_id"
    echo "ROUND: $round"
    echo "MESSAGE: round $round の launch が並行実行中です（二重起動を抑止）。完了後に再確認してください"
    return 0
  fi
  trap 'rmdir "$lock_dir" 2>/dev/null || true' EXIT
  local existing_pane; existing_pane=$(manifest_get "$session_id" ".panes[\"$round\"]")
  if rl_pane_alive "$existing_pane"; then
    echo "STATUS: ALREADY_RUNNING"
    echo "SESSION_ID: $session_id"
    echo "ROUND: $round"
    echo "REVIEWER: $reviewer"
    echo "PANE_ID: $existing_pane"
    echo "REVIEW_OUT: $review_out"
    echo "MESSAGE: round $round のレビュアーが既に pane $existing_pane で稼働中です（二重起動を抑止）"
    return 0
  fi

  local tmux_session; tmux_session=$(tmux display-message -p '#{session_name}')
  local pane
  pane=$(create_role_window "$tmux_session" "rl-rev-$round" "$work_dir") || die "reviewer window の作成に失敗しました"

  local rcmd
  rcmd=$(rl_build_reviewer_cmd "$reviewer" "$work_dir" "$review_prompt" "$review_out" "$claude_sid" "$team" "$SELF" "$agmsg_home" "$round")
  local launch_ready=yes
  launch_in_pane "$pane" "$work_dir" "$rcmd" || launch_ready=no

  [ "$round" -eq 1 ] && [ ! -f "$(manifest_path "$session_id")" ] && \
    write_manifest "$session_id" "$repo_root" "$work_dir" "$base_ref" "$tmux_session" "$reviewer" "$team"
  update_manifest "$session_id" ".reviewer=\"$reviewer\" | .base_ref=\"$base_ref\" | .tmux_session=\"$tmux_session\" | .team=\"$team\" | .panes[\"$round\"]=\"$pane\""

  echo "STATUS: STARTED"
  echo "SESSION_ID: $session_id"
  echo "ROUND: $round"
  echo "REVIEWER: $reviewer"
  echo "TEAM: $team"
  echo "BASE_REF: $base_ref"
  echo "PANE_ID: $pane"
  echo "REVIEW_OUT: $review_out"
  echo "LAUNCH_READY: $launch_ready"
  if [ "$launch_ready" = no ]; then
    echo "WARNING: pane $pane が起動コマンドを受理できる状態を確認できないまま送出しました（fish 初期化の遅延等）。レビュアーが起動していない可能性があります。pane を確認し、未起動なら cleanup 後に再実行してください"
    tmux display-message -d 6000 "review-loop: round $round launch not confirmed (pane $pane)" 2>/dev/null || true
  fi
  tmux display-message -d 4000 "review-loop: round $round review started ($reviewer) [$session_id]" 2>/dev/null || true
}

# ============================================================
# サブコマンド: notify-verdict（codex wrapper から呼ばれる）
# ============================================================

cmd_notify_verdict() {
  local team="" out_file="" to_name="" from_name="$REVIEWER_NAME" home="" round=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --team)  team="$2"; shift 2 ;;
      --out)   out_file="$2"; shift 2 ;;
      --to)    to_name="$2"; shift 2 ;;
      --from)  from_name="$2"; shift 2 ;;
      --home)  home="$2"; shift 2 ;;
      --round) round="$2"; shift 2 ;;
      *) shift ;;
    esac
  done
  [ -z "$team" ]     && die "--team が指定されていません"
  [ -z "$out_file" ] && die "--out が指定されていません"
  # 宛先は --to 明示があれば優先、なければ round 別 identity（Fix C: round ごとに inbox を分離）。
  if [ -z "$to_name" ]; then
    if [ -n "$round" ]; then to_name=$(implementer_round_name "$round"); else to_name="$IMPLEMENTER_NAME"; fi
  fi
  # 実装役(wait-review)と同じ DB を参照させる。pane に env を伝播できないため --home で受けて export する。
  [ -n "$home" ] && export AGMSG_HOME="$home"
  command -v agmsg >/dev/null 2>&1 || die "agmsg が利用できません"

  # review_file から verdict を抽出。無ければ安全側に CHANGES_REQUESTED（実装役が再確認できる）。
  local verdict=""
  [ -f "$out_file" ] && verdict=$(rl_verdict_word < "$out_file")
  [ -z "$verdict" ] && verdict="CHANGES_REQUESTED"

  # round を併記して受信側が round を突合できるようにする（round 不明なら従来形）。
  local body
  if [ -n "$round" ]; then body=$(rl_verdict_body "$verdict" "$round"); else body="REVIEW_RESULT: $verdict"; fi

  agmsg send "$to_name" "$body" --from "$from_name" --team "$team" \
    || die "agmsg send に失敗しました（team=$team to=$to_name）"

  echo "STATUS: NOTIFIED"
  echo "VERDICT: $verdict"
  echo "ROUND: $round"
  echo "TEAM: $team"
}

# ============================================================
# サブコマンド: wait-review
# ============================================================

cmd_wait_review() {
  local repo_root="" session_id="" round="" timeout="$MARKER_TIMEOUT"

  while [ $# -gt 0 ]; do
    case "$1" in
      --session-id) session_id="$2"; shift 2 ;;
      --round)      round="$2"; shift 2 ;;
      --timeout)    timeout="$2"; shift 2 ;;
      *) if [ -z "$repo_root" ]; then repo_root="$1"; fi; shift ;;
    esac
  done

  [ -z "$repo_root" ]  && die "repo-root が指定されていません"
  [ -z "$session_id" ] && die "--session-id が指定されていません"
  [ -z "$round" ]      && die "--round が指定されていません"
  case "$round" in (''|*[!0-9]*) die "--round は整数で指定してください: $round" ;; esac
  case "$timeout" in (''|*[!0-9]*) die "--timeout は整数(秒)で指定してください: $timeout" ;; esac
  command -v jq >/dev/null 2>&1 || die "jq が利用できません"
  command -v agmsg >/dev/null 2>&1 || die "agmsg が利用できません"

  local out_dir; out_dir=$(out_dir_for "$repo_root" "$session_id")
  local review_out="$out_dir/round-${round}-review.md"
  local reviewer; reviewer=$(manifest_get "$session_id" ".reviewer")
  local team; team=$(manifest_get "$session_id" ".team")
  local pane; pane=$(manifest_get "$session_id" ".panes[\"$round\"]")
  [ -z "$team" ] && team="$session_id"

  # reviewer → implementer の REVIEW_RESULT メッセージを agmsg inbox のポーリングで待つ。
  # Fix C: 受信 identity は round 別（implementer-r<round>）。round ごとに inbox が分かれるので、
  # 残留・クロスラウンド・二重起動の verdict が混じっても取り違えない。inbox は破壊読みだが、
  # この inbox にはこの round 宛てしか入らないため全件ドレインしても安全。
  local recipient; recipient=$(implementer_round_name "$round")
  local waited=0 verdict="" inbox_out="" msg_round=""
  while [ "$waited" -lt "$timeout" ]; do
    inbox_out=$(agmsg inbox --name "$recipient" --team "$team" 2>/dev/null || true)
    verdict=$(printf '%s\n' "$inbox_out" | rl_verdict_word)
    [ -n "$verdict" ] && break
    sleep 5; waited=$((waited + 5))
  done

  if [ -z "$verdict" ]; then
    update_manifest "$session_id" ".rounds += [{round:$round, verdict:\"TIMEOUT\", at:(now|todate)}]"
    echo "STATUS: TIMEOUT"
    echo "ROUND: $round"
    echo "MESSAGE: round $round のレビュアーが ${timeout}s 以内に REVIEW_RESULT を送信しませんでした"
    echo "REVIEW_OUT: $review_out"
    return 1
  fi

  # 受信本文の round=<N> がこの round と一致するか突合（不一致は誤受信の兆候として警告）。
  msg_round=$(printf '%s\n' "$inbox_out" | rl_verdict_round)

  # verdict 受信後に reviewer pane を後始末。claude は REPL に留まるので終了させる。
  # codex は exec 終了後に wrapper が送信しているので既にシェルへ戻っている。
  if [ "$reviewer" = claude ]; then
    terminate_claude "$pane"
  else
    wait_pane_shell "$pane" 120 || true
  fi

  update_manifest "$session_id" ".rounds += [{round:$round, verdict:\"$verdict\", at:(now|todate)}]"

  # Fix D: 受信 verdict と round-N-review.md 末尾の判定を突合。食い違えば二重起動・クロストークの兆候。
  local file_verdict="" consistency="OK"
  [ -f "$review_out" ] && file_verdict=$(rl_verdict_word < "$review_out")
  if [ -n "$file_verdict" ] && [ "$file_verdict" != "$verdict" ]; then
    consistency="INCONSISTENT(msg=$verdict,file=$file_verdict)"
  fi
  if [ -n "$msg_round" ] && [ "$msg_round" != "$round" ]; then
    consistency="INCONSISTENT(msg_round=$msg_round,want=$round)"
  fi

  echo "STATUS: REVIEWED"
  echo "ROUND: $round"
  echo "VERDICT: $verdict"
  echo "CONSISTENCY: $consistency"
  echo "REVIEW_OUT: $review_out"
  if [ "$consistency" != "OK" ]; then
    echo "WARNING: verdict の整合に不一致を検出しました（二重起動 / クロストークの可能性）。round-${round}-review.md と pane を確認してください: $consistency"
    tmux display-message -d 6000 "review-loop: round $round verdict 不一致 $consistency" 2>/dev/null || true
  fi
}

# ============================================================
# サブコマンド: cleanup
# ============================================================

cmd_cleanup() {
  local session_id="${1:-}"
  [ -z "$session_id" ] && die "session-id が指定されていません"
  local mf; mf=$(manifest_path "$session_id")
  [ -f "$mf" ] || die "マニフェストが見つかりません: $mf"
  command -v jq >/dev/null 2>&1 || die "jq が利用できません"

  local deleted=()

  # reviewer window のみ削除する（元 coding session のある tmux session 自体は閉じない）。
  local pane
  while IFS= read -r pane; do
    [ -z "$pane" ] && continue
    if tmux kill-window -t "$pane" 2>/dev/null; then
      deleted+=("window:$pane")
    fi
  done < <(jq -r '.panes // {} | to_entries[].value' "$mf" 2>/dev/null || true)

  rm -rf "$REVIEW_LOOP_DIR/$session_id"
  deleted+=("manifest:$session_id")

  echo "STATUS: CLEANED"
  if [ "${#deleted[@]}" -gt 0 ]; then
    printf 'DELETED: %s\n' "${deleted[@]}"
  fi
}

# ============================================================
# サブコマンド: selftest（純粋ロジックのみ・副作用なし）
# ============================================================

cmd_selftest() {
  local fail=0
  local tmp; tmp=$(mktemp -d)
  trap "rm -rf '$tmp'" EXIT

  assert_contains() { case "$3" in (*"$2"*) echo "ok  $1" ;; (*) echo "NG  $1 [needle=$2]"; fail=1 ;; esac; }
  assert_excludes() { case "$3" in (*"$2"*) echo "NG  $1 [見つかってはいけない: $2]"; fail=1 ;; (*) echo "ok  $1" ;; esac; }
  # プロンプトに判定パターン(REVIEW_RESULT: <verdict>)がリテラルで含まれないこと（codex のプロンプトエコー誤検知の回帰防止）
  assert_no_verdict() {
    if grep -qE 'REVIEW_RESULT:[[:space:]]*(APPROVED|CHANGES_REQUESTED)' <<<"$2"; then
      echo "NG  $1 [判定パターンがプロンプトに混入]"; fail=1
    else echo "ok  $1"; fi
  }
  check_true()  { local label="$1"; shift; if "$@"; then echo "ok  $label"; else echo "NG  $label"; fail=1; fi; }
  check_false() { local label="$1"; shift; if "$@"; then echo "NG  $label"; fail=1; else echo "ok  $label"; fi; }

  # --- verdict 抽出 / 収束判定 ---
  printf 'some findings\nREVIEW_RESULT: APPROVED\n' > "$tmp/a.md"
  check_true  "converged: APPROVED" rl_review_converged "$tmp/a.md"
  printf 'REVIEW_RESULT: CHANGES_REQUESTED\n' > "$tmp/b.md"
  check_false "not-converged: CHANGES_REQUESTED" rl_review_converged "$tmp/b.md"
  printf 'REVIEW_RESULT: changes_requested\n...fixed...\nreview_result: approved\n' > "$tmp/c.md"
  check_true  "converged: 最後の行(approved)を採用" rl_review_converged "$tmp/c.md"
  check_false "not-converged: 欠損ファイル" rl_review_converged "$tmp/missing"
  printf 'no verdict line here\n' > "$tmp/d.md"
  check_false "not-converged: 判定行なし" rl_review_converged "$tmp/d.md"
  # inbox 出力（1 行プロトコル）からの verdict 抽出
  local inbox_line='1970-01-01T00:00:00Z | reviewloop-x | reviewer → implementer | REVIEW_RESULT: APPROVED'
  assert_contains "verdict from inbox line" "APPROVED" "$(printf '%s\n' "$inbox_line" | rl_verdict_word)"

  # --- single quote guard（die が exit するため subshell で検証）---
  if ( rl_reject_squote "t" "/safe/path-ok" ) >/dev/null 2>&1; then echo "ok  reject_squote: 安全な値は通る"; else echo "NG  reject_squote: 安全な値が拒否された"; fail=1; fi
  if ( rl_reject_squote "t" "has'quote" ) >/dev/null 2>&1; then echo "NG  reject_squote: ' を含む値が通った"; fail=1; else echo "ok  reject_squote: ' を含む値を拒否"; fi

  # --- レビュアープロンプト ---
  printf 'レビュー指摘ABC' > "$tmp/review.md"
  printf 'パフォーマンス観点XYZ' > "$tmp/note.md"
  local rev_codex rev_claude
  rev_codex=$(rl_build_reviewer_prompt 1 main "" codex "" "" reviewloop-x "$HOME/.agents/skills/agmsg")
  rev_claude=$(rl_build_reviewer_prompt 1 main "" claude "$tmp/round-1-review.md" "" reviewloop-x "$HOME/.agents/skills/agmsg")
  assert_contains  "review prompt(codex): REVIEW_RESULT 指示"        'REVIEW_RESULT:'    "$rev_codex"
  assert_contains  "review prompt(codex): APPROVED 言及"             'APPROVED'          "$rev_codex"
  assert_contains  "review prompt(codex): CHANGES_REQUESTED 言及"    'CHANGES_REQUESTED' "$rev_codex"
  assert_no_verdict "review prompt(codex): 判定パターン非混入(回帰防止)" "$rev_codex"
  assert_no_verdict "review prompt(claude): 判定パターン非混入(回帰防止)" "$rev_claude"
  assert_contains  "review prompt(claude): ファイル書き出し指示"     'round-1-review.md' "$rev_claude"
  assert_contains  "review prompt(claude): agmsg send 指示"         'agmsg send'        "$rev_claude"
  assert_contains  "review prompt(claude): round 別の宛先 identity" "'implementer-r1'"  "$rev_claude"
  assert_contains  "review prompt(claude): body に round 併記"       'round=1'           "$rev_claude"
  assert_contains  "review prompt(claude): team を quote して埋め込み" "--team 'reviewloop-x'" "$rev_claude"
  assert_contains  "review prompt(claude): AGMSG_HOME 伝播"         'AGMSG_HOME='       "$rev_claude"
  # AGMSG_HOME 未指定なら prefix を付けない（両者とも既定 DB）
  assert_excludes  "review prompt(claude): home 空なら prefix なし" 'AGMSG_HOME=' "$(rl_build_reviewer_prompt 1 main "" claude "$tmp/round-1-review.md" "" reviewloop-x "")"
  assert_excludes  "review prompt(codex): ファイル書き出し指示は無い" 'Write ツール'     "$rev_codex"
  assert_excludes  "review prompt(codex): agmsg send 指示は無い"     'agmsg send'       "$rev_codex"
  assert_contains  "review prompt r2(codex): 前回レビュー同梱"       'レビュー指摘ABC'   "$(rl_build_reviewer_prompt 2 main "$tmp/review.md" codex "" "" reviewloop-x)"
  assert_contains  "review prompt(codex): note(観点)反映"           'パフォーマンス観点XYZ' "$(rl_build_reviewer_prompt 1 main "" codex "" "$tmp/note.md" reviewloop-x)"

  # --- レビュアーコマンド生成 ---
  local c
  c=$(rl_build_reviewer_cmd codex /w /p/rv /o/rev.md "" reviewloop-x /skills/review-loop.sh /home/u/.agents/skills/agmsg 2)
  assert_contains "cmd codex reviewer: round 引き渡し"  "--round '2'"        "$c"
  assert_contains "cmd codex reviewer: codex exec"      'codex exec'         "$c"
  assert_contains "cmd codex reviewer: read-only"       '-s read-only'       "$c"
  assert_contains "cmd codex reviewer: stdout 捕捉"     "> '/o/rev.md'"      "$c"
  # stderr(hook ログ)を out_file に合流させない。別ファイル <out_file>.log に分離する（review.md 汚染防止の回帰ガード）
  assert_excludes "cmd codex reviewer: 2>&1 で合流しない" '2>&1'             "$c"
  assert_contains "cmd codex reviewer: stderr 分離"     "2> '/o/rev.md.log'" "$c"
  # exec 終了後に notify-verdict を連鎖する（verdict を agmsg send）
  assert_contains "cmd codex reviewer: notify-verdict 連鎖" 'notify-verdict' "$c"
  assert_contains "cmd codex reviewer: team 引き渡し"   "--team 'reviewloop-x'" "$c"
  assert_contains "cmd codex reviewer: AGMSG_HOME 引き渡し" "--home '/home/u/.agents/skills/agmsg'" "$c"
  # home 空なら --home を付けない
  assert_excludes "cmd codex reviewer: home 空なら --home なし" '--home' "$(rl_build_reviewer_cmd codex /w /p/rv /o/rev.md '' reviewloop-x /skills/review-loop.sh '')"
  # fish pane へ送るため env-prefix / コマンド置換は使わない
  assert_excludes "cmd codex reviewer: env-prefix 不使用" 'AGMSG_NAME='       "$c"
  assert_excludes "cmd codex reviewer: \$() 不使用"       '$('               "$c"
  c=$(rl_build_reviewer_cmd claude /w /p/rv /o/rev.md sid-rev reviewloop-x /skills/review-loop.sh)
  assert_contains "cmd claude reviewer: --session-id"  "--session-id 'sid-rev'" "$c"
  assert_excludes "cmd claude reviewer: -p 不使用"     ' -p '            "$c"
  assert_excludes "cmd claude reviewer: --print 不使用" '--print'        "$c"

  # --- round 別 identity / verdict payload（Fix C）---
  assert_contains "implementer_round_name: round 別宛先" "implementer-r3" "$(implementer_round_name 3)"
  assert_contains "verdict body: verdict 含む"           "APPROVED"        "$(rl_verdict_body APPROVED 2)"
  assert_contains "verdict body: round 併記"             "round=2"         "$(rl_verdict_body APPROVED 2)"
  # body から round / verdict を双方向に取り出せる
  assert_contains "verdict round 抽出"                   "2"  "$(printf '%s\n' "$(rl_verdict_body CHANGES_REQUESTED 2)" | rl_verdict_round)"
  assert_contains "verdict word: round 併記でも抽出可"   "CHANGES_REQUESTED" "$(printf '%s\n' "$(rl_verdict_body CHANGES_REQUESTED 2)" | rl_verdict_word)"

  # --- (session, round) 二重起動ロック（Fix B）---
  local ld="$tmp/lockdir"
  check_true  "try_lock: 初回は取得できる" rl_try_lock "$ld"
  check_false "try_lock: 2 回目は弾く（再入拒否）" rl_try_lock "$ld"
  rmdir "$ld"
  check_true  "try_lock: 解放後は再取得できる" rl_try_lock "$ld"
  rmdir "$ld"

  # --- session-id 採番（Fix A）: 衝突不能・文字種制約を満たす ---
  local sid1 sid2
  sid1=$(rl_mint_session_id /tmp/my-repo)
  sid2=$(rl_mint_session_id /tmp/my-repo)
  assert_contains "mint session-id: prefix"        "reviewloop-" "$sid1"
  assert_contains "mint session-id: repo basename" "my-repo"     "$sid1"
  check_true  "mint session-id: 文字種制約(英数 . _ -)を満たす" bash -c '[[ "'"$sid1"'" =~ ^[A-Za-z0-9._-]+$ ]]'
  check_false "mint session-id: 同一 repo でも毎回異なる（衝突不能）" test "$sid1" = "$sid2"

  if [ "$fail" -eq 0 ]; then
    echo "SELFTEST: PASS"
  else
    echo "SELFTEST: FAIL"
    return 1
  fi
}

# ============================================================
# メイン
# ============================================================

subcommand="${1:-}"
shift || true
case "$subcommand" in
  review-once)    cmd_review_once "$@" ;;
  wait-review)    cmd_wait_review "$@" ;;
  notify-verdict) cmd_notify_verdict "$@" ;;
  cleanup)        cmd_cleanup "$@" ;;
  selftest)       cmd_selftest ;;
  *) die "Unknown subcommand: $subcommand. Usage: review-loop.sh {review-once|wait-review|notify-verdict|cleanup|selftest}" ;;
esac
