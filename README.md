# agmsg-go

> **共有 SQLite ファイル 1 個を通信路にした CLI AI エージェント間 IPC と、その上で動く協調ワークフロー（skills）を、1 つの binary から配る。デーモンなし・ネットワークなし。**

`agmsg-go` は、bash 製ツール [**fujibee/agmsg**](https://github.com/fujibee/agmsg) を起点にした **Go 実装**です。IPC コア（`agmsg` binary）に加え、その上で動く協調ワークフローを **skills 層**として同梱します。

Claude Code / Codex / Gemini CLI / Antigravity などの CLI AI エージェント同士が、共有 SQLite ファイルを介してメッセージをやり取りします。中央プロセス（broker / daemon）もネットワークも持たず、各エージェントが同じ DB ファイルに直接読み書きすることで通信が成立します。設計思想は **"No daemon, no network, no complexity"**。

```sh
go install github.com/ishii1648/agmsg-go/cmd/agmsg@latest   # IPC コアを入れる
agmsg skills install                                        # 協調ワークフローを展開する
```

この 2 行で、エージェント間 IPC とその上の協調ワークフロー（`dispatch` / `review-loop`）一式が揃います。

## 2 つの層: IPC インフラ (mechanism) と skills (policy)

agmsg-go は **2 つの層**を同じリポジトリから配布します。両者は明確に分離されています。

- **インフラ層 — `agmsg` binary**: エージェント間 IPC のプリミティブ（送る・受け取る・購読する・宛先を解決する）。この層は **mechanism, not policy** を厳守し、「IPC をどう使うか」を binary には作り込みません。「複数の利用側が別々の使い方をしうる機能」はツール側に入れない、という意図的な制約です。
- **skills 層 — `skills/` + `agmsg skills install`**: その IPC を使う具体的なワークフロー（`dispatch` / `review-loop`）。これは policy ですが、binary を `send` / `inbox` / `watch` / `join` 経由でのみ呼ぶ「消費者」として同梱されます。分離可能で opt-in です。

当初は IPC インフラだけを提供する方針でしたが、協調ワークフロー全体を 1 箇所から配れる **delivery 容易化**のため skills 層の同梱へ方針変更しました。mechanism-not-policy は放棄しておらず、**binary コア層の原則として温存**しています。詳細は [design.md §2.1](./design.md) / [§13](./design.md) を参照してください。

## ステータス

🚧 開発中です。**Tier 1 の IPC コア（`send` / `inbox` / `watch` / `join` / `leave` / `whoami`）は実装済み**で、skills 層（`dispatch` / `review-loop`）の同梱と `agmsg skills install` を追加しました。設計の詳細は [design.md](./design.md)、次の作業は [issues/](./issues) を参照してください。

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

### skills を展開する

binary には skills 層（`dispatch` / `review-loop`）が埋め込まれており、次のコマンドで展開します（既定の展開先は `~/.claude/skills`）。

```sh
agmsg skills install            # ~/.claude/skills へ展開
agmsg skills install --dest DIR # 展開先を指定
agmsg skills install --force    # 既存ファイルを上書き（既定は保持）
agmsg skills list               # 同梱 skills の一覧を表示
```

`go install` と合わせれば、協調ワークフロー一式が 2 行で揃います。手順全体は [setup.md](./setup.md)、skills の概要は [§skills](#skills-dispatch--review-loop) を参照してください。

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

## skills (dispatch / review-loop)

IPC プリミティブを使う具体的なワークフローを **skills 層**として同梱しています（binary とは分離された opt-in な policy 層。詳細は [design.md §13](./design.md)）。`agmsg skills install` で `~/.claude/skills` 等に展開して使います。

| skill | 役割 | agmsg の使い方 |
|---|---|---|
| **dispatch** | 別リポジトリで CLI agent（claude / codex）を git worktree + tmux window で起動する。 | 起動した agent を `agmsg join` で team に auto-join し、親 session から `send` で到達可能にする。 |
| **review-loop** | 実装済みコードを「逆エージェント」に反復レビューさせ収束させる（実装役は元 session に固定）。 | reviewer → implementer の verdict 通知・完了検知を `agmsg send` / `agmsg inbox` に載せる。 |

いずれも **tmux で対話 agent のプロセスを起こし、agent 間の連絡を agmsg に載せる**構成です（tmux は launcher として残り、agmsg が置き換えるのは連絡だけ）。前提として `agmsg` が PATH にあること、tmux セッション内・git リポジトリ内であることが必要です。各 skill の `SKILL.md` に手順を記載しています。

## ドキュメント

- [setup.md](./setup.md) — セットアップ手順（binary の導入・skills の展開・動作確認）
- [design.md](./design.md) — 設計判断とその根拠（アーキテクチャ / データモデル / 受信検知 / ライフサイクル管理 / トレードオフ）

## ライセンス

MIT License。[LICENSE](./LICENSE) を参照してください。

## 謝辞

オリジナルの設計とアイデアに感謝します: [fujibee/agmsg](https://github.com/fujibee/agmsg)。
