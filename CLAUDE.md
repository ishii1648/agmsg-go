# agmsg-go

共有 SQLite ファイルを通信路にした、CLI AI エージェント間 IPC のインフラ。bash 製 [fujibee/agmsg](https://github.com/fujibee/agmsg) の Go fork。

**スコープは IPC プリミティブ（送る・受け取る・購読する・宛先解決）のみ**。レビュー+修正ループや複数 issue の orchestration といった「使い方」は実装せず、利用側に委ねる（mechanism, not policy）。根拠は [design.md §2.1](design.md)。

## ドキュメント構成

- `design.md` — 設計の正本（アーキテクチャ・データモデル・受信検知・ライフサイクル・トレードオフ）
- `README.md` — 概要・オリジナルとの違い・サブコマンド一覧（人間向け入口）
- `issues/` — タスク兼意思決定記録の primary store（`issues/closed/` が過去の意思決定の正本）

## 開発規約

### 意思決定の記録方針

意思決定の primary store は `issues/`。

- **複数コミット or 後続が参照しそうな決定** → `issues/<NNNN>-...` に書く（`decision_type` を埋める）
- 仕様・実装方針の変更 → `design.md` を更新（issue にも `decision_type: spec` / `design` で記録）
- 1 コミット内で完結する判断 → Contextual Commits のアクション行で記録（issue 化不要）
- chore / リファクタなど意思決定を伴わない変更 → アクション行不要

### コミット

Contextual Commits を使用。Conventional Commits プレフィックス + 構造化されたアクション行でコミットの意図を記録する。

### ブランチ命名

`feat/`, `fix/`, `docs/`, `chore/` + kebab-case（例: `feat/store-placeholder-insert`）

### issues / バグ・課題管理

`issues/` 配下で Markdown ライフサイクル管理する。命名規則・SEQUENCE 運用・ディレクトリ構成・frontmatter・close/reopen/pending の手順は **`AGENTS.md` の「issues について」セクションを正とする**。CLAUDE.md と AGENTS.md の二重管理を避けるため、ルールの本体は AGENTS.md 側のみに置く。

### テスト

実装着手後は `go test ./...` / `go vet ./...` を基本とする（現状は設計フェーズでコードは未着手）。

## ステータス

🚧 設計フェーズ。設計の正本は `design.md`、次の作業は `issues/` を参照。
