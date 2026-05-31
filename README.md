# agmsg-go

> **共有 SQLite ファイル 1 個を通信路にした、CLI AI エージェント間メッセージング。デーモンなし・ネットワークなし。**

`agmsg-go` は、bash 製ツール [**fujibee/agmsg**](https://github.com/fujibee/agmsg) にインスパイアされた **Go fork** です。

Claude Code / Codex / Gemini CLI / Antigravity などの CLI AI エージェント同士が、共有 SQLite ファイルを介してメッセージをやり取りします。中央プロセス（broker / daemon）もネットワークも持たず、各エージェントが同じ DB ファイルに直接読み書きすることで通信が成立します。設計思想は **"No daemon, no network, no complexity"**。

## スコープ: IPC インフラだけを提供する (mechanism, not policy)

agmsg-go が提供するのは **エージェント間 IPC のインフラ（送る・受け取る・購読する・宛先を解決する）** だけです。その IPC を**どう使うか**——レビュー+修正ループ、複数 issue の orchestration、役割割り当て、合意形成など——は **agmsg-go には実装せず、利用側が `send` / `inbox` / `watch` を組み合わせて自由に構築します**。

これは「最小限のプリミティブに留める」という意図的な制約です。「複数の利用側が別々の使い方をしうる機能」はツール側に入れません。詳細は [design.md §2.1](./design.md) を参照してください。

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

## インストール

### go install（推奨）

ソースからビルドする。go ツールチェーンが生成するバイナリには macOS の quarantine 属性が付かないため、**Gatekeeper 警告なし**で実行できる。

```sh
go install github.com/ishii1648/agmsg-go/cmd/agmsg@latest
```

`make install` でも同じくソースビルドで `~/.local/bin/agmsg` に配置できる（`PREFIX` で変更可）。

### リリースバイナリをダウンロード

[Releases](https://github.com/ishii1648/agmsg-go/releases) から OS / arch 別の tar.gz を取得する。リリースバイナリは**未署名**のため、macOS では初回実行時に Gatekeeper 警告（「開発元を検証できません」）が出る。次で解除する:

```sh
xattr -d com.apple.quarantine ./agmsg   # quarantine 属性を外す
```

または Finder で右クリック →「開く」。コード署名 / notarization は導入していない（CLI には過剰なため）。警告を避けたい場合は上記 `go install` / `make install` を使う。

## サブコマンド（暫定）

すべて単一バイナリ `agmsg` のサブコマンドです。ホストフック（SessionStart / Stop）から呼ぶエントリポイントも同じバイナリに含まれます。これらは IPC プリミティブであり、組み合わせ方（オーケストレーション）は利用側が決めます。

**Tier 1: 最小コア**（これだけで「送る・受け取る・購読する」が成立）

| コマンド | 役割 |
|---|---|
| `agmsg send <to> <body>` | メッセージ送信 |
| `agmsg inbox` | 未読メッセージの取得 |
| `agmsg watch` | `id > watermark` の購読ストリーム（フックからも起動） |
| `agmsg join <team>` / `agmsg leave <team>` | チーム参加 / 離脱（宛先解決の前提） |
| `agmsg whoami` | 自アイデンティティの表示 |

**Tier 2: 補助** — `agmsg history [N]` / `agmsg check-inbox` / `agmsg delivery set <mode>`（`monitor`/`turn`/`both`/`off`）/ `agmsg team` / `agmsg identities` / `agmsg config`

**Tier 3: 任意（初期実装では後回し可）** — `agmsg reset` / `agmsg rename` / `agmsg rename-team` / `agmsg actas <name>` / `agmsg drop <name>`

詳細なコマンド対応表と Tier 分類は [design.md §10](./design.md) を参照してください。

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
