#!/usr/bin/env bash
set -euo pipefail

# dispatch.sh - 別リポジトリで tmux window を作り launcher（claude / codex）を起動する軽量ツール
#
# サブコマンド:
#   launch <repo> "<prompt>" [--launcher claude|codex] [--session <name>] [--window <name>] [--branch <name>] [--no-worktree] [--no-prompt] [--prompt-file <path>]
#   list-repos

GHQ_ROOT="$(ghq root 2>/dev/null || echo "$HOME/ghq")"

die() {
  echo "STATUS: ERROR"
  echo "MESSAGE: $1"
  tmux display-message -d 5000 "dispatch: ERROR: $1" 2>/dev/null || true
  exit 1
}

notify() {
  echo "STATUS: $1"
  tmux display-message -d 600000 "dispatch: $1" 2>/dev/null || true
}

# pane が「コマンドを実際に実行できる対話プロンプト状態」になるまで待つ（agmsg-go issues/0012）。
# pane_current_command がシェル名になっても fish 等の対話初期化（config.fish 読込・welcome 描画・
# readline 起動）が未完だと、send-keys の末尾 Enter が初期化に飲まれてコマンドが実行されないレースがある。
# 固定 sleep では負荷次第で待ちきれないため、ユニークマーカーを echo させ、その出力が capture-pane に
# 単独行で現れることで「Enter が効きシェルがコマンドを実行できる」状態を機構的に確認する（shell 非依存）。
# 確認できれば 0、timeout 内に確認できなければ 1。 $1: pane_id, $2: timeout(sec, 既定 30)
wait_pane_ready() {
  local pane_id="$1" timeout="${2:-30}" marker out i attempts cmd waited=0
  # まずシェル名になる（シェルが起動する）まで待つ。
  while [ "$waited" -lt "$timeout" ]; do
    cmd=$(tmux display-message -p -t "$pane_id" '#{pane_current_command}' 2>/dev/null || echo "")
    case "$cmd" in fish|bash|zsh|sh|dash|-fish|-bash|-zsh|-sh) break ;; esac
    sleep 1; waited=$((waited + 1))
  done
  # 衝突しないマーカー（英数のみ。grep -F の素にするため記号を含めない）。
  marker="DISPATCHREADY${RANDOM}${RANDOM}"
  # 入力行に残骸があれば消してからマーカーを送る（残骸との連結・誤実行を防ぐ）。
  tmux send-keys -t "$pane_id" C-u 2>/dev/null || true
  tmux send-keys -t "$pane_id" "printf '%s\\n' $marker" Enter
  attempts=$((timeout * 3))   # 0.33s 間隔で timeout 秒ぶん試行する
  i=0
  while [ "$i" -lt "$attempts" ]; do
    out=$(tmux capture-pane -p -t "$pane_id" 2>/dev/null || echo "")
    # マーカー単独の行（= printf の出力）が見えたら ready。タイプされたコマンド行は
    # "printf '%s\n' DISPATCHREADY..." なので whole-line 完全一致(-x)では出力行とだけ一致する。
    if printf '%s\n' "$out" | grep -qxF "$marker"; then
      return 0
    fi
    sleep 0.33; i=$((i + 1))
  done
  return 1
}

# repo パス解決: フルパス or ghq 短縮名（your-org/your-sandbox）
resolve_repo() {
  local repo="$1"
  if [ -d "$repo" ]; then
    echo "$repo"
    return
  fi
  # ghq 短縮名を試す（github.com/ を補完）
  local candidates=("$GHQ_ROOT/github.com/$repo" "$GHQ_ROOT/$repo")
  for candidate in "${candidates[@]}"; do
    if [ -d "$candidate" ]; then
      echo "$candidate"
      return
    fi
  done
  return 1
}

# git worktree 作成: gw_add と同じ命名規則 (<main_worktree>@<branch_dir_name>)
create_worktree() {
  local repo_path="$1"
  local branch_name="$2"

  local main_worktree
  main_worktree=$(git -C "$repo_path" worktree list --porcelain | head -n1 | sed 's/^worktree //')
  if [ -z "$main_worktree" ]; then
    die "メインworktreeのパスを取得できません: $repo_path"
  fi

  # branch がいずれかの worktree でチェックアウト済みなら、そのパスを返す（resume）
  local existing_path
  existing_path=$(git -C "$repo_path" worktree list --porcelain \
    | awk -v b="$branch_name" '/^worktree /{sub(/^worktree /,""); p=$0} /^branch refs\/heads\//{sub(/^branch /,""); if($0=="refs/heads/"b) print p}')
  if [ -n "$existing_path" ] && [ -d "$existing_path" ]; then
    echo "$existing_path"
    return
  fi

  local worktree_dir_name
  worktree_dir_name=$(echo "$branch_name" | tr '/' '-')
  local worktree_path="$main_worktree@$worktree_dir_name"

  # 希望 path が別 branch の worktree で使用中の場合、suffix を付けて新規作成する
  # （手動で別ブランチに切り替えた残骸などを乗っ取らないため）
  if [ -d "$worktree_path" ]; then
    local base="$worktree_path"
    worktree_path="${base}-$(date +%H%M%S)"
    local i=2
    while [ -d "$worktree_path" ]; do
      worktree_path="${base}-$(date +%H%M%S)-${i}"
      i=$((i + 1))
      if [ "$i" -gt 100 ]; then
        die "worktree path の衝突を解決できません: $base"
      fi
    done
  fi

  # リモートの最新を取得
  git -C "$repo_path" fetch origin 2>/dev/null || true

  # デフォルトブランチを特定
  local default_branch
  default_branch=$(git -C "$repo_path" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's|refs/remotes/origin/||')
  if [ -z "$default_branch" ]; then
    default_branch="main"
  fi

  # branch の存在確認: ローカルにあるか / リモートにあるか
  local local_branch_exists=no
  if git -C "$repo_path" show-ref --verify --quiet "refs/heads/$branch_name" 2>/dev/null; then
    local_branch_exists=yes
  fi
  local remote_branch_exists
  remote_branch_exists=$(git -C "$repo_path" ls-remote --heads origin "$branch_name" 2>/dev/null || true)

  if [ "$local_branch_exists" = yes ] || [ -n "$remote_branch_exists" ]; then
    # 既存ブランチを worktree にチェックアウト（remote にしか無ければ先に fetch）
    if [ -n "$remote_branch_exists" ] && [ "$local_branch_exists" = no ]; then
      git -C "$repo_path" fetch origin "$branch_name":"$branch_name" 2>/dev/null || true
    fi
    git -C "$repo_path" worktree add "$worktree_path" "$branch_name" >&2 \
      || die "worktree の作成に失敗しました: $worktree_path"
  else
    # 新規 branch を作成
    git -C "$repo_path" worktree add "$worktree_path" -b "$branch_name" "origin/${default_branch}" >&2 \
      || die "worktree の作成に失敗しました: $worktree_path"
  fi

  # .claude/settings.local.json をコピー（gw_add と同じ）
  if [ -f "$main_worktree/.claude/settings.local.json" ]; then
    mkdir -p "$worktree_path/.claude"
    cp "$main_worktree/.claude/settings.local.json" "$worktree_path/.claude/settings.local.json"
  fi

  echo "$worktree_path"
}

# メインworktree のパスを返し、作業ツリーが clean ならデフォルトブランチに checkout する。
# no-worktree-repos 設定によるトリガー時のみ呼ばれる（明示 --no-worktree のみの場合は呼ばれない）。
checkout_default_branch() {
  local repo_path="$1"

  local main_worktree
  main_worktree=$(git -C "$repo_path" worktree list --porcelain 2>/dev/null | head -n1 | sed 's/^worktree //')
  if [ -z "$main_worktree" ] || [ ! -d "$main_worktree" ]; then
    echo "$repo_path"
    return
  fi

  # デフォルトブランチを特定（origin/HEAD → main → master の順）
  local default_branch
  default_branch=$(git -C "$main_worktree" symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's|refs/remotes/origin/||')
  if [ -z "$default_branch" ]; then
    if git -C "$main_worktree" show-ref --verify --quiet refs/heads/main 2>/dev/null; then
      default_branch="main"
    elif git -C "$main_worktree" show-ref --verify --quiet refs/heads/master 2>/dev/null; then
      default_branch="master"
    fi
  fi

  if [ -n "$default_branch" ]; then
    local current_branch
    current_branch=$(git -C "$main_worktree" rev-parse --abbrev-ref HEAD 2>/dev/null || true)
    if [ "$current_branch" != "$default_branch" ]; then
      if git -C "$main_worktree" diff --quiet 2>/dev/null && git -C "$main_worktree" diff --cached --quiet 2>/dev/null; then
        if ! git -C "$main_worktree" checkout "$default_branch" >/dev/null 2>&1; then
          tmux display-message -d 5000 "dispatch: warn: failed to checkout $default_branch" 2>/dev/null || true
        fi
      else
        tmux display-message -d 5000 "dispatch: dirty working tree, staying on $current_branch" 2>/dev/null || true
      fi
    fi
  fi

  echo "$main_worktree"
}

# --- サブコマンド: list-repos ---
cmd_list_repos() {
  ghq list 2>/dev/null || die "ghq が利用できません"
}

# --- サブコマンド: launch ---
cmd_launch() {
  local repo="" prompt="" prompt_file_arg="" session_name="" window_name="" branch_name="" no_worktree=false no_prompt=false launcher="claude"
  # agmsg 連携: 起動 agent を team に auto-join して親 session から到達可能にする。
  # agmsg は必須（PATH に無ければ起動前に落とす）。team/name は導出するが --agmsg-team/--agmsg-name で上書き可。
  local agmsg_team="" agmsg_name=""
  # --session が明示的に渡されたかを追跡する。未指定なら衝突時に suffix を付けて
  # 必ず新規 session を作成する（SKILL.md の "default = 新規作成" を守るため）。
  local session_explicit=false

  # 引数パース
  while [ $# -gt 0 ]; do
    case "$1" in
      --launcher)
        launcher="$2"
        shift 2
        ;;
      --session)
        session_name="$2"
        session_explicit=true
        shift 2
        ;;
      --window)
        window_name="$2"
        shift 2
        ;;
      --branch)
        branch_name="$2"
        shift 2
        ;;
      --no-worktree)
        no_worktree=true
        shift
        ;;
      --no-prompt)
        no_prompt=true
        shift
        ;;
      --prompt-file)
        prompt_file_arg="$2"
        shift 2
        ;;
      --agmsg-team)
        agmsg_team="$2"
        shift 2
        ;;
      --agmsg-name)
        agmsg_name="$2"
        shift 2
        ;;
      --*)
        # 未知の --* は silent に捨てず明示的に落とす。撤去した --no-agmsg を旧利用者が渡しても
        # 黙って無視されず（required の意図に反して join が走るのを防ぐ）、タイポも早期に検出できる。
        die "未知のオプションです: $1"
        ;;
      *)
        if [ -z "$repo" ]; then
          repo="$1"
        elif [ -z "$prompt" ]; then
          prompt="$1"
        fi
        shift
        ;;
    esac
  done

  # --prompt-file があればそちらを優先して読み込む
  # 失敗時にプロンプト消失を防ぐため、削除は launcher 起動成功後まで遅延する
  if [ -n "$prompt_file_arg" ]; then
    if [ ! -f "$prompt_file_arg" ]; then
      die "prompt-file が見つかりません: $prompt_file_arg"
    fi
    prompt=$(cat "$prompt_file_arg")
  fi

  # バリデーション
  if [ -z "$repo" ]; then
    die "repo が指定されていません"
  fi
  if [ "$no_prompt" = false ] && [ -z "$prompt" ]; then
    die "prompt が指定されていません"
  fi
  case "$launcher" in
    claude|codex) ;;
    *) die "--launcher は claude または codex のみ対応です: $launcher" ;;
  esac

  # agmsg は必須。起動 agent の auto-join（親 session からの到達性）が dispatch の前提のため、
  # PATH に無ければ起動前に落とす。
  command -v agmsg >/dev/null 2>&1 || die "agmsg が利用できません（PATH に必要 — auto-join に使用）"

  # repo パス解決
  local repo_path
  repo_path=$(resolve_repo "$repo") || die "リポジトリが見つかりません: $repo"

  # no-worktree 設定ファイルによる自動判定
  # ~/.config/dispatch/no-worktree-repos に "owner/repo" 形式で列挙されたリポジトリは worktree を作成しない
  local no_worktree_config="$HOME/.config/dispatch/no-worktree-repos"
  local config_match=false
  if [ -f "$no_worktree_config" ]; then
    local repo_short
    repo_short=$(echo "$repo_path" | sed "s|$GHQ_ROOT/||")
    if grep -qxF "$repo_short" "$no_worktree_config" 2>/dev/null; then
      config_match=true
      if [ "$no_worktree" = false ]; then
        no_worktree=true
      fi
    fi
  fi

  # デフォルト window 名
  if [ -z "$window_name" ]; then
    window_name="$(basename "$repo_path")"
  fi

  # worktree 作成（--no-worktree でない場合）
  local work_dir="$repo_path"
  if [ "$no_worktree" = false ]; then
    if [ -z "$branch_name" ]; then
      die "--branch が指定されていません（--no-worktree でない場合は必須）"
    fi
    notify "creating worktree: $branch_name"
    work_dir=$(create_worktree "$repo_path" "$branch_name")
  elif [ "$config_match" = true ]; then
    # no-worktree-repos 設定によるトリガー: メインworktreeに移動しデフォルトブランチで開く
    work_dir=$(checkout_default_branch "$repo_path")
  fi

  # work_dir / prompt_file は pane へ送るコマンドに single quote で埋め込む。`'` を含むパスは
  # quote が壊れて任意 shell 断片として解釈されうるため拒否する（prompt_file は work_dir 配下に作る）。
  case "$work_dir" in *\'*) die "work_dir に single quote を含むパスは未対応です: $work_dir" ;; esac

  # agmsg auto-join: 起動 agent の identity (name, team) を config に登録し、親 session から
  # `agmsg send <name> --team <team>` で到達可能にする。join は config への登録なので、
  # dispatch.sh（bash）が代行して実行できる（fish pane への send-keys 制約を回避）。
  # 起動した agent は work_dir に居れば (type, project) 自動解決で同じ identity に解決される。
  #
  # AGMSG_HOME の伝播: join は dispatch.sh の AGMSG_HOME に書く。起動した agent も同じ DB を
  # 見るよう、AGMSG_HOME が設定されていれば launcher コマンドへ env-prefix で伝播する
  # （`env VAR=val cmd` は bash/fish 双方で動く。pane の shell builtin 代入 `VAR=val cmd` は fish 非対応）。
  local agmsg_joined="" agmsg_type="" agmsg_env_prefix=""
  if [ -n "${AGMSG_HOME:-}" ]; then
    case "$AGMSG_HOME" in *\'*) die "AGMSG_HOME に single quote を含むパスは未対応です: $AGMSG_HOME" ;; esac
    agmsg_env_prefix="env AGMSG_HOME='$AGMSG_HOME' "
  fi
  # team: --agmsg-team > 親の AGMSG_TEAM > repo basename
  [ -z "$agmsg_team" ] && agmsg_team="${AGMSG_TEAM:-$(basename "$repo_path")}"
  # name: --agmsg-name > branch名(/→-) > window名
  if [ -z "$agmsg_name" ]; then
    if [ -n "$branch_name" ]; then
      agmsg_name="$(echo "$branch_name" | tr '/' '-')"
    else
      agmsg_name="$window_name"
    fi
  fi
  agmsg_type="claude-code"
  [ "$launcher" = codex ] && agmsg_type="codex"
  if agmsg join "$agmsg_team" "$agmsg_name" --type "$agmsg_type" --project "$work_dir" >/dev/null 2>&1; then
    agmsg_joined="$agmsg_name@$agmsg_team"
  else
    die "agmsg join に失敗しました ($agmsg_name@$agmsg_team)"
  fi

  # prompt を一時ファイルに書き出し（worktree 側に配置）
  local prompt_file=""
  if [ "$no_prompt" = false ]; then
    local output_dir="$work_dir/.outputs/claude"
    mkdir -p "$output_dir"
    prompt_file=$(mktemp "$output_dir/dispatch-prompt-XXXXXX")
    printf '%s' "$prompt" > "$prompt_file"
  fi

  # tmux session/window 作成
  # session 名が未指定の場合、worktree のディレクトリ名をセッション名にする
  if [ -z "$session_name" ]; then
    session_name="$(basename "$work_dir")"
  fi

  # --session 未指定時に既存 session 名と衝突したら suffix を付けて必ず新規作成する
  if [ "$session_explicit" = false ] && tmux has-session -t "=$session_name" 2>/dev/null; then
    local base="$session_name"
    session_name="${base}-$(date +%H%M%S)"
    local i=2
    while tmux has-session -t "=$session_name" 2>/dev/null; do
      session_name="${base}-$(date +%H%M%S)-${i}"
      i=$((i + 1))
      if [ "$i" -gt 100 ]; then
        die "session 名の衝突を解決できません: $base"
      fi
    done
  fi

  # 作成直後の window の active pane id を tmux 自身に出力させて固定する。
  # display-message で再取得すると、同名 window が複数あった場合に最初のものを
  # 拾ってしまい send-keys が誤った pane に飛ぶ（特に sidebar plugin がある時）。
  # -P -F '#{pane_id}' で new-session/new-window が作成した pane を直接取得する。
  local target_pane_id
  if ! tmux has-session -t "=$session_name" 2>/dev/null; then
    # session が存在しない → 新規作成（最初の window として window_name を使う）
    # 現在のターミナルサイズを渡す（デタッチ作成後の比例拡大による sidebar 幅崩れを防止）
    local cur_width cur_height
    cur_width=$(tmux display-message -p '#{window_width}' 2>/dev/null || echo 200)
    cur_height=$(tmux display-message -p '#{window_height}' 2>/dev/null || echo 50)
    target_pane_id=$(tmux new-session -d -P -F '#{pane_id}' \
      -s "$session_name" -n "$window_name" -c "$work_dir" \
      -x "$cur_width" -y "$cur_height") \
      || die "tmux session の作成に失敗しました: $session_name"
  else
    # session が存在する → window 追加
    # trailing colon を付けないと tmux が target を window index と解釈して
    # base-index 衝突（"index 1 in use"）で失敗するため "=$session_name:" を使う
    target_pane_id=$(tmux new-window -P -F '#{pane_id}' \
      -t "=$session_name:" -n "$window_name" -c "$work_dir") \
      || die "tmux window の作成に失敗しました"
  fi

  # claude がターミナルタイトルを変更して window 名を上書きするのを防止
  # pane_id 経由で対象の window を一意に指定する（同名 window がある場合の保険）
  tmux set-option -wt "$target_pane_id" allow-rename off

  # pane タイトル設定
  tmux select-pane -t "$target_pane_id" -T "$window_name"

  # window index を取得
  local window_index
  window_index=$(tmux display-message -t "$target_pane_id" -p '#{window_index}' 2>/dev/null || echo "unknown")
  local pane_id="$target_pane_id"

  # codex は起動時に OSC 11 で背景色 query を送るが、tmux 3.4+ は attached client
  # にしか query を passthrough しない。dispatch は detached session で起動する
  # ため、attach 前に codex を起こすと入力エリアの背景色が描画されないモードで
  # 初期化されてしまう。attached client が来るまで待ってから send-keys する。
  # 関連: openai/codex#4744
  if [ "$launcher" = codex ]; then
    local wait_iter=0
    while [ "$(tmux list-clients -t "=$session_name" 2>/dev/null | wc -l | tr -d ' ')" -eq 0 ]; do
      if [ "$wait_iter" -ge 600 ]; then
        break
      fi
      sleep 0.5
      wait_iter=$((wait_iter + 1))
    done
  fi

  # pane が起動コマンドを受理できる状態（プロンプトが Enter を受理し実行できる）になるまで待つ。
  # 新規 window 直後は pane_current_command がシェル名でも fish 初期化が未完で send-keys の Enter が
  # 飲まれるレースがある（agmsg-go issues/0012）。固定 sleep の代わりにマーカー往復で機構的に確認する。
  local launch_ready=yes
  wait_pane_ready "$target_pane_id" 30 || launch_ready=no
  # マーカー実行後は入力行が空のはずだが、保険でもう一度クリアしてから本命を送る。
  tmux send-keys -t "$target_pane_id" C-u 2>/dev/null || true

  if [ "$no_prompt" = true ]; then
    # launcher 名のみ送る（claude も codex も同様）
    tmux send-keys -t "$target_pane_id" "cd '$work_dir'; ${agmsg_env_prefix}$launcher" Enter
  else
    case "$launcher" in
      claude)
        tmux send-keys -t "$target_pane_id" "cd '$work_dir'; ${agmsg_env_prefix}claude < '$prompt_file'" Enter
        ;;
      codex)
        # codex の TUI は stdin redirect 不可のため、位置引数で prompt を渡す
        # 改行を含む prompt は $(/bin/cat ...) でそのまま読ませる（shell quote で injection 対策）
        # 絶対パス指定で fish の abbreviation/alias（例: cat → nyan）を確実に回避する
        # 注: `$(...)` を使うため pane の shell は bash / zsh / fish 3.4+ のいずれかであること
        # （fish は 3.4 以降 `$()` を posix 互換で解釈する）。claude 経路は stdin redirect で全 shell 可。
        tmux send-keys -t "$target_pane_id" "cd '$work_dir'; ${agmsg_env_prefix}codex -C '$work_dir' \"\$(/bin/cat '$prompt_file')\"" Enter
        ;;
    esac
  fi

  # launcher 起動まで成功したので、ここで初めて元の prompt-file を削除する
  if [ -n "$prompt_file_arg" ] && [ -f "$prompt_file_arg" ]; then
    rm -f "$prompt_file_arg"
  fi

  # 構造化出力
  echo "STATUS: LAUNCHED"
  echo "SESSION: $session_name"
  echo "WINDOW: $window_index"
  echo "PANE_ID: $pane_id"
  echo "REPO: $repo_path"
  echo "WORK_DIR: $work_dir"
  echo "LAUNCH_READY: $launch_ready"
  if [ "$launch_ready" = no ]; then
    echo "WARNING: pane $pane_id が起動コマンドを受理できる状態を確認できないまま送出しました（fish 初期化の遅延等）。launcher が起動していない可能性があります。pane を確認してください"
  fi
  if [ -n "$agmsg_joined" ]; then
    echo "AGMSG: $agmsg_joined (type=$agmsg_type, project=$work_dir)"
  fi

  tmux display-message -d 5000 "dispatch: launched [$session_name]" 2>/dev/null || true
}

# --- メイン ---
subcommand="${1:-}"
shift || true

case "$subcommand" in
  launch)
    cmd_launch "$@"
    ;;
  list-repos)
    cmd_list_repos
    ;;
  *)
    die "Unknown subcommand: $subcommand. Usage: dispatch.sh {launch|list-repos}"
    ;;
esac
