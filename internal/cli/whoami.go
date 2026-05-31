package cli

import (
	"context"
	"fmt"

	"github.com/ishii1648/agmsg-go/internal/config"
	"github.com/ishii1648/agmsg-go/internal/paths"
)

// cmdWhoami は現在の (type, project) に一致する登録状態を表示する (design.md §6)。
// 未登録 / 単一 / 複数登録のいずれかを返す。policy（どれを選ぶか）は持たない。
func cmdWhoami(_ context.Context, e Env, l paths.Layout, f flags) error {
	typ, project := f.agentType(), f.project()
	res, err := config.Resolve(l, typ, project)
	if err != nil {
		return err
	}

	fmt.Fprintf(e.Stdout, "type=%s project=%s\n", typ, project)
	if len(res.Matches) == 0 {
		fmt.Fprintln(e.Stdout, "not joined to any team")
		teams, err := config.ListTeams(l)
		if err != nil {
			return err
		}
		if len(teams) > 0 {
			fmt.Fprintf(e.Stdout, "available teams: %v\n", teams)
		}
		return nil
	}
	if id, ok := res.Single(); ok {
		fmt.Fprintf(e.Stdout, "identity: %s\n", id)
		return nil
	}
	fmt.Fprintf(e.Stdout, "%d identities:\n", len(res.Matches))
	for _, id := range res.Matches {
		fmt.Fprintf(e.Stdout, "  - %s\n", id)
	}
	return nil
}
