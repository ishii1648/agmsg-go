package store

import (
	"context"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "messages.db")
	s, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestPlaceholderSafety は bash 版最大の弱点（手動 SQL エスケープ）を構造的に
// 潰せていることを検証する。SQL を破壊しうる文字列を body に入れても、
// placeholder バインドにより値がそのまま round-trip する。
func TestPlaceholderSafety(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	hostile := []string{
		`'); DROP TABLE messages; --`,
		`O'Brien said "hi"`,
		"line1\nline2\ttabbed",
		`100% done; SELECT * FROM messages WHERE '1'='1'`,
		`back\slash and 'quote'`,
		`日本語のメッセージ 🚀`,
		``, // 空文字
	}

	for _, body := range hostile {
		id, err := s.Insert(ctx, "team1", "alice", "bob", body)
		if err != nil {
			t.Fatalf("Insert(%q): %v", body, err)
		}
		if id == 0 {
			t.Fatalf("Insert(%q): got id 0", body)
		}
	}

	// テーブルが破壊されていない & 全件が原文のまま取れる。
	got, err := s.Unread(ctx, "team1", "bob")
	if err != nil {
		t.Fatalf("Unread: %v", err)
	}
	if len(got) != len(hostile) {
		t.Fatalf("got %d rows, want %d (table may have been corrupted)", len(got), len(hostile))
	}
	for i, m := range got {
		if m.Body != hostile[i] {
			t.Errorf("row %d body mismatch:\n got %q\nwant %q", i, m.Body, hostile[i])
		}
	}
}

// TestTakeUnread は inbox の取得→既読化が機能し、再取得で消えることを検証する。
func TestTakeUnread(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	for _, b := range []string{"m1", "m2", "m3"} {
		if _, err := s.Insert(ctx, "t", "alice", "bob", b); err != nil {
			t.Fatal(err)
		}
	}
	// 別宛ては対象外。
	if _, err := s.Insert(ctx, "t", "alice", "carol", "not-for-bob"); err != nil {
		t.Fatal(err)
	}

	taken, err := s.TakeUnread(ctx, "t", "bob", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("TakeUnread: %v", err)
	}
	if len(taken) != 3 {
		t.Fatalf("took %d, want 3", len(taken))
	}
	for _, m := range taken {
		if !m.ReadAt.Valid {
			t.Errorf("message %d not marked read", m.ID)
		}
	}

	// 既読化済みなので 2 回目は 0 件。
	again, err := s.TakeUnread(ctx, "t", "bob", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second take got %d, want 0", len(again))
	}
}

// TestMaxIDAndSince は watch の watermark 動作を検証する。
// TestTakeUnreadConcurrent は同一宛ての inbox が並行実行されても、各メッセージが
// ちょうど 1 回だけ claim される（二重取得なし）ことを検証する。
func TestTakeUnreadConcurrent(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	const n = 50
	for i := 0; i < n; i++ {
		if _, err := s.Insert(ctx, "t", "alice", "bob", "msg"); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 8
	results := make(chan []Message, workers)
	errs := make(chan error, workers)
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		go func() {
			<-start
			msgs, err := s.TakeUnread(ctx, "t", "bob", "2026-01-01T00:00:00Z")
			if err != nil {
				errs <- err
				return
			}
			results <- msgs
		}()
	}
	close(start)

	seen := map[int64]int{}
	total := 0
	for w := 0; w < workers; w++ {
		select {
		case err := <-errs:
			t.Fatalf("concurrent TakeUnread: %v", err)
		case msgs := <-results:
			for _, m := range msgs {
				seen[m.ID]++
				total++
			}
		}
	}
	if total != n {
		t.Fatalf("claimed %d messages total, want %d", total, n)
	}
	for id, c := range seen {
		if c != 1 {
			t.Errorf("message %d claimed %d times, want exactly 1", id, c)
		}
	}
}

func TestMaxIDAndSince(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()

	if max, err := s.MaxID(ctx); err != nil || max != 0 {
		t.Fatalf("MaxID on empty: got %d, err %v; want 0", max, err)
	}

	id1, _ := s.Insert(ctx, "t", "alice", "bob", "first")
	watermark, err := s.MaxID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if watermark != id1 {
		t.Fatalf("MaxID got %d, want %d", watermark, id1)
	}

	// watermark 後の新着のみが Since で返る。
	s.Insert(ctx, "t", "alice", "bob", "second")
	s.Insert(ctx, "t", "alice", "carol", "other") // 別宛ては除外
	s.Insert(ctx, "t", "alice", "bob", "third")

	got, err := s.Since(ctx, "t", "bob", watermark)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("Since got %d, want 2", len(got))
	}
	if got[0].Body != "second" || got[1].Body != "third" {
		t.Errorf("Since order/content wrong: %q, %q", got[0].Body, got[1].Body)
	}
}
