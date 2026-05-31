package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/ishii1648/agmsg-go/internal/paths"
	"github.com/ishii1648/agmsg-go/internal/watch"
)

// defaultPollInterval はポーリング保険の既定間隔 (design.md §8.3)。
// 受信検知の主は fsnotify（DB ディレクトリ監視）で、典型遅延は実質即時。ポーリングは
// fsnotify が取りこぼした場合に最大この時間で追いつくための保険なので、案 A の単純
// ポーリング（5 秒）より大幅に長く取りアイドルコストを抑える。0002 の実機検証で
// 「fsnotify 主・30 秒保険」がアイドル効率と取りこぼし追従のバランスとして妥当と確認。
const defaultPollInterval = 30 * time.Second

// cmdWatch は自分宛ての id > watermark を stream する (design.md §7/§8)。
// 起動時の MAX(id) を watermark とし、新着のみを 1 行ずつ出力する。
// 受信検知は internal/watch（fsnotify によるディレクトリ監視 + ポーリング保険）に委ね、
// 本関数は watermark 管理と出力整形だけを持つ。
// ctx がキャンセルされる（SIGINT 等）まで動き続ける長命プロセス。
func cmdWatch(ctx context.Context, e Env, l paths.Layout, f flags) error {
	self, err := resolveSelf(l, f)
	if err != nil {
		return err
	}

	interval := defaultPollInterval
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

	// onTrigger は fsnotify / ポーリングのどちらでトリガされても同じ取得経路を通る。
	// watch.Run が単一 goroutine から逐次呼ぶため、watermark への閉包アクセスは安全。
	onTrigger := func(ctx context.Context) error {
		msgs, err := s.Since(ctx, self.Team, self.Name, watermark)
		if err != nil {
			return err
		}
		for _, m := range msgs {
			fmt.Fprintln(e.Stdout, formatMessage(m))
			if m.ID > watermark {
				watermark = m.ID
			}
		}
		return nil
	}

	return watch.Run(ctx, watch.Config{
		Dir:          l.DBDir(),
		PollInterval: interval,
		OnTrigger:    onTrigger,
		// fsnotify の非致命エラーは stderr に出すが watch は止めない（ポーリング保険が
		// 動き続ける）。閲覧ストリーム本体（stdout）と混ざらないよう stderr に書く。
		OnError: func(err error) { fmt.Fprintln(e.Stderr, err) },
	})
}
