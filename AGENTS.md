# agmsg-go

共有 SQLite ファイルを通信路にした、CLI AI エージェント間 IPC のインフラ。bash 製 [fujibee/agmsg](https://github.com/fujibee/agmsg) の Go fork。提供範囲は IPC プリミティブのみで、その使い方（orchestration）は利用側に委ねる（mechanism, not policy）。

## ドキュメント構成

- `design.md` — 設計の正本（アーキテクチャ・データモデル・受信検知・ライフサイクル・トレードオフ）。spec / design を兼ねる。
- `README.md` — 概要・オリジナルとの違い・サブコマンド一覧（人間向け入口）
- `issues/` — タスク兼意思決定記録の primary store
- `issues/closed/` — 解決済み issue（過去の意思決定の正本）

新規の設計判断は ADR を作らない。`design.md` を更新し、Contextual Commits のアクション行で「なぜ」を記録する。後続が参照しそうな大きな方針転換は `issues/<NNNN>-<cat>-<slug>.md` に起こす（実装完了で `issues/closed/` へ move）。設計変更と実装はブランチを分けなくてよく、同一 PR で `design.md`・コードを一緒に更新してよい（merge 後に別途追従はしない）。

## 開発規約

### 意思決定の記録方針

意思決定の primary store は `issues/`。frontmatter の `decision_type` で層を構造化する。why の鎖は git blame → commit → PR → PR description の issue リンク → issue 本文で辿る。

- **複数コミット or 後続が参照しそうな決定** → `issues/<NNNN>-...` に書く（`decision_type` を埋め、影響する path は本文で言及する）
- 仕様・実装方針の変更 → `design.md` を更新（issue にも `decision_type: spec` / `design` で記録）
- 1 コミット内で完結する判断 → Contextual Commits のアクション行で記録（issue 化不要）
- chore / リファクタなど意思決定を伴わない変更 → アクション行不要

### コミット

Contextual Commits を使用。Conventional Commits プレフィックス + 構造化されたアクション行（intent / decision / rejected / constraint / learned）でコミットの意図を記録する。

### ブランチ命名

`feat/`, `fix/`, `docs/`, `chore/` + kebab-case（例: `feat/store-placeholder-insert`）

### issues について

`issues/` は **タスク兼意思決定記録** の primary store。バグ・課題・設計判断はここに Markdown としてライフサイクル管理する。GitHub Issues は使わない（ローカルで完結し、`git log` と同じ粒度で追跡できるため）。

各 issue は frontmatter で「どの層の決定か（`decision_type`: spec / design / implementation / process）」を構造化して持つ。「複数コミット or 後続が参照しそうな決定」は issue 化する。1 コミットで完結する判断は Contextual Commits の action 行で十分。影響する path（ディレクトリや主要ファイル）は本文で自然に言及しておく（将来の逆引き grep に拾われやすくするため）。

#### ディレクトリ構成

```
issues/
  SEQUENCE                     # 次に発番する番号（整数 1 行）
  NNNN-<category>-<slug>.md    # open な issue
  closed/
    NNNN-<category>-<slug>.md  # 解決済み
  pending/
    NNNN-<category>-<slug>.md  # 設計判断保留・外部依存待ち
```

`issues/closed/` と `issues/pending/` には `.gitkeep` を置き、空でも git に残す。

#### 命名規則

`{seqnum}-{category}-{short-description}.md`

- `seqnum` は 4 桁ゼロパディング（`0001`, `0042`）。9999 を超えたら 5 桁に拡張する
- `seqnum` は `issues/SEQUENCE` の値を使う。issue を新規作成したら同コミットで `+1` する
- `category` は `bug` / `feat` / `doc` / `chore` / `design` のいずれか。`design` は pending に置かれることが多い
- `short-description` は kebab-case の英数字。例: `0001-feat-tier1-core-ipc-implementation.md`

#### issue ファイルの構造

```markdown
---
decision_type: spec | design | implementation | process
supersedes: [0023]
tags: [ipc, sqlite, watch]
closed_at: YYYY-MM-DD
---

# <タイトル>

Created: YYYY-MM-DD

## 概要

## 根拠

## 問題

## 対応方針
```

具体的なコード変更は PR 側に書く。ここに書くのは「なぜ対応する必要があるか」と「どの方針で進めるか」までに留める。

##### frontmatter フィールド

| フィールド | 型 | セマンティクス |
|---|---|---|
| `decision_type` | enum | `spec`（外部契約）/ `design`（内部設計）/ `implementation`（実装 detail）/ `process`（開発プロセス）。意思決定の層 |
| `supersedes` | int[] | 過去 issue 番号の配列（例: `[0023]`）。supersededBy は superseding 側 issue の本文末尾に手書きで双方向参照を張る |
| `tags` | string[] | free-form。当面は明示的な語彙統制を置かず、出現頻度から事後に整理する |
| `closed_at` | date | close 時に確定（`YYYY-MM-DD`）。open / pending では省略可 |

#### 粒度の目安

- **open 時**: 「なぜ取り組むか（根拠）」と「どの方針で進めるか（対応方針）」を中心に書く。実装手順や関数名・行レベルの詳細は書かない（PR / commit body に任せる）。影響する path への言及は構わない
- **close 時**: `## 解決方法` と `## 採用しなかった代替` は **要点だけ**。実装ログ・コード差分の説明・行レベルの判断は commit body と PR description に書く
- **目安**: open + close 合わせて 200 行を超えそうなら、本当に issue で記録すべき意思決定は何か再考する（実装詳細を切り出して別 issue / PR description に移す）
- **例外**: 規約そのものを作る meta issue は、規約と実装例がセットで価値を持つため高密度になることを許容する。通常の issue でこの密度を求めない

#### ライフサイクル

| 状態 | 場所 | 移動契機 | 同時に書く内容 |
|---|---|---|---|
| open | `issues/<id>-<cat>-<slug>.md` | 検出時に新規作成 | Created / 概要 / 根拠 / 問題 / 対応方針 |
| closed | `issues/closed/<id>-<cat>-<slug>.md` | 修正 PR の最終コミットで `git mv` | 末尾に `Completed: YYYY-MM-DD` と `## 解決方法` を追記 |
| pending | `issues/pending/<id>-<cat>-<slug>.md` | 設計判断・外部依存待ちで `git mv` | 末尾に `## Pending YYYY-MM-DD` と保留理由を追記 |
| reopen | `git mv issues/closed/<id>-... issues/<id>-...` | close 後に再発見 | 末尾に `## Reopen YYYY-MM-DD` と再発の経緯を追記。`## 解決方法` は経緯として残す |

#### コミットルール

- 番号が小さい open issue から順に対応する
- issue を新規作成したら同コミットで `issues/SEQUENCE` も `+1` する（失念防止）
- 1 issue の close ごとに 1 コミットを基本とする
- 関連 PR の説明冒頭に `issues/<id>-...` へのリンクを貼る。`.github/workflows/intent.yml` が PR description の issue リンク（または issue を伴わない chore は `(N/A — chore)` 明記）を必須化し、欠落は merge をブロックする。これが why を必ず辿れる状態を担保する装置（git blame → commit → PR → issue リンク → issue 本文）。`.github/pull_request_template.md` がそのフォームを示す — PR description は薄く保ち、「なぜ」「方針」「却下案」は issue 側 / commit body 側に書く

#### バグ発見時のフロー

1. `issues/SEQUENCE` を読み次の番号を決める
2. `issues/<NNNN>-bug-<slug>.md` に再現手順・原因仮説・影響範囲を書く
3. `issues/SEQUENCE` を `+1` する
4. 1 コミットでまとめる（実装着手前に記録するためのコミット）
5. 別ブランチで修正 → 修正 PR の最終コミットで `git mv issues/<id>-... issues/closed/<id>-...` し、`Completed:` と `## 解決方法` を追記する

### テスト

実装着手後は以下を基本とする（コードはまだ無い＝設計フェーズ）。

```fish
go test ./...        # 全テスト
go vet ./...         # 静的検査
```

## ステータス

🚧 設計フェーズ（実装はこれから）。設計の正本は `design.md`。
