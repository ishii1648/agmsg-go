---
decision_type: implementation
tags: [upgrade, dos, network, security]
closed_at: 2026-06-02
---

# upgrade のダウンロード/tar 展開にサイズ上限を設ける

Created: 2026-06-02

影響する path: `internal/upgrade/upgrade.go`（`downloadAndVerify` / `extractBinary`）。

## 概要

`agmsg upgrade` はリリースアセットのダウンロードと tar 展開のどちらにもサイズ上限が無く、
攻撃者制御（あるいは破損）レスポンスでローカルのディスク/メモリを枯渇させられる。セキュリティ
レビューが指摘する "Download size is not bounded"（Medium）に該当し、唯一のネットワーク向き
コードパス（[[0005-feat-self-upgrade-subcommand]]）の DoS 面を塞ぐ。

## 根拠

- `downloadAndVerify` の `io.Copy(io.MultiWriter(f, h), resp.Body)` はレスポンスボディを無制限に
  temp ファイルへ書き込む。`Content-Length` 検査も `io.LimitReader` も無い。
- `extractBinary` の `io.Copy(out, tr)` も tar エントリを無制限に展開する。gzip 展開後サイズは
  圧縮率次第でいくらでも膨らむため、小さな tarball で巨大ファイルを書き出す decompression bomb が成立する。
- チェックサム検証はダウンロード**完了後**に走るので、検証前の段階で枯渇させられる。

## 問題

GitHub 本体／リリース資格情報の侵害は別レイヤの脅威（[[0009-design-upgrade-release-signing]]）だが、
サイズ上限の欠落は信頼ドメインの侵害を前提にしなくても、経路上の改ざんや単純な破損で
ローカル DoS を起こしうる、独立した堅牢性欠陥。

## 対応方針

- ダウンロード・展開とも既知バイナリの現実的上限（例: 数十 MB オーダ）を定数で持ち、
  `io.LimitReader` でラップして超過時にエラーを返す。上限値は将来のバイナリ肥大化を見込んで決める。
- 可能なら `resp.ContentLength` が上限超過なら早期に弾く（ただし `-1` や詐称があるため
  `LimitReader` を真の防壁とし、ContentLength は早期 short-circuit の補助に留める）。
- selftest / 既存 upgrade テストに「上限超過で失敗する」回帰ケースを追加。

Completed: 2026-06-02

## 解決方法

サイズ上限の実装ではなく、発生源である upgrade サブコマンド自体を削除して解消
（[[0010-spec-remove-upgrade-subcommand]]）。`internal/upgrade` が消えたため
`downloadAndVerify` / `extractBinary` の無制限 `io.Copy` も存在しなくなり、本 DoS 面は閉じた。
独立してサイズ上限を実装する案（## 対応方針）は、スコープ外機能の延命になるため採らなかった。
