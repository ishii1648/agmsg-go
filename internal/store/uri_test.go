package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileURIEncoding(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/Users/x/messages.db", "file:/Users/x/messages.db"},   // 通常パスは素通り
		{"/tmp/a?b/db", "file:/tmp/a%3Fb/db"},                    // ? を encode
		{"/tmp/a#b/db", "file:/tmp/a%23b/db"},                    // # を encode
		{"/tmp/100%done/db", "file:/tmp/100%25done/db"},          // % を encode
		{"/日本/db", "file:/日本/db"},                              // UTF-8 は壊さない
	}
	for _, c := range cases {
		if got := fileURI(c.in); got != c.want {
			t.Errorf("fileURI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestOpenWithSpecialCharPath は ? を含むディレクトリでも正しい DB を開けることを
// 実際に検証する（DSN への素朴な埋め込みだと別ファイルを開いてしまう）。
func TestOpenWithSpecialCharPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "weird?dir#1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "messages.db")

	ctx := context.Background()
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open(%q): %v", dbPath, err)
	}
	defer s.Close()

	if _, err := s.Insert(ctx, "t", "a", "b", "hello"); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := s.Unread(ctx, "t", "b")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Body != "hello" {
		t.Fatalf("round-trip failed via special-char path: %+v", got)
	}
}
