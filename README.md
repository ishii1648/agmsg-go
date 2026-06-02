# agmsg-go

> 共有 SQLite ファイル 1 個を通信路にした、CLI AI エージェント間の IPC。デーモンなし・ネットワークなし。

Claude Code / Codex / Gemini CLI などの CLI AI エージェントが、中央プロセスもネットワークも持たず、同じ SQLite ファイルへ直接読み書きして通信します。設計思想は **"No daemon, no network, no complexity"**。bash 製の [fujibee/agmsg](https://github.com/fujibee/agmsg) のフォークで、設計を Go で再実装したものです。

IPC コア（`agmsg` binary）は **mechanism, not policy** を貫き、「どう使うか」は作り込みません。その上の協調ワークフローは、分離した opt-in の **skills 層**として同梱します。

```sh
go install github.com/ishii1648/agmsg-go/cmd/agmsg@latest   # IPC コア
agmsg skills install                                        # 協調ワークフロー（skills）
```

導入の全手順は [setup.md](./setup.md) を参照。

## サブコマンド

単一バイナリ `agmsg` のサブコマンドです。これらは IPC プリミティブで、組み合わせ方は利用側が決めます。

| コマンド | 役割 |
|---|---|
| `agmsg send <to> <body>` | メッセージ送信 |
| `agmsg inbox` | 未読メッセージの取得 |
| `agmsg watch` | `id > watermark` の購読ストリーム |
| `agmsg join <team>` / `leave <team>` | チーム参加 / 離脱（宛先解決の前提） |
| `agmsg whoami` | 自アイデンティティの表示 |

Tier 2/3・配信モード・完全な対応表は [design.md §7](./design.md) / [§10](./design.md) を参照。

## skills

IPC プリミティブを使うワークフローを、binary とは分離した opt-in の policy 層として同梱します（`agmsg skills install` で展開、詳細は [design.md §12](./design.md)）。

| skill | 役割 |
|---|---|
| **dispatch** | 別リポジトリで CLI agent を git worktree + tmux window で起動し、`agmsg join` で team に auto-join して親から到達可能にする。 |
| **review-loop** | 実装済みコードを「逆エージェント」に反復レビューさせ収束させる。verdict 通知を `agmsg send` / `inbox` に載せる。 |

いずれも tmux で対話 agent を起こし、agent 間の連絡を agmsg に載せます。前提と手順は各 `SKILL.md` を参照。

## ドキュメント

- [📊 アーキテクチャ インフォグラフィック](https://ishii1648.github.io/agmsg-go/) — IPC の全体像をビジュアルで解説
- [setup.md](./setup.md) — セットアップ手順
- [design.md](./design.md) — 設計判断とその根拠

## ライセンス

MIT License（[LICENSE](./LICENSE)）。原典に感謝します: [fujibee/agmsg](https://github.com/fujibee/agmsg)。
