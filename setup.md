# セットアップ

`agmsg`（IPC binary）と skills 層（`dispatch` / `review-loop`）の導入手順。2 つの層の関係は [design.md §13](./design.md)、概要は [README](./README.md) を参照。

## TL;DR

```sh
go install github.com/ishii1648/agmsg-go/cmd/agmsg@latest   # binary を導入
agmsg skills install                                        # skills を ~/.claude/skills へ展開
```

これで協調ワークフロー一式が揃う。`go install` が `$GOBIN`（既定 `$(go env GOPATH)/bin`）に `agmsg` を置くので、そこが `PATH` に入っていること。

## 1. 前提

| 用途 | 必要なもの |
|---|---|
| binary のビルド | Go 1.25+ |
| skills 実行（共通） | `agmsg` が `PATH` にあること、tmux セッション内、git リポジトリ内 |
| `dispatch` | `ghq`（リポジトリ解決に使用）、起動する CLI（`claude` / `codex`） |
| `review-loop` | レビュアーに使う逆エージェント（`claude` 実装役なら `codex`、その逆も）。pane の shell は bash / zsh / fish 3.4+ |

> skills は IPC プリミティブを使う policy 層であり、`agmsg` binary を `send` / `inbox` / `watch` / `join` 経由でのみ呼ぶ。binary が無いと skills は動作しない。

## 2. binary `agmsg` を導入する

### go install（推奨）

go ツールチェーンが生成するバイナリには macOS の quarantine 属性が付かないため、Gatekeeper 警告なしで実行できる。

```sh
go install github.com/ishii1648/agmsg-go/cmd/agmsg@latest
```

### make install

リポジトリを clone 済みなら、ソースビルドで `~/.local/bin/agmsg` に配置する（`PREFIX` で変更可）。

```sh
make install                 # ~/.local/bin/agmsg
make install PREFIX=~/.opt   # ~/.opt/bin/agmsg
```

### リリースバイナリ

[Releases](https://github.com/ishii1648/agmsg-go/releases) から OS / arch 別 tar.gz を取得。未署名のため macOS では初回に Gatekeeper 警告が出る。`xattr -d com.apple.quarantine ./agmsg` で解除するか、上記 2 経路を使う。

### 確認

```sh
agmsg version
agmsg help
```

`PATH` に通っていない場合（fish）:

```fish
fish_add_path (go env GOPATH)/bin   # go install の場合
fish_add_path ~/.local/bin          # make install の場合
```

## 3. skills を展開する

skills は binary に `go:embed` されており、`agmsg skills install` で取り出す。**既定の展開先は `~/.claude/skills`**（Claude Code が自動で読み込む場所）。

```sh
agmsg skills list               # 同梱 skills の一覧を確認
agmsg skills install            # ~/.claude/skills へ展開
agmsg skills install --dest DIR # 展開先を指定
agmsg skills install --force    # 既存ファイルを上書き（既定は保持してスキップ）
```

- `*.sh` は実行ビット付き（0755）で展開される。
- `--force` を付けない限り、既存ファイルは上書きせず `skip` と表示する（手元の改変を保護）。

展開後の構成:

```
~/.claude/skills/
├── dispatch/
│   ├── SKILL.md
│   └── dispatch.sh
└── review-loop/
    ├── SKILL.md
    └── review-loop.sh
```

### 別の場所へ入れたい場合

- Codex から駆動するなら、その skills ディレクトリを `--dest` に指定する（例: `agmsg skills install --dest ~/.codex/skills`）。
- 複数ツールで共有したい場合は `--dest` を各ツールの skills ディレクトリに向けて複数回実行する。

## 4. 動作確認

binary（共有 SQLite IPC、DB 既定 `~/.agents/skills/agmsg`）の疎通:

```sh
agmsg join demo alice           # demo チームに alice として参加
agmsg send alice "hello"        # 自分宛に送信
agmsg inbox                     # 未読を取得（"hello" が見えれば OK）
```

skills の読み込み: Claude Code を起動し、`/dispatch` / `/review-loop` がスラッシュコマンドとして認識されれば導入完了。各 skill の手順は展開された `SKILL.md` に記載。

`review-loop.sh` の純粋ロジックは外部依存なしで自己テストできる:

```sh
bash ~/.claude/skills/review-loop/review-loop.sh selftest   # SELFTEST: PASS
```

## 5. AGMSG_HOME（任意）

DB / チーム名簿の置き場所は環境変数 `AGMSG_HOME` で上書きできる（既定 `~/.agents/skills/agmsg`）。

```fish
set -gx AGMSG_HOME ~/my-agmsg
```

`review-loop` / `dispatch` は、起動するレビュアー / agent にも同じ `AGMSG_HOME` を伝播する（設定時のみ）。実装役と起動先が同じ DB を参照するために必要なので、`AGMSG_HOME` を使う場合はシェルの rc で `export` しておくのが確実。

## 6. アンインストール

```sh
make uninstall                              # make install で入れた binary を削除
rm -rf ~/.claude/skills/dispatch ~/.claude/skills/review-loop   # 展開した skills を削除
```
