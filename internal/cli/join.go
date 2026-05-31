package cli

import (
	"context"
	"fmt"

	"github.com/ishii1648/agmsg-go/internal/config"
	"github.com/ishii1648/agmsg-go/internal/identity"
	"github.com/ishii1648/agmsg-go/internal/paths"
)

// joinArgs は join / leave 共通の引数解決。
//
//	agmsg join <team> <name>   （name は位置引数 or --name / AGMSG_NAME）
//
// design.md §10 の表記は `join <team>` だが、(name, team) が同一性キーである以上
// name は必須。位置引数・フラグ・env のいずれでも与えられるようにする。
func joinArgs(f flags) (team, name string, reg identity.Registration, err error) {
	if len(f.pos) < 1 {
		return "", "", identity.Registration{}, fmt.Errorf("missing <team>")
	}
	team = f.pos[0]
	if len(f.pos) >= 2 {
		name = f.pos[1]
	} else {
		name = f.get("name", envName, "")
	}
	if name == "" {
		return "", "", identity.Registration{}, fmt.Errorf("missing <name> (positional, --name, or AGMSG_NAME)")
	}
	reg = identity.Registration{Type: f.agentType(), Project: f.project()}
	return team, name, reg, nil
}

func cmdJoin(_ context.Context, e Env, l paths.Layout, f flags) error {
	team, name, reg, err := joinArgs(f)
	if err != nil {
		return err
	}
	added, err := config.Join(l, team, name, reg)
	if err != nil {
		return err
	}
	id := identity.Identity{Name: name, Team: team}
	if added {
		fmt.Fprintf(e.Stdout, "joined: %s (type=%s, project=%s)\n", id, reg.Type, reg.Project)
	} else {
		fmt.Fprintf(e.Stdout, "already joined: %s (type=%s, project=%s)\n", id, reg.Type, reg.Project)
	}
	return nil
}

func cmdLeave(_ context.Context, e Env, l paths.Layout, f flags) error {
	team, name, reg, err := joinArgs(f)
	if err != nil {
		return err
	}
	removed, err := config.Leave(l, team, name, reg)
	if err != nil {
		return err
	}
	id := identity.Identity{Name: name, Team: team}
	if removed {
		fmt.Fprintf(e.Stdout, "left: %s (type=%s, project=%s)\n", id, reg.Type, reg.Project)
	} else {
		fmt.Fprintf(e.Stdout, "not registered: %s (type=%s, project=%s)\n", id, reg.Type, reg.Project)
	}
	return nil
}
