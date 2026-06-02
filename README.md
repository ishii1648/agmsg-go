# agmsg-go

> 共有 SQLite ファイル 1 個を通信路にした CLI AI エージェント間 IPC と、その上で動く協調ワークフロー（skills）を、1 つの binary から配る。デーモンなし・ネットワークなし。

Claude Code / Codex / Gemini CLI などの CLI AI エージェントが、中央プロセス（broker / daemon）もネットワークも持たず、同じ SQLite ファイルへ直接読み書きして通信します。設計思想は **"No daemon, no network, no complexity"**。bash 製の [**fujibee/agmsg**](https://github.com/fujibee/agmsg) のフォークで、その設計とアイデアを Go で再実装したものです。

**IPC コア（`agmsg` binary）は mechanism, not policy** を厳守し「IPC をどう使うか」を作り込みません。その上の協調ワークフローは、分離された opt-in な **skills 層**として同梱します。設計の正本は [design.md](./design.md)（スコープ境界は [§2.1](./design.md)、skills 層は [§12](./design.md)）。

```sh
go install github.com/ishii1648/agmsg-go/cmd/agmsg@latest   # IPC コアを入れる
agmsg skills install                                        # 協調ワークフローを展開する
```

この 2 行で、エージェント間 IPC とその上の協調ワークフロー（`dispatch` / `review-loop`）一式が揃います。導入の全手順は [setup.md](./setup.md)。

## サブコマンド

すべて単一バイナリ `agmsg` のサブコマンドです。これらは IPC プリミティブであり、組み合わせ方（オーケストレーション）は利用側が決めます。

**Tier 1: 最小コア**（これだけで「送る・受け取る・購読する」が成立）

| コマンド | 役割 |
|---|---|
| `agmsg send <to> <body>` | メッセージ送信 |
| `agmsg inbox` | 未読メッセージの取得 |
| `agmsg watch` | `id > watermark` の購読ストリーム（フックからも起動） |
| `agmsg join <team>` / `agmsg leave <team>` | チーム参加 / 離脱（宛先解決の前提） |
| `agmsg whoami` | 自アイデンティティの表示 |

Tier 2/3・配信モード（`monitor` / `turn` / `both` / `off`）・完全な対応表は [design.md §7](./design.md) / [§10](./design.md) を参照。運用補助として `agmsg version` があります。更新は `go install github.com/ishii1648/agmsg-go/cmd/agmsg@latest` の再実行で行います（自己更新コマンドは持ちません）。

## skills (dispatch / review-loop)

IPC プリミティブを使う具体的なワークフローを、binary とは分離された **opt-in な policy 層**として同梱しています。`agmsg skills install` で `~/.claude/skills` 等に展開して使います（詳細は [design.md §12](./design.md)）。

| skill | 役割 |
|---|---|
| **dispatch** | 別リポジトリで CLI agent（claude / codex）を git worktree + tmux window で起動し、`agmsg join` で team に auto-join して親 session から `send` で到達可能にする。 |
| **review-loop** | 実装済みコードを「逆エージェント」に反復レビューさせ収束させる（実装役は元 session に固定）。verdict 通知・完了検知を `agmsg send` / `agmsg inbox` に載せる。 |

いずれも **tmux で対話 agent を起こし、agent 間の連絡を agmsg に載せる**構成です。前提（`agmsg` が PATH にあること・tmux セッション内・git リポジトリ内）と手順は各 `SKILL.md` を参照してください。

## ドキュメント

- [📊 アーキテクチャ インフォグラフィック](https://ishii1648.github.io/agmsg-go/) — エージェント間 IPC の全体像をビジュアルで解説（GitHub Pages）
- [setup.md](./setup.md) — セットアップ手順（binary の導入・skills の展開・Gatekeeper 解除・動作確認）
- [design.md](./design.md) — 設計判断とその根拠（アーキテクチャ / データモデル / 受信検知 / ライフサイクル管理 / トレードオフ）

## ライセンス

MIT License。[LICENSE](./LICENSE) を参照してください。原典の設計とアイデアに感謝します: [fujibee/agmsg](https://github.com/fujibee/agmsg)。
