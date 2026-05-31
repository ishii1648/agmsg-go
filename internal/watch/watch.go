// Package watch は受信検知ループを提供する: 対象ディレクトリを fsnotify で監視し、
// 長めのポーリングを保険として併走させる (design.md §8.2 / §8.3)。
//
// 設計の核は「いつ取得するかを fsnotify が決め、何が新着かは DB が単調増加 id で
// 答える」という分離にある。本パッケージはトリガ（fsnotify イベント or ポーリング）
// の orchestration のみを担い、実際の取得（id > watermark SELECT）は OnTrigger に
// 委ねる。これにより fsnotify が取りこぼしても最大 PollInterval で必ず追いつく。
//
// # WAL を見るための監視対象 (design.md §8.4)
//
// SQLite WAL モードでは書き込みが messages.db 本体ではなく messages.db-wal に乗る。
// そのため「DB ファイル本体だけを監視する」直感的な実装は発火しない。本パッケージは
// *ファイルではなくディレクトリ*を監視対象にすることで、messages.db / -wal / -shm の
// いずれが変化しても発火し、checkpoint による -wal の truncate / 再作成にも追従する。
// 過剰発火（無関係な -shm 変更等）は OnTrigger 側の「SELECT して 0 件」で安全に
// 空振りするだけなので害はない。
package watch

import (
	"context"
	"fmt"
	"time"

	"github.com/fsnotify/fsnotify"
)

// debounceWindow は fsnotify イベントのバーストを 1 回のトリガに畳む時間窓。
// WAL への連続書き込みや checkpoint は短時間に複数イベントを生むため、各イベントで
// 即 SELECT すると無駄が出る。窓内のイベントを 1 回にまとめてから OnTrigger を呼ぶ。
// OnTrigger はべき等なので、この遅延は遅延短縮ではなくアイドル効率のためのもの。
const debounceWindow = 50 * time.Millisecond

// Config は Run の挙動を定める。
type Config struct {
	// Dir は fsnotify で監視するディレクトリ（DB の置かれた db/ ディレクトリ）。
	Dir string

	// PollInterval はポーリング保険の間隔。fsnotify が取りこぼしても最大この時間で
	// 追いつく (design.md §8.3)。0 以下はエラー。
	PollInterval time.Duration

	// DisableFSNotify を true にすると fsnotify を使わずポーリング保険だけで動く。
	// 保険単独での追従性を検証する用途（design.md §8.3 の保険間隔確定）に使う。
	DisableFSNotify bool

	// OnTrigger は各トリガで呼ばれる取得処理。id > watermark の SELECT を撃ち、
	// 新着を出力して watermark を進める。べき等であること（空振り無害）。
	// Run は OnTrigger を単一 goroutine から逐次呼ぶため、watermark 等の共有状態に
	// ロックは要らない。
	OnTrigger func(context.Context) error

	// OnError は致命的でないエラー（fsnotify の初期化失敗・監視中エラー）の通知先。
	// nil なら握りつぶす。fsnotify が使えなくてもポーリング保険で動き続けるため、
	// これらは Run を中断させない。
	OnError func(error)
}

// Run は Dir を監視しつつポーリングを保険併走させ、いずれかのトリガで OnTrigger を
// 呼ぶ。ctx がキャンセルされる（SIGINT 等）までブロックする長命ループ。
//
// 起動直後に一度 OnTrigger を呼び、watermark 確定から監視確立までの隙間に届いた
// 書き込みを取りこぼさない（design.md §8.3「監視を確立する前に発生した書き込み」）。
//
// 正常終了（ctx.Canceled）では nil を返す。fsnotify の初期化や監視に失敗しても
// OnError で通知のうえポーリング保険にフォールバックし、Run は中断しない。
func Run(ctx context.Context, cfg Config) error {
	if cfg.PollInterval <= 0 {
		return fmt.Errorf("watch: PollInterval must be positive, got %v", cfg.PollInterval)
	}
	if cfg.OnTrigger == nil {
		return fmt.Errorf("watch: OnTrigger must not be nil")
	}

	// fsnotify は「主」だが必須ではない。確立に失敗してもポーリング保険で動くため、
	// 初期化エラーは OnError 通知のうえ events/errs を nil のまま進める。
	var events <-chan fsnotify.Event
	var errs <-chan error
	if !cfg.DisableFSNotify {
		w, err := fsnotify.NewWatcher()
		if err != nil {
			cfg.reportError(fmt.Errorf("watch: init fsnotify (falling back to polling): %w", err))
		} else {
			defer w.Close()
			if err := w.Add(cfg.Dir); err != nil {
				cfg.reportError(fmt.Errorf("watch: add %q (falling back to polling): %w", cfg.Dir, err))
			} else {
				events = w.Events
				errs = w.Errors
			}
		}
	}

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	// 起動時同期: watermark 確定〜監視確立の隙間を埋める。
	if err := cfg.OnTrigger(ctx); err != nil {
		return graceful(ctx, err)
	}

	// debounce は arm 中のみ非 nil。fsnotify イベントで張り、発火で OnTrigger を呼ぶ。
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return graceful(ctx, nil)

		case <-ticker.C:
			if err := cfg.OnTrigger(ctx); err != nil {
				return graceful(ctx, err)
			}

		case _, ok := <-events:
			if !ok {
				// watcher が閉じられた。以後はポーリング保険のみで動く。
				events = nil
				continue
			}
			// バーストを畳むため、未 arm のときだけ窓を張る。窓中の後続イベントは
			// この 1 回のトリガに吸収される。
			if debounce == nil {
				debounce = time.After(debounceWindow)
			}

		case <-debounce:
			debounce = nil
			if err := cfg.OnTrigger(ctx); err != nil {
				return graceful(ctx, err)
			}

		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			cfg.reportError(fmt.Errorf("watch: fsnotify error: %w", err))
		}
	}
}

func (cfg Config) reportError(err error) {
	if cfg.OnError != nil {
		cfg.OnError(err)
	}
}

// graceful は ctx キャンセルに伴うエラーを正常終了（nil）に丸める。OnTrigger が
// ctx.Canceled / DeadlineExceeded の最中に失敗した場合もシャットダウンとみなす。
func graceful(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return nil
	}
	return err
}
