# agmsg-go 設計ドキュメント

## 1. 概要 / オリジナルとの関係

`agmsg-go` は、bash 製ツール [**fujibee/agmsg**](https://github.com/fujibee/agmsg) を Go で書き直す fork である。

オリジナル agmsg は、Claude Code / Codex / Gemini CLI / Antigravity などの **CLI AI エージェント同士が、共有 SQLite ファイル 1 個を通信路にしてメッセージをやり取りする**ためのツールである。デーモンを持たず、ネットワークも使わない。各エージェントは `~/.agents/skills/<cmd>/db/messages.db`（SQLite WAL モード）に対し `sqlite3` CLI で直接 INSERT / SELECT することで通信する。中央プロセスは存在しない。

設計思想スローガンは **"No daemon, no network, no complexity"**。「自分で管理する常駐プロセスを持たない」ことを最重視し、受信検知（monitor の watch）はホストのセッション寿命に寿命を預ける形で実現している。

本 fork は、この**通信モデル・通信路・アイデンティティモデル・配信モードといった設計の核を維持したまま**、bash 実装が構造的に抱える弱点（後述）を Go の型システム・標準ライブラリ・テスト機構で解消することを目的とする。

加えて本 fork は、提供範囲を **「エージェント間 IPC のインフラ」だけ**に明確に絞る。レビュー+修正ループや複数 issue の orchestration といった**「IPC をどう使うか」は実装せず、利用側に委ねる**（mechanism, not policy）。この境界の根拠と線引きは §2.1 に記す。

---

## 2. 設計目標と非目標

### 設計目標

| 目標 | 内容 |
|---|---|
| **mechanism, not policy** | agmsg-go は**エージェント間 IPC のインフラ（通信路・配送・宛先解決）だけ**を提供する。「その IPC をどう使うか」（レビュー+修正ループ、複数 issue の orchestration 等）は**一切実装せず、利用側に委ねる**。提供物を「最小限のプリミティブ」に保つことを最優先の制約とする。 |
| No daemon 思想の継承 | broker / 常駐 daemon を新設しない。通信路は共有 SQLite ファイルのまま。受信検知の常駐はホストのセッション寿命に預ける構造を維持する。 |
| 依存最小・単一バイナリ | `sqlite3` CLI を含む外部バイナリ依存を排除し、`go build` で単一バイナリに完結させる。クロスコンパイル可能を維持する。 |
| SQL 安全性 | 文字列連結による SQL 組み立て（手動エスケープ）を撤廃し、placeholder / prepared statement に置き換える。 |
| テスタビリティ | 並行・ライフサイクル・世代管理ロジックを Go の単体テストで検証可能にする。 |
| アイドル効率 | 受信が無い間に `sqlite3` プロセスを定期 fork し続けるアイドルコストを削減する。 |
| 互換性 | 既存の `messages.db` スキーマ・`teams/<team>/config.json`・ユーザ config と互換を保つ（同じ DB を bash 版・Go 版が読めることが望ましい）。 |

### 非目標

- **ネットワーク通信・リモート配送**は対象外。あくまでローカルファイルシステム上の共有 SQLite が通信路である。
- **メッセージの暗号化・認証・アクセス制御**は対象外（ローカルユーザ前提）。
- **GUI / TUI** は対象外。CLI サブコマンドのみ。
- **broker daemon の常設**は非目標（§12 で将来オプションとして言及するに留める）。
- **オーケストレーション / ワークフロー**は対象外。レビュー+修正ループ、複数 issue の orchestration、エージェント役割の自動割り当て、タスク分配、合意形成プロトコルなどの「使い方」は **agmsg-go に作り込まない**。これらは利用側がプリミティブ（`send` / `inbox` / `watch`）を組み合わせて自由に構築する領域である。
- **メッセージ本文の意味解釈・スキーマ強制**は対象外。本文は不透明な `TEXT` であり、JSON 構造やコマンド規約を agmsg が定義・検証することはしない（利用側の合意事項）。

### 2.1 スコープ境界: mechanism, not policy

本プロジェクトの最大の設計判断は「**どこまでを agmsg が持ち、どこからを利用側に委ねるか**」である。agmsg は **transport（運ぶ仕組み）** だけを提供し、**policy（何のために・どんな順序で・誰と運ぶか）** は持たない。

```
┌─────────────────────────────────────────────────────────┐
│  利用側が自由に構築する層 (agmsg の対象外 / policy)        │
│  ・レビュー + 修正ループ                                   │
│  ・複数 issue の orchestration                            │
│  ・役割割り当て / タスク分配 / 合意形成                     │
│  ・本文フォーマットの規約 (JSON 等)                        │
└──────────────────────▲──────────────────────────────────┘
                       │ send / inbox / watch を呼ぶだけ
┌──────────────────────┴──────────────────────────────────┐
│  agmsg-go が提供する層 (本プロジェクト / mechanism)        │
│  ・宛先解決: (name, team) アイデンティティ                 │
│  ・送信:     send   (INSERT, placeholder)                │
│  ・受信:     inbox  (未読 SELECT + 既読化)                │
│  ・購読:     watch  (id > watermark を stream)           │
│  ・配送経路: delivery mode + ホストフック用エントリポイント  │
└──────────────────────▲──────────────────────────────────┘
                       │ 共有 SQLite ファイルへの読み書き
                  messages.db (WAL)
```

この境界を守る判断基準: **「複数の利用側が別々の使い方をしうる機能」は agmsg に入れない。** 例えば「レビュー担当に振り分ける」のは一つの使い方にすぎず、別の利用側は「全 issue を担当者へ broadcast する」かもしれない。両者が共通して必要とするのは「宛先へ運ぶ」だけなので、agmsg はそれだけを提供する。迷ったら**プリミティブ側に倒さず、利用側に委ねる**。

---

## 3. なぜ Go か

### 3.1 正当化は「規模(LOC)」ではなく「複雑さの質」

agmsg を Go に書き直す正当化は、行数が多いからではない。**bash 実装が抱える複雑さの「質」が、ちょうど sh の不得手と一致している**からである。以下の各弱点は、Go の標準的な道具立てで構造的に消える。

| オリジナルの構造的弱点 | Go 化による解決 |
|---|---|
| **(1) SQL エスケープが手動 `sed "s/'/''/g"` 依存**。文字列連結で SQL を組み立てており、エスケープ漏れが 1 箇所でもあれば破綻する。**最大の構造的弱点。** | **prepared statement / placeholder** で「文字列としてエスケープする」問題自体を構造的に消す。`database/sql` の `?` バインドにより、メッセージ本文に何が入ろうと SQL インジェクションも構文破壊も起こらない。**これが Go 化の最大の利得。** |
| **(2) session-start.sh の複雑さ**。pid / session_id / cc_pid を `ps -o` で追い、`/clear` や `--resume` による SessionStart 再発火での二重ウォッチャ防止、孤児プロセス回収、pid 再利用対策を、毎回ゼロから走る短命スクリプト内で再計算している。 | pid / 世代 / セッション同一性を**構造体 + テスト可能な関数**に分解する。状態遷移を単体テストで固定できる。 |
| **(3) OS 分岐**。`ps -o`, `stat -f`(Darwin) / `stat -c`(Linux) などプラットフォーム差を都度分岐。 | **標準ライブラリ + ビルドタグ**で吸収。`os`, `os/exec`, `runtime`, `//go:build` により分岐を局所化・型安全化する。 |
| **(4) テスタビリティ**。bats でしかテストできず、並行・ライフサイクル系ロジックが検証しづらい。 | 通常の `go test`（テーブルドリブン・並行テスト・`testing` のリーク検出）が書ける。 |
| **(5) monitor のポーリング**。何も来ていない時も 5 秒ごとに `sqlite3` プロセスを fork し続ける（アイドルコスト）。 | fsnotify による**イベント駆動**で「何も来ない時は何もしない」を実現（§8）。 |

### 3.2 得るもの / 失うもの

**得るもの**: SQL 安全性（構造的保証）、外部 `sqlite3` CLI 依存の排除、単体テスト、アイドル効率、型による状態管理。

**失うもの**（§12 のトレードオフでも再掲）:

- ユーザが `cat *.sh` でツールの中身を**そのまま監査できる透明性**を失う（コンパイル済みバイナリになる）。
- macOS 配布時の**コード署名 / notarization** という新たな手間が生じる（bash スクリプトには不要だった）。
- ビルド工程（`go build` / クロスビルド）が配布の前提になる。

---

## 4. 全体アーキテクチャ

中央プロセスは存在しない。各エージェント（の背後で動く `agmsg` バイナリ）が、共有 SQLite ファイルに直接読み書きする。

```
                    共有ファイルシステム (ローカル)
        ┌─────────────────────────────────────────────────┐
        │   ~/.agents/skills/<cmd>/db/messages.db          │
        │   ├── messages.db        (メイン DB)              │
        │   ├── messages.db-wal    (WAL: 書き込みはここに乗る)│
        │   └── messages.db-shm                            │
        │   teams/<team>/config.json   (チーム名簿)          │
        │   config (ユーザ設定: 配信モード等)                 │
        └─────────────────────────────────────────────────┘
              ▲            ▲                    ▲
              │ INSERT     │ SELECT             │ SELECT (未読)
              │ (placeholder)                   │ + fsnotify watch
              │            │                    │
   ┌──────────┴───┐ ┌──────┴───────┐  ┌─────────┴──────────┐
   │ agmsg send   │ │ agmsg inbox  │  │ agmsg watch        │
   │ (Agent A)    │ │ (Agent B)    │  │ (Agent C: monitor) │
   └──────┬───────┘ └──────────────┘  └─────────┬──────────┘
          │                                     │ stream 1 行/件
   ┌──────┴───────┐                   ┌─────────┴──────────┐
   │ Claude Code  │                   │ ホスト Monitor ツール │
   │ Codex / etc. │                   │ (セッション寿命に追従) │
   └──────────────┘                   └────────────────────┘

   全エージェントが「同じ単一バイナリ agmsg」のサブコマンドを呼ぶ。
   No daemon: agmsg 自身は常駐 broker を持たない。
   watch のみがホストセッションの寿命に追従する長命プロセス。
```

複数リーダ + 1 ライタの SQLite WAL モデルにより、複数エージェントの同時読み取りと逐次書き込みが安全に成立する。

---

## 5. データモデル

### 5.1 messages テーブル

オリジナルと互換のスキーマを維持する。

```sql
CREATE TABLE messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,  -- 単調増加。watermark に使う
  team TEXT NOT NULL,
  from_agent TEXT NOT NULL,
  to_agent TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  read_at TEXT                           -- NULL = 未読
);

CREATE INDEX idx_unread  ON messages(team, to_agent, read_at);  -- 未読検索用
CREATE INDEX idx_history ON messages(team, created_at);          -- 履歴用
```

- `id` は単調増加であり、後述の watch が watermark（既読位置）として使う。
- `read_at IS NULL` が未読を表す。inbox は未読のみを返し、読んだら `read_at` を埋める。
- すべての値挿入・検索条件は **placeholder バインド（`?`）** で渡す。本文 `body` に `'` や `;` や改行が含まれても安全。これが bash 版との決定的な差。

### 5.2 SQLite ドライバ選定

| ドライバ | 方式 | 単一バイナリ | クロスコンパイル | 評価 |
|---|---|---|---|---|
| **modernc.org/sqlite** | **純 Go（CGO 不要）** | ◎ | ◎（`GOOS`/`GOARCH` を変えるだけ） | **第一候補（採用）** |
| mattn/go-sqlite3 | CGO（C 実装をリンク） | △（C ツールチェーン必須） | ✕（ターゲットごとに C クロスコンパイラが必要） | 却下寄り |

**採用: `modernc.org/sqlite`。** 理由は設計目標「単一バイナリ・クロスコンパイル維持・外部依存最小」と直結する。CGO を使わないため、`CGO_ENABLED=0` のまま `GOOS=linux GOARCH=arm64 go build` のような単純クロスビルドが成立し、配布が容易。実行時に `sqlite3` CLI を呼ぶ必要もなくなり（SQLite エンジンがバイナリ内蔵）、§2 の「外部バイナリ依存排除」が完成する。

`mattn/go-sqlite3` は成熟度・性能で勝るが、CGO により C ツールチェーンとターゲット別クロスコンパイラを要求し、本プロジェクトの「単一バイナリで簡単に配れる」という核心価値を損なう。性能要件は人間×LLM のメッセージング程度では純 Go 実装で十分であり、却下する。

### 5.3 teams config / user config

- **チーム名簿**: `teams/<team>/config.json`。`agents` 配下に `registrations[]` を持つ JSON。オリジナル互換。Go では構造体に unmarshal して扱う。
- **ユーザ config**: 配信モード（monitor/turn/both/off）等のユーザ設定を保持。`agmsg config` / `agmsg delivery set` で読み書きする。

---

## 6. アイデンティティモデル

エージェントは **`(name, team)` の組**で識別される。`project path` と `type`（claude-code / codex / gemini / antigravity）は**メタデータ**であり、同一性の判定には使わない。

- 同じ `(name, team)` であれば、複数の project から join しても**同一アイデンティティ**として扱われ、`registrations[]` に登録情報が積まれる。
- `whoami` は登録状態を表現する。状態は概念的に 4 つ（未登録 / 単一登録 / 複数登録（複数 project から同一名で参加）/ actas による役割多重）に整理できる。
- **`actas <name>` / `drop <name>`**: 同一 project・同一 type で、役割（name）だけを複数持つための仕組み。1 つのセッションが複数の役割を演じ分けたり外したりできる。

Go では `(name, team)` を値型のキーとして扱い、registration をスライスで保持する。同一性判定が型で固定されるため、bash 版で文字列比較に散在していた同一性ロジックを 1 箇所に集約できる。

---

## 7. 配信モード（受信の割り込み）とホストフック連携

受信メッセージを、どうやってエージェントの「コンテキスト」に割り込ませるか（＝エージェントに気づかせるか）を決めるのが配信モードである。オリジナルの 4 モードを踏襲する。

| mode | 機構 | 遅延 | 主な対象 |
|---|---|---|---|
| **monitor**（Claude Code 既定） | SessionStart フック → ホストの Monitor ツール → `agmsg watch` がストリーム | 約 5 秒（fsnotify 化で実質即時、§8） | Claude Code（Monitor ツールあり） |
| **turn**（Codex 既定） | Stop フック → ターン間に `agmsg check-inbox` | 次の発話まで | Monitor ツールが無い Codex 等 |
| **both** | monitor を主、turn を保険として併用 | 約 5 秒 | 取りこぼし防止重視 |
| **off** | 自動配信なし（手動 `agmsg inbox`） | — | 手動派 |

ホストフック（SessionStart / Stop）から呼び出すエントリポイントも、**同じ単一バイナリのサブコマンド**にする（`agmsg watch`, `agmsg check-inbox` 等）。これにより、フックスクリプトはバイナリを呼ぶだけの薄い殻になり、bash 実装で session-start.sh に集中していた複雑さをバイナリ内部の型安全なコードへ移動できる。

`monitor` の watch は、起動時に `MAX(id)` を watermark とし、`id > LAST` の差分のみをストリームする。出力は 1 行 = `<ts> | <team> | <from> → <to> | <body>`。

---

## 8. 受信検知の設計

### 8.1 アーキテクチャ選択肢の比較

| 案 | 機構 | 遅延 | アイドルコスト | broker ライフサイクル | 評価 |
|---|---|---|---|---|---|
| **A. ファイルポーリング**（オリジナル移植） | 定期的に `SELECT ... WHERE id > LAST` | 約 5 秒 | **高**（何も来なくても定期実行） | 不要 | 単純だがアイドル無駄 |
| **B. fsnotify + ファイル監視 + ポーリング保険** | OS のファイル変更通知でトリガ、長めのポーリングを保険併走 | 実質即時 | **低**（変更が無ければ何もしない） | **不要** | **採用** |
| **C. ローカル broker daemon + Unix ソケット push** | 常駐 broker が変更を push | 即時 | ゼロ | **必要（地獄）** | 却下（将来オプション） |

### 8.2 採用: 案 B（fsnotify + ポーリング保険）

**最大の美点は「broker のライフサイクル管理を持ち込まずに済むこと」**、すなわちオリジナルの **No daemon 思想を尊重したままアイドル効率を改善できる**点にある。

Go の [`github.com/fsnotify/fsnotify`](https://github.com/fsnotify/fsnotify) は、`inotify`(Linux) / `kqueue`(BSD) / `FSEvents`(macOS) / `ReadDirectoryChangesW`(Windows) を **1 つの API に抽象化**する。オリジナルが fsnotify を避けた理由（外部バイナリ依存・プラットフォーム分岐の保守コスト・ポーリングとの両建てによる行数増）は、Go の標準的なライブラリ採用によって**ほぼ消える**。

#### 正当化は「遅延短縮」ではなく「アイドル効率」に置く

fsnotify を採用する正当化は、遅延を 5 秒 → 即時に縮める点**ではない**。実用ワークロードでは、受信の数秒の遅延は **LLM の推論時間に埋もれて体感差にならない**。本当の利得は **「何も来ていない時に、何のプロセスも fork せず、CPU も使わず、静かに待てる」アイドル効率**である。これが案 A に対する明確な優位点。

### 8.3 取りこぼし対策: 長めのポーリングを保険として併走

fsnotify は万能ではなく、以下の理由でイベントを**取りこぼしうる**:

- イベントキューの溢れ（短時間に大量変更）
- 監視を確立する**前**に発生した書き込み
- OS による変更イベントの coalescing（まとめられて 1 回になる）

そのため、**長めのポーリング（例: 30 秒間隔）を保険として常時併走**させ、fsnotify がイベントを落としても最大 30 秒で必ず追いつく構造にする。これは「fsnotify を主、ポーリングを保険」という堅牢な fsnotify 実装の定石である。アイドルコストは案 A（5 秒ポーリング）より大幅に低い。

いずれの経路でトリガされても、実際の取得は **`id > watermark` の SELECT** に一本化する。fsnotify は「いつ SELECT を撃つか」を決めるだけで、何が新着かは常に DB が単調増加 id で正しく答える。これにより重複配信や順序逆転は起きない。

### 8.4 WAL を fsnotify で見る際の罠

SQLite を WAL モードで使う場合、**書き込みは `messages.db` 本体ではなく `messages.db-wal` ファイルに乗る**。checkpoint（WAL → 本体への反映）が起きるまで本体ファイルの mtime は更新されないことがあり、また mtime の粒度（秒単位など）の影響で「**`messages.db` 本体だけを監視する**」という直感的な実装は期待どおりに発火しない。

#### 監視対象の設計判断

- **`messages.db` 単体を監視 → 不可**（WAL に書かれるため本体が動かない）。
- **`messages.db-wal` を監視**: 書き込みを最も早く捉えられるが、`-wal` は checkpoint で truncate / 再作成されうるため、ファイルの作り直しに監視が追従できる実装にする必要がある。
- **DB の置かれた*ディレクトリ全体*を監視（採用方針）**: `messages.db` / `-wal` / `-shm` のいずれが変化しても発火する。`-wal` の再作成にも強い。発火条件をファイル単位で絞らずディレクトリ単位にすることで、WAL の実装詳細に依存しない堅牢さを得る。発火後の実取得は §8.3 のとおり `id > watermark` SELECT に委ねるため、過剰発火（無関係な `-shm` 変更等）があっても「SELECT して 0 件」で安全に空振りするだけで害はない。

### 8.5 実装と実機検証の結論（issue 0002）

§8.2〜§8.4 の方針を `internal/watch`（fsnotify ディレクトリ監視 + ポーリング保険）として実装し、Linux / macOS / Windows の CI matrix（`.github/workflows/test.yml`）で検証した。確定事項:

- **監視対象は DB ディレクトリ（`db/`）**。ファイル単位（`messages.db` 単体や `-wal` 単体）ではなくディレクトリを `fsnotify.Add` する。`messages.db-wal` への追記でディレクトリ監視が発火することをテストで直接確認した（`TestFSNotifyFiresOnWALFile`）。これにより §8.4 の「本体だけ監視すると発火しない」罠と、checkpoint による `-wal` 再作成の両方を回避する。
- **ポーリング保険間隔は 30 秒**（`defaultPollInterval`）を既定とする。fsnotify が主・典型遅延は実質即時で、保険は取りこぼし時の上限追従にすぎないため、案 A の 5 秒より大幅に長く取りアイドルコストを抑える。`--interval` で上書き可能。fsnotify を意図的に無効化（`DisableFSNotify`）してもポーリングのみで追従することをテストで確認した。
- **トリガと取得の分離を徹底**。fsnotify／ポーリングのどちらでトリガされても取得は `id > watermark` SELECT に一本化（`OnTrigger`）。fsnotify イベントのバーストは短い debounce 窓（50ms）で 1 回に畳む。起動直後に一度同期し、watermark 確定〜監視確立の隙間の書き込みも取りこぼさない。
- **fsnotify は必須ではない**。初期化や `Add` に失敗してもエラーを通知（stderr）したうえでポーリング保険にフォールバックし、watch は止めない。OS ごとの通知機構（inotify / FSEvents / ReadDirectoryChangesW）の差は fsnotify が吸収し、coalescing による丸めは watermark 追従と 30 秒保険で漏れなく回収される。

---

## 9. プロセス / ライフサイクル管理

`watch`（monitor モードの長命プロセス）は、agmsg において唯一の常駐的存在であり、オリジナルで session-start.sh が苦労していた領域である。Go では以下を**構造体 + テスト可能な関数**として実装し、安全性を型で担保する。

### 9.1 二重起動防止（多重ウォッチャ防止）

`/clear` や `--resume` による SessionStart の再発火で、同一セッションに対し watch が二重に起動する事故を防ぐ。

- セッション同一性（session_id / 親 pid 等）を**明示的な構造体フィールド**として保持し、起動時に既存ウォッチャの生存確認を行う。
- ロック手段としてはロックファイル（pid + 世代を記録）を用い、**pid 再利用**（同じ pid 値が別プロセスに再割り当てされる問題）に対しては pid 単独ではなく「pid + 起動時刻 / 世代トークン」で同一性を判定する。bash 版で `ps -o` を都度パースしていた処理を、検証可能な 1 関数に閉じ込める。

### 9.2 孤児プロセス回収

ホストセッションが消えたのに watch だけが生き残る孤児を回収する。

- watch は**親（ホストセッション）の生存を監視**し、親が消えたら自身も終了する（self-terminate）。
- 起動時に、ロックファイルが指す旧ウォッチャが既に死んでいれば、その残骸（stale lock）を回収してから起動する。

### 9.3 テスト容易性

上記のライフサイクル判定（生存確認・世代比較・stale lock 回収）はすべて副作用を注入可能なインターフェース越しに書き、`go test` で状態遷移を固定する。これは bash + bats では困難だった領域であり、Go 化の主要な動機の 1 つ（§3.1 弱点 (2)(4)）。

### 9.4 「No daemon」は半分標語であることの明記

正直に書くと、**monitor の watch は実態として常駐ポーリング（兼イベント待ち）プロセス**であり、この点は Go 化後も変わらない。watch はホストのセッション寿命に自らの寿命を預けることで「**自分で管理する常駐 broker は持たない**」を成立させているにすぎず、「プロセスが一切常駐しない」わけではない。

Go 化が変えるのは「常駐をなくすこと」ではなく、**「その常駐の管理（二重起動防止・孤児回収・世代管理）を、型と構造とテストで安全にすること」**である。No daemon は思想としては真だが、文字どおりではない——この点を曖昧にしない。

---

## 10. CLI サブコマンド構成

オリジナルの各 `*.sh` に対応するサブコマンドを、単一バイナリ `agmsg` のサブコマンドとして再設計する。ただし §2.1 のスコープ境界に従い、**「最小限の IPC プリミティブ」を先に固め、それ以外は補助／後回し可**として段階を明示する。最小コアだけで「送る・受け取る・購読する」という IPC は完結する。

#### Tier 1: 最小コア（IPC プリミティブ — これだけで通信は成立する）

| オリジナル `*.sh` | Go サブコマンド | 役割 |
|---|---|---|
| send.sh | `agmsg send <to> <body>` | メッセージ送信（INSERT、placeholder バインド） |
| inbox.sh | `agmsg inbox` | 未読メッセージ取得（取得後 `read_at` 更新） |
| watch.sh | `agmsg watch` | `id > watermark` を stream する購読（フックからも起動） |
| join.sh / leave.sh | `agmsg join <team>` / `agmsg leave <team>` | 宛先解決の前提となるチーム参加 / 離脱 |
| whoami.sh | `agmsg whoami` | 自アイデンティティ表示（送信者の確定に必要） |

#### Tier 2: 補助（運用に要るが IPC の本質ではない）

| オリジナル `*.sh` | Go サブコマンド | 役割 |
|---|---|---|
| history.sh | `agmsg history [N]` | 履歴（最新 N 件） |
| check-inbox.sh | `agmsg check-inbox` | turn モードのターン間チェック（フックから起動） |
| delivery.sh | `agmsg delivery set <mode>` | 配信モード設定（monitor/turn/both/off） |
| team.sh / identities.sh | `agmsg team` / `agmsg identities` | チーム名簿 / 登録一覧の表示 |
| config.sh | `agmsg config` | ユーザ設定の読み書き |

#### Tier 3: 任意（あると便利だが初期実装では後回し可）

| オリジナル `*.sh` | Go サブコマンド | 役割 |
|---|---|---|
| reset.sh | `agmsg reset` | DB / 状態のリセット |
| rename.sh / rename-team.sh | `agmsg rename` / `agmsg rename-team` | 名前 / チーム名の変更 |
| actas.sh / drop.sh | `agmsg actas <name>` / `agmsg drop <name>` | 役割（name）の多重追加 / 除去 |

ホストフック（SessionStart / Stop）から呼ぶエントリポイント（`watch` / `check-inbox`）も同じバイナリのサブコマンドにすることで、配布物は 1 つで完結する。**いずれのサブコマンドにもオーケストレーション的判断（誰にどう振り分けるか等）は持ち込まない**——それらは利用側が Tier 1 を組み合わせて実装する（§2.1）。なお actas/drop が役割を「多重に持てる」機能を提供するに留め、その役割をどう使い分けるか（例: レビュー役と実装役の演じ分け）は利用側の policy である。

---

## 11. パッケージ構成案

```
agmsg-go/
├── cmd/
│   └── agmsg/
│       └── main.go            # エントリポイント（サブコマンド dispatch のみ）
├── internal/
│   ├── cli/                   # 各サブコマンドの定義・引数解析
│   │   ├── send.go
│   │   ├── inbox.go
│   │   ├── watch.go
│   │   ├── delivery.go
│   │   └── ...
│   ├── store/                 # SQLite アクセス層（placeholder バインドを集約）
│   │   ├── store.go           # messages の INSERT/SELECT/未読更新
│   │   ├── schema.go          # CREATE TABLE / INDEX / migration
│   │   └── store_test.go
│   ├── identity/              # (name, team) アイデンティティ・registration・actas/drop
│   │   ├── identity.go
│   │   └── identity_test.go
│   ├── watch/                 # 受信検知（fsnotify + ポーリング保険）
│   │   ├── watcher.go         # fsnotify とポーリングの併走・watermark 管理
│   │   ├── lifecycle.go       # 二重起動防止・孤児回収・世代管理
│   │   └── watcher_test.go
│   ├── delivery/              # 配信モード（monitor/turn/both/off）の解釈
│   ├── config/                # ユーザ config / teams config の読み書き
│   │   ├── user.go
│   │   └── team.go
│   └── paths/                 # ~/.agents/... のパス解決・OS 差吸収
│       ├── paths.go
│       ├── paths_darwin.go    # //go:build darwin
│       └── paths_linux.go     # //go:build linux
├── design.md
├── README.md
├── LICENSE
└── go.mod
```

- **`internal/store`**: SQL を扱う唯一の層。placeholder バインドをここに閉じ込め、上位層は文字列 SQL を一切組み立てない（弱点 (1) の構造的封じ込め）。
- **`internal/watch/lifecycle.go`**: §9 のライフサイクル管理。副作用を注入可能にして単体テスト。
- **`internal/paths`**: OS 差をビルドタグで局所化（弱点 (3)）。
- `internal/` 配下にすることで、外部からの import を禁じ、公開 API 面を持たない（このツールはライブラリではなくアプリ）。

---

## 12. トレードオフと将来課題

### 12.1 案 C（broker daemon）について

ローカル broker daemon + Unix ソケットで push する案 C は、**遅延もアイドルコストも構造的にゼロ**にできる魅力がある。しかし、

- 誰が broker を起動するのか
- クラッシュ時に誰が再起動するのか
- アップデート時に旧プロセスをどう kill するのか
- 複数バージョンの broker が同時に走ったらどうなるか

という「**自分で管理する常駐プロセスのライフサイクル地獄**」を復活させる。これはオリジナルが最も避けたかったもの（No daemon 思想）と正面衝突する。よって**却下**する。将来、どうしても push 型の即時性が要求されるユースケース（高頻度・低遅延が LLM 推論時間に埋もれず効くケース）が現れた場合の**オプション**としてのみ言及するに留める。

### 12.2 Go 化で失うもの

| 失うもの | 内容 | 緩和策 |
|---|---|---|
| 透明性 | `cat *.sh` でツールの中身をそのまま読めなくなる。 | OSS としてソース公開・ビルド再現性を担保。 |
| コード署名 / notarization | macOS 配布でバイナリの署名・公証が新たに必要。 | リリース手順に組み込む（bash には無かった工程）。 |
| ビルド前提 | 配布にビルド工程（クロスビルド）が要る。 | CI でのマルチプラットフォームビルド。modernc.org/sqlite 採用で CGO 不要、クロスビルドは単純。 |

### 12.3 その他の将来課題

WAL 監視の実地検証とポーリング保険間隔の確定は issue 0002 で完了し、結論は §8.5 に反映済み（OS 横断の発火再現は `.github/workflows/test.yml` の matrix で継続検証する）。残る課題:

- **bash 版との DB 互換性検証**: 同一 `messages.db` を bash 版・Go 版で混在運用できるかの確認。
- **`watch` の親プロセス検知の移植性**: 親セッション消滅の検知手段が OS 横断で堅牢かの検証。
```
