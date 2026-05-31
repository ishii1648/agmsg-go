package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// errSentinel は OnTrigger エラー伝播の検証に使うダミー。
var errSentinel = errors.New("sentinel")

// runWatch は Run を goroutine で起動し、各トリガを通知する channel を返す。
// triggers には OnTrigger が呼ばれるたびに 1 が送られる（バッファ付きで取りこぼさない）。
// done は Run が返ったら閉じられ、戻り値の error を errp に書く。
func runWatch(t *testing.T, cfg Config) (triggers <-chan struct{}, cancel context.CancelFunc, wait func() error) {
	t.Helper()
	ch := make(chan struct{}, 1024)
	inner := cfg.OnTrigger
	cfg.OnTrigger = func(ctx context.Context) error {
		ch <- struct{}{}
		if inner != nil {
			return inner(ctx)
		}
		return nil
	}
	ctx, cancelFn := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- Run(ctx, cfg) }()
	return ch, cancelFn, func() error { return <-errCh }
}

// waitTrigger は timeout 以内にトリガが来れば true。来なければ false。
func waitTrigger(triggers <-chan struct{}, timeout time.Duration) bool {
	select {
	case <-triggers:
		return true
	case <-time.After(timeout):
		return false
	}
}

// TestInitialTrigger は起動直後に一度 OnTrigger が呼ばれ、watermark 確定〜監視確立の
// 隙間に届いた書き込みを取りこぼさないことを検証する (design.md §8.3)。
func TestInitialTrigger(t *testing.T) {
	triggers, cancel, wait := runWatch(t, Config{
		Dir:          t.TempDir(),
		PollInterval: time.Hour, // ポーリングもイベントも無くても初回は呼ばれる
		OnTrigger:    func(context.Context) error { return nil },
	})
	defer wait()
	defer cancel()

	if !waitTrigger(triggers, time.Second) {
		t.Fatal("OnTrigger should fire once at startup")
	}
}

// TestPollingInsurance は fsnotify を無効化した状態で、ポーリング保険だけで
// 周期的に追従することを検証する (design.md §8.3 / 保険間隔の妥当性確認)。
func TestPollingInsurance(t *testing.T) {
	triggers, cancel, wait := runWatch(t, Config{
		Dir:             t.TempDir(),
		PollInterval:    20 * time.Millisecond,
		DisableFSNotify: true,
		OnTrigger:       func(context.Context) error { return nil },
	})
	defer wait()
	defer cancel()

	// 初回 + ポーリング由来で複数回来るはず。
	for i := 0; i < 3; i++ {
		if !waitTrigger(triggers, time.Second) {
			t.Fatalf("polling insurance should fire repeatedly; missed trigger %d", i)
		}
	}
}

// TestFSNotifyFiresOnDirChange はディレクトリ内のファイル変更で fsnotify が
// （ポーリングを待たずに）即時発火することを検証する。CI の OS matrix で
// Linux/macOS/Windows それぞれの実機挙動を再現する (design.md §8.2)。
func TestFSNotifyFiresOnDirChange(t *testing.T) {
	dir := t.TempDir()
	triggers, cancel, wait := runWatch(t, Config{
		Dir:          dir,
		PollInterval: time.Hour, // 発火は fsnotify 由来であることを保証
		OnTrigger:    func(context.Context) error { return nil },
	})
	defer wait()
	defer cancel()

	// 起動時トリガを消費。
	if !waitTrigger(triggers, time.Second) {
		t.Fatal("missing startup trigger")
	}
	// 監視確立の取りこぼしを避けるため少し待ってから書き込む。
	time.Sleep(50 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(dir, "messages.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitTrigger(triggers, 2*time.Second) {
		t.Fatal("fsnotify should fire on directory change before the (1h) poll")
	}
}

// TestFSNotifyFiresOnWALFile は WAL モードの肝（design.md §8.4）を直接検証する:
// 書き込みは messages.db 本体ではなく messages.db-wal に乗るため、ディレクトリ監視が
// -wal の変化を確実に拾えなければ「届いているのに気づかない」取りこぼしになる。
func TestFSNotifyFiresOnWALFile(t *testing.T) {
	dir := t.TempDir()
	wal := filepath.Join(dir, "messages.db-wal")
	// 監視確立前に -wal を作っておき、以降の追記（=実書き込み相当）で発火するか見る。
	if err := os.WriteFile(wal, []byte("init"), 0o644); err != nil {
		t.Fatal(err)
	}

	triggers, cancel, wait := runWatch(t, Config{
		Dir:          dir,
		PollInterval: time.Hour,
		OnTrigger:    func(context.Context) error { return nil },
	})
	defer wait()
	defer cancel()

	if !waitTrigger(triggers, time.Second) {
		t.Fatal("missing startup trigger")
	}
	time.Sleep(50 * time.Millisecond)

	f, err := os.OpenFile(wal, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("append"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if !waitTrigger(triggers, 2*time.Second) {
		t.Fatal("directory watch must fire on messages.db-wal change (design.md §8.4)")
	}
}

// TestContextCancelReturnsNil は SIGINT 相当の ctx キャンセルが正常終了
// （nil 返却）になることを検証する。
func TestContextCancelReturnsNil(t *testing.T) {
	triggers, cancel, wait := runWatch(t, Config{
		Dir:          t.TempDir(),
		PollInterval: time.Hour,
		OnTrigger:    func(context.Context) error { return nil },
	})
	if !waitTrigger(triggers, time.Second) {
		t.Fatal("missing startup trigger")
	}
	cancel()
	if err := wait(); err != nil {
		t.Errorf("ctx cancel should return nil, got %v", err)
	}
}

// TestStartupTriggerErrorPropagates は起動時同期で OnTrigger が（ctx 健在のまま）
// 失敗したら、その error がそのまま返ることを検証する。
func TestStartupTriggerErrorPropagates(t *testing.T) {
	wantErr := errSentinel
	err := Run(context.Background(), Config{
		Dir:          t.TempDir(),
		PollInterval: time.Hour,
		OnTrigger:    func(context.Context) error { return wantErr },
	})
	if err != wantErr {
		t.Errorf("startup OnTrigger error should propagate, got %v", err)
	}
}

// TestRejectsBadConfig は不正な Config を弾くことを検証する。
func TestRejectsBadConfig(t *testing.T) {
	if err := Run(context.Background(), Config{Dir: t.TempDir(), PollInterval: 0, OnTrigger: func(context.Context) error { return nil }}); err == nil {
		t.Error("non-positive PollInterval must error")
	}
	if err := Run(context.Background(), Config{Dir: t.TempDir(), PollInterval: time.Second}); err == nil {
		t.Error("nil OnTrigger must error")
	}
}

// TestDegradesWhenDirMissing は監視対象ディレクトリが存在しなくても、fsnotify の
// Add 失敗を OnError で通知しつつポーリング保険で動き続けることを検証する。
func TestDegradesWhenDirMissing(t *testing.T) {
	var errCount atomic.Int32
	triggers, cancel, wait := runWatch(t, Config{
		Dir:          filepath.Join(t.TempDir(), "does-not-exist"),
		PollInterval: 20 * time.Millisecond,
		OnTrigger:    func(context.Context) error { return nil },
		OnError:      func(error) { errCount.Add(1) },
	})
	defer wait()
	defer cancel()

	// fsnotify が張れなくてもポーリングは回る。
	for i := 0; i < 2; i++ {
		if !waitTrigger(triggers, time.Second) {
			t.Fatalf("polling must keep firing even when fsnotify add fails; missed %d", i)
		}
	}
	if errCount.Load() == 0 {
		t.Error("missing directory should be reported via OnError")
	}
}
