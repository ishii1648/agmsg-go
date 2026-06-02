---
decision_type: design
tags: [upgrade, supply-chain, security, release]
closed_at: 2026-06-02
---

# upgrade のリリース成果物に独立した署名検証を導入するか

Created: 2026-06-02

影響する path: `internal/upgrade/upgrade.go`、`.github/workflows/`（release / GoReleaser 設定、[[0003-chore-release-go-binary-workflow]]）。

## 概要

`agmsg upgrade` は SHA-256 を `checksums.txt` と突合するが、その checksums は**アセットと
同一のリリース信頼ドメイン**（同じ GitHub Release）から取得され、独立した署名は無い。
GitHub / リリース資格情報 / GoReleaser / リポジトリのいずれかが侵害されると、攻撃者は
バイナリと checksums を同時に差し替えられ、検証を通過して任意コードがインストールされる。
セキュリティレビューが Critical 級として挙げる供給網の論点。pending として判断を記録し、
[[0005-feat-self-upgrade-subcommand]] の既知の限界を明示する。

## 根拠

- 現状の緩和（tar パストラバーサル防止・通常ファイルのみ展開・checksum 突合・atomic rename）は
  「配信経路の改ざん」には有効だが、「信頼ドメインそのものの侵害」には無力。
- ただしこの脅威はリポジトリ所有者の制御下にある資産の侵害が前提で、agmsg 単体では完全には
  閉じられない。導入には署名鍵の生成・保管・配布（公開鍵の同梱）と release パイプライン変更を伴う
  ため、即着手でなく設計判断として保留する。

## 検討軸（未決）

- **署名方式**: cosign（keyless / OIDC）か minisign / age 系の静的公開鍵同梱か。後者は鍵を
  バイナリに焼き込めば信頼ドメインを分離できるが鍵更新が難しい。
- **検証の必須/任意**: 署名が無い旧リリースへの後方互換、検証失敗時の挙動（fail-closed か警告か）。
- **コスト対効果**: 単一開発者ワークステーション・オプトイン upgrade という運用前提で、
  鍵運用の負担に見合うか。見合わないなら「checksum 同梱の限界」を README/design に明記して
  受容する選択肢も含めて決める。

## Pending 2026-06-02

実装可能だが、鍵運用と release パイプライン変更を要する設計判断のため保留。
[[0007-fix-upgrade-bound-download-extract-size]]（独立して着手可能な DoS 面）を先に塞ぐ。

Completed: 2026-06-02

## 解決方法（moot 化により close）

upgrade サブコマンドを削除した（[[0010-spec-remove-upgrade-subcommand]]）ことで、本 issue が前提と
していた「self-upgrade クライアントが checksums を検証する」経路そのものが消滅した。よって
「アセット署名を upgrade 側で検証するか」という論点は moot となり close する。

残る配布経路と、そこで供給網リスクをどう扱うかは下記に整理し直す:

- **`go install ...@latest`（主経路）**: 信頼は Go module proxy / checksum DB（`sum.golang.org`）と
  GitHub に委ねられる。agmsg 側のコードで足せる検証は無く、Go ツールチェーンの仕組みに乗る。
- **GitHub Releases のバイナリ直配布（[[0003-chore-release-go-binary-workflow]]、維持）**: 自己更新は
  しないが、ユーザが手動 DL する際の真正性は依然 checksums のみで未署名。これを署名するか否かは
  「リリース成果物の署名」という**配布側だけの独立した判断**に縮小する。必要になれば別 issue
  （cosign keyless / minisign 静的鍵同梱など）として design から再起票する。本 issue では未署名を
  現状の受容点として確定し、過度な前倒しはしない。
