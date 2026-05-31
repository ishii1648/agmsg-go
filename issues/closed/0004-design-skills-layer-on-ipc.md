---
decision_type: spec
tags: [skills, delivery, scope, dispatch, review-loop]
closed_at: 2026-05-31
---

# IPC プリミティブの上に skills 層（dispatch / review-loop）を同梱する

Created: 2026-05-31

## 概要

agmsg-go の提供範囲を「エージェント間 IPC のプリミティブだけ」から拡張し、その上で動く **skills 層（dispatch / review-loop）を同じリポジトリから同梱配布**する。ただし **インフラ層（IPC binary = mechanism）と skills 層（policy）を明確に分離**する。

skills は [ishii1648/dotfiles](https://github.com/ishii1648/dotfiles) の `configs/claude/skills/{dispatch,review-loop}` を取り込み、**agent 間通信の仕組みを agmsg 自身の IPC（`send`/`inbox`/`watch`/`join`）に置き換える**。

影響する path: `skills/{dispatch,review-loop}/`、`assets.go`（ルート, `go:embed`）、`internal/skills/`、`internal/cli/skills.go`、`internal/cli/cli.go`（`skills` サブコマンド配線・`--force` bool フラグ）、`design.md`（§1 / §2 / §2.1 / 新 §13）、`README.md`。

## 根拠

design.md §2.1 は当初「mechanism, not policy」を「本プロジェクト最大の設計判断」と位置づけ、§2 非目標で「レビュー+修正ループ」「orchestration」を名指しで対象外にしていた。今回この判断自体を変更する。

- **狙いは delivery の容易化**。IPC binary だけでなく、その上で動く skills も 1 箇所から配れば、エージェント協調ワークフロー全体を `go install ...@latest && agmsg skills install` の 2 行で配布できる。原典 agmsg 自身も `~/.agents/skills/` に skills として配布される前例がある。
- **mechanism-not-policy は binary コア層の原則として温存**する。§2.1 の binary 純度の論理（SQL 安全性・最小プリミティブ）は引き続き正しい。skills 層は binary を `send`/`inbox`/`watch`/`join` 経由でのみ呼ぶ「消費者」であり、binary 側に skills 固有ロジックを漏らさない（層分離の不変条件）。
- dispatch / review-loop は元々 tmux + file-marker で agent 間連絡していた。これを agmsg に載せ替えることで「IPC の上に乗る skills」という構図を技術的にも成立させ、agmsg 自身のドッグフーディングにもなる。

## 問題 / 検討した論点

- **go:embed の制約**: 親ディレクトリを辿れないため、トップレベル `skills/` を埋め込むにはモジュールルートに埋め込み用 `.go`（`package agmsg`）を置く必要がある。
- **tmux は残る**: 「対話 agent の起動（プロセス起こし）」は agmsg では代替できない。agmsg が置き換えるのは agent 間の連絡（verdict 通知・到達性）のみ。
- **fish pane への send-keys 制約**: `VAR=val cmd` や `$(...)` が使えないため、review-loop の codex verdict 送信は `notify-verdict` という bash サブコマンドに委譲。dispatch の auto-join は dispatch.sh（bash）が `agmsg join` を代行して回避。

## 対応方針

- **配布**: skills を `go:embed`（ルート `assets.go` の `SkillsFS`）し、`agmsg skills install [--dest ~/.claude/skills] [--force]` で展開する。`internal/skills.Install` はファイル書き出しのみで policy を解釈しない。
- **review-loop**: verdict 受け渡し・完了検知の file-marker ポーリングを agmsg メッセージに置換。per-session の ephemeral team（= session-id）、固定 identity（implementer / reviewer）を明示フラグで使い `join` 不要。codex は exec 後に wrapper が `notify-verdict` で送信、claude はプロンプト指示で自ら送信。`wait-review` は `agmsg inbox` ポーリングで受信。
- **dispatch**: 起動 agent を `agmsg join <team> <name> --type <type> --project <work_dir>` で team に auto-join（既定 ON、agmsg 不在時は graceful skip）。team は親 `AGMSG_TEAM` → repo basename、name はブランチ名から導出。`--agmsg-team`/`--agmsg-name`/`--no-agmsg` で制御。
- **脱・個人化**: ADR 参照 → issues/ 参照、ハードコードパス・fish/ghq 前提の一般化。
- **却下案**: skills を別リポジトリで配る案は、delivery を 2 箇所に分散させ「1 コマンドで揃う」利点を失うため却下。
