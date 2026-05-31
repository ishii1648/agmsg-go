package cli

import (
	"context"
	"fmt"

	"github.com/ishii1648/agmsg-go/internal/paths"
)

// cmdInbox は自分宛ての未読を取得し、取得分を既読化する (design.md §5.1)。
// 取得と既読化は store.TakeUnread が単一トランザクションで行う。
func cmdInbox(ctx context.Context, e Env, l paths.Layout, f flags) error {
	self, err := resolveSelf(l, f)
	if err != nil {
		return err
	}

	s, err := openStore(ctx, l)
	if err != nil {
		return err
	}
	defer s.Close()

	msgs, err := s.TakeUnread(ctx, self.Team, self.Name, e.nowISO())
	if err != nil {
		return err
	}
	if len(msgs) == 0 {
		fmt.Fprintln(e.Stdout, "(no unread messages)")
		return nil
	}
	for _, m := range msgs {
		fmt.Fprintln(e.Stdout, formatMessage(m))
	}
	return nil
}
