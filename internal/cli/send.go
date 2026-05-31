package cli

import (
	"context"
	"fmt"

	"github.com/ishii1648/agmsg-go/internal/paths"
)

// cmdSend は `agmsg send <to> <body>` を実装する。
// 送信者 (from, team) は --from/--team または現在の (type, project) から解決する。
// body は placeholder バインドで INSERT され、内容によらず安全 (design.md §5.1)。
func cmdSend(ctx context.Context, e Env, l paths.Layout, f flags) error {
	// ちょうど 2 個（<to> <body>）を要求する。3 個目以降を無言で捨てると
	// `agmsg send bob hello world` が "hello" だけ送るデータ欠落になるため、
	// 余剰はエラーにして本文の引用を促す。
	if len(f.pos) != 2 {
		return fmt.Errorf("usage: agmsg send <to> <body> (quote the body if it contains spaces)")
	}
	to, body := f.pos[0], f.pos[1]

	// --from は --name と同義に扱う（送信者名）。
	if v, ok := f.opts["from"]; ok && v != "" {
		f.opts["name"] = v
	}
	self, err := resolveSelf(l, f)
	if err != nil {
		return err
	}

	s, err := openStore(ctx, l)
	if err != nil {
		return err
	}
	defer s.Close()

	id, err := s.Insert(ctx, self.Team, self.Name, to, body)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Stdout, "sent #%d: %s → %s@%s\n", id, self.Name, to, self.Team)
	return nil
}
