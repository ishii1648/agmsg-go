package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ishii1648/agmsg-go/internal/paths"
)

// defaultWatchInterval はポーリング間隔の既定値。
// Tier 1 は単純ポーリングで実装する。fsnotify によるイベント駆動 + ポーリング保険は
// 別 issue 0002 で載せ替える (design.md §8 / issue 0001 対応方針)。
const defaultWatchInterval = 2 * time.Second

// cmdWatch は自分宛ての id > watermark を stream する (design.md §7/§8.3)。
// 起動時の MAX(id) を watermark とし、新着のみを 1 行ずつ出力する。
// ctx がキャンセルされる（SIGINT 等）まで動き続ける長命プロセス。
func cmdWatch(ctx context.Context, e Env, l paths.Layout, f flags) error {
	self, err := resolveSelf(l, f)
	if err != nil {
		return err
	}

	interval := defaultWatchInterval
	if v := f.get("interval", "", ""); v != "" {
		d, perr := time.ParseDuration(v)
		if perr != nil {
			return fmt.Errorf("invalid --interval %q: %w", v, perr)
		}
		// time.NewTicker は d <= 0 で panic するため、CLI エラーとして弾く。
		if d <= 0 {
			return fmt.Errorf("invalid --interval %q: must be positive", v)
		}
		interval = d
	}

	s, err := openStore(ctx, l)
	if err != nil {
		return err
	}
	defer s.Close()

	// 起動時点までの既存メッセージは「過去」として watermark に畳む。
	watermark, err := s.MaxID(ctx)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// SIGINT 等での正常終了。Canceled はエラー扱いしない。
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return ctx.Err()
		case <-ticker.C:
			msgs, err := s.Since(ctx, self.Team, self.Name, watermark)
			if err != nil {
				// ctx キャンセルに伴う失敗は握りつぶす。
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
			for _, m := range msgs {
				fmt.Fprintln(e.Stdout, formatMessage(m))
				if m.ID > watermark {
					watermark = m.ID
				}
			}
		}
	}
}
