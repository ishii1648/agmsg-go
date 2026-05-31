package cli

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuffer は watch goroutine と test goroutine からの並行アクセスを直列化する
// bytes.Buffer ラッパ。watch は別 goroutine で Stdout に書くため必要。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestWatchStreamsNewMessage は「送信 → watch が即時 stream する」エンドツーエンドの
// 発火を検証する (issue 0002 / design.md §8)。watch を起動後に send し、新着が
// watch の出力へ流れることを確認する。fsnotify とポーリング保険のどちらが先に拾っても
// よいよう、--interval を短くして両経路を併走させる。
func TestWatchStreamsNewMessage(t *testing.T) {
	t.Setenv("AGMSG_HOME", t.TempDir())
	t.Setenv("AGMSG_TYPE", "claude-code")
	t.Setenv("AGMSG_PROJECT", "/repo/test")

	out := &syncBuffer{}
	e := Env{
		Stdout: out,
		Stderr: out,
		Now:    func() time.Time { return time.Unix(0, 0).UTC() },
	}

	// alice として参加（以後 watch/send は自動解決）。
	run(t, e, "join", "alpha", "alice")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		// 短い保険間隔で fsnotify 取りこぼし時もすぐ追いつく。
		done <- Run(ctx, e, []string{"watch", "--interval", "100ms"})
	}()

	// watch が MaxID で watermark を確定するまで待ってから send する。
	// （早すぎると新着が watermark に畳まれ「過去」扱いになる。）
	time.Sleep(200 * time.Millisecond)

	// 別の Env（出力を混ぜない）から alice 宛てに送る。
	sendEnv := Env{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Now: e.Now}
	run(t, sendEnv, "send", "alice", "ping from test")

	// 最大 ~2 秒で watch の出力に現れること。
	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(out.String(), "ping from test") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("watch did not stream the new message; got:\n%s", out.String())
		case <-time.After(20 * time.Millisecond):
		}
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("watch should exit cleanly on cancel, got %v", err)
	}
}
