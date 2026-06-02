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

### PR 作成

**PR 作成時は CI チェックと review-loop を必ず実行する。** PR を作成する前後で以下を必ず行う：

1. **CI チェック** — `go test ./...` / `go vet ./...` をローカルで通し、PR push 後は `gh pr checks` で GitHub Actions の結果も確認する（赤があれば修正してから完了とする）。
2. **review-loop** — `review-loop` skill を実行し、指摘がなくなる（APPROVED）まで修正ループを回す。

**PR description の冒頭で必ず関連 issue にリンクする。** `.github/workflows/intent.yml`（`Intent` チェック）が PR description 内に `issues/NNNN-<cat>-<slug>.md`（close を伴う場合は `issues/closed/NNNN-...`）のリンク、または issue を伴わない軽微な変更を示す `(N/A — chore)` の記載を必須化しており、**どちらも無いと PR の CI が落ちる**（コミットメッセージではなく PR 本文を見る点に注意）。フォームは `.github/pull_request_template.md` に従い、「なぜ」「方針」「却下案」は issue / commit body 側に置いて description は薄く保つ。詳細は AGENTS.md「issues について」。

### ブランチ命名

`feat/`, `fix/`, `docs/`, `chore/` + kebab-case（例: `feat/store-placeholder-insert`）

### issues / バグ・課題管理

`issues/` 配下で Markdown ライフサイクル管理する。命名規則・SEQUENCE 運用・ディレクトリ構成・frontmatter・close/reopen/pending の手順は **`AGENTS.md` の「issues について」セクションを正とする**。CLAUDE.md と AGENTS.md の二重管理を避けるため、ルールの本体は AGENTS.md 側のみに置く。

### テスト

実装着手後は `go test ./...` / `go vet ./...` を基本とする（現状は設計フェーズでコードは未着手）。

## ステータス

🚧 設計フェーズ。設計の正本は `design.md`、次の作業は `issues/` を参照。
