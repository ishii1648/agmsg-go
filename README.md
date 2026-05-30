# agmsg-go

> **共有 SQLite ファイル 1 個を通信路にした、CLI AI エージェント間メッセージング。デーモンなし・ネットワークなし。**

`agmsg-go` は、bash 製ツール [**fujibee/agmsg**](https://github.com/fujibee/agmsg) にインスパイアされた **Go fork** です。

Claude Code / Codex / Gemini CLI / Antigravity などの CLI AI エージェント同士が、共有 SQLite ファイルを介してメッセージをやり取りします。中央プロセス（broker / daemon）もネットワークも持たず、各エージェントが同じ DB ファイルに直接読み書きすることで通信が成立します。設計思想は **"No daemon, no network, no complexity"**。

## ステータス

🚧 **設計フェーズ**です。実装はこれから着手します。設計の詳細は [design.md](./design.md) を参照してください。

## オリジナルとの違い

本 fork は、オリジナルの通信モデル・通信路・アイデンティティモデル・配信モードといった核を維持したまま、bash 実装の構造的弱点を Go で解消することを目的とします。

| 観点 | fujibee/agmsg (bash) | agmsg-go (Go) |
|---|---|---|
| SQL 安全性 | 手動 `sed "s/'/''/g"` エスケープ + 文字列連結（エスケープ漏れで破綻） | **prepared statement / placeholder バインド**で構造的に安全 |
| 外部依存 | `sqlite3` CLI に依存 | **`sqlite3` CLI 不要**（純 Go ドライバ [`modernc.org/sqlite`](https://gitlab.com/cznic/sqlite) を内蔵） |
| 配布 | 複数の `*.sh` スクリプト | **単一バイナリ**（CGO 不要でクロスコンパイル容易） |
| 受信検知 | 5 秒ポーリング（アイドル時も `sqlite3` を fork し続ける） | **fsnotify によるイベント駆動 + 長めのポーリング保険**（アイドル時は何もしない） |
| ライフサイクル管理 | 短命スクリプトで pid/世代を都度再計算 | 型付き構造体 + **単体テスト可能**な関数に分解 |
| OS 分岐 | `ps -o` / `stat -f` / `stat -c` 等を都度分岐 | 標準ライブラリ + ビルドタグで吸収 |

> 補足: fsnotify 採用の主眼は「遅延短縮」ではなく**アイドル効率**です。受信の数秒の遅延は LLM の推論時間に埋もれるため、本質的な利得は「何も来ない時に何もしない」ことにあります。詳細は [design.md §8](./design.md) を参照。

## サブコマンド（暫定）

すべて単一バイナリ `agmsg` のサブコマンドです。ホストフック（SessionStart / Stop）から呼ぶエントリポイントも同じバイナリに含まれます。

| コマンド | 役割 |
|---|---|
| `agmsg send <to> <body>` | メッセージ送信 |
| `agmsg inbox` | 未読メッセージの取得 |
| `agmsg history [N]` | 履歴（最新 N 件） |
| `agmsg join <team>` / `agmsg leave <team>` | チーム参加 / 離脱 |
| `agmsg team` | チーム名簿の表示・操作 |
| `agmsg whoami` / `agmsg identities` | アイデンティティ / 登録状態の表示 |
| `agmsg delivery set <mode>` | 配信モード設定（`monitor` / `turn` / `both` / `off`） |
| `agmsg watch` | monitor モードの長命ストリーム（フックから起動） |
| `agmsg check-inbox` | turn モードのターン間チェック（フックから起動） |
| `agmsg config` | ユーザ設定の読み書き |
| `agmsg actas <name>` / `agmsg drop <name>` | 役割（name）の多重追加 / 除去 |
| `agmsg rename <new>` / `agmsg rename-team <new>` | 名前 / チーム名の変更 |
| `agmsg reset` | DB / 状態のリセット |

詳細なコマンド対応表は [design.md §10](./design.md) を参照してください。

## 配信モード

受信メッセージをエージェントのコンテキストにどう割り込ませるかを選べます。

| mode | 機構 | 対象 |
|---|---|---|
| `monitor` | SessionStart フック → ホストの Monitor ツール → `agmsg watch` がストリーム | Claude Code |
| `turn` | Stop フック → ターン間に `agmsg check-inbox` | Monitor ツールが無い Codex 等 |
| `both` | monitor 主 + turn 保険 | 取りこぼし防止 |
| `off` | 自動配信なし（手動 `agmsg inbox`） | 手動派 |

## ドキュメント

- [design.md](./design.md) — 設計判断とその根拠（アーキテクチャ / データモデル / 受信検知 / ライフサイクル管理 / トレードオフ）

## ライセンス

MIT License。[LICENSE](./LICENSE) を参照してください。

## 謝辞

オリジナルの設計とアイデアに感謝します: [fujibee/agmsg](https://github.com/fujibee/agmsg)。
