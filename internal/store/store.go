// Package store は messages.db への SQLite アクセスを担う「SQL を扱う唯一の層」。
//
// agmsg-go 最大の設計目的は、bash 版の手動 SQL エスケープ (sed "s/'/''/g")
// を構造的に撲滅すること (design.md §3.1 弱点(1) / §11)。本パッケージは
// すべての値挿入・検索条件を database/sql の placeholder (?) バインドで渡し、
// 上位層には文字列 SQL を一切組み立てさせない。body に ' ; 改行 -- が含まれても
// SQL インジェクション・構文破壊は起こらない。
//
// SQLite ドライバは modernc.org/sqlite（純 Go・CGO 不要）を採用 (design.md §5.2)。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	_ "modernc.org/sqlite"
)

// Message は messages テーブルの 1 行に対応する。
type Message struct {
	ID        int64
	Team      string
	From      string
	To        string
	Body      string
	CreatedAt string
	ReadAt    sql.NullString
}

// Store は messages.db への接続を保持する。
type Store struct {
	db *sql.DB
}

// Open は dbPath の SQLite を WAL モードで開き、スキーマを冪等に適用する。
// dbPath が置かれるディレクトリは呼び出し側で用意しておくこと（paths.EnsureDBDir）。
func Open(ctx context.Context, dbPath string) (*Store, error) {
	// WAL + busy_timeout を DSN pragma で設定。
	// WAL: 複数リーダ + 1 ライタ (design.md §4)。
	// busy_timeout: 同時書き込み競合時にすぐ諦めず待つ。
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close は接続を閉じる。
func (s *Store) Close() error { return s.db.Close() }

// Insert は 1 件を追加し、採番された id を返す。created_at はスキーマ既定に委ねる。
// 全フィールドを placeholder バインドで渡す。
func (s *Store) Insert(ctx context.Context, team, from, to, body string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (team, from_agent, to_agent, body) VALUES (?, ?, ?, ?)`,
		team, from, to, body)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// scanRows は messages の SELECT 結果を []Message に詰める共通処理。
func scanRows(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Team, &m.From, &m.To, &m.Body, &m.CreatedAt, &m.ReadAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

const selectCols = `id, team, from_agent, to_agent, body, created_at, read_at`

// Unread は (team, to) 宛ての未読 (read_at IS NULL) を id 昇順で返す（既読化しない）。
func (s *Store) Unread(ctx context.Context, team, to string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+selectCols+` FROM messages
		 WHERE team = ? AND to_agent = ? AND read_at IS NULL
		 ORDER BY id`,
		team, to)
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}

// TakeUnread は (team, to) 宛ての未読を取得し、同時に read_at を埋めて返す。
// inbox サブコマンドの実体。
//
// 取得と既読化を UPDATE ... RETURNING の単一文で原子的に行う。SELECT→UPDATE の
// 2 段だと、同一宛ての inbox が並行実行されたとき両者が同じ行を SELECT して二重
// 取得しうる。UPDATE が WHERE read_at IS NULL で行をその場で claim し、RETURNING で
// 確定分のみ返すことでこの窓を構造的に消す（SQLite は書き込みを直列化するため、
// 後発の inbox は read_at が既に埋まった行を 0 件として返す）。
// RETURNING は順序を保証しないため、id 昇順に整えてから返す。
func (s *Store) TakeUnread(ctx context.Context, team, to, readAt string) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`UPDATE messages SET read_at = ?
		 WHERE team = ? AND to_agent = ? AND read_at IS NULL
		 RETURNING `+selectCols,
		readAt, team, to)
	if err != nil {
		return nil, err
	}
	msgs, err := scanRows(rows)
	if err != nil {
		return nil, err
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	return msgs, nil
}

// MaxID は messages の最大 id を返す（空なら 0）。watch の初期 watermark に使う。
func (s *Store) MaxID(ctx context.Context) (int64, error) {
	var max sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(id) FROM messages`).Scan(&max); err != nil {
		return 0, err
	}
	if !max.Valid {
		return 0, nil
	}
	return max.Int64, nil
}

// Since は (team, to) 宛てで id > watermark の新着を id 昇順で返す (design.md §7/§8.3)。
// watch のポーリング取得に使う。既読化はしない（監視は閲覧であり消費ではない）。
func (s *Store) Since(ctx context.Context, team, to string, watermark int64) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+selectCols+` FROM messages
		 WHERE team = ? AND to_agent = ? AND id > ?
		 ORDER BY id`,
		team, to, watermark)
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}
