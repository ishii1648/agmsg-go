package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// newEnv は AGMSG_HOME を一時ディレクトリに固定した上で、出力を捕捉する Env を返す。
func newEnv(t *testing.T) (Env, *bytes.Buffer) {
	t.Helper()
	t.Setenv("AGMSG_HOME", t.TempDir())
	// 解決を決定的にするため type / project を固定。
	t.Setenv("AGMSG_TYPE", "claude-code")
	t.Setenv("AGMSG_PROJECT", "/repo/test")
	var out bytes.Buffer
	e := Env{
		Stdout: &out,
		Stderr: &out,
		Now:    func() time.Time { return time.Unix(0, 0).UTC() },
	}
	return e, &out
}

func run(t *testing.T, e Env, args ...string) {
	t.Helper()
	if err := Run(context.Background(), e, args); err != nil {
		t.Fatalf("Run(%v): %v", args, err)
	}
}

// TestSendInboxFlow は join で identity を確立し、send → inbox が成立することを
// 現在の (type, project) からの自動解決のみで通すことを検証する。
func TestSendInboxFlow(t *testing.T) {
	e, out := newEnv(t)

	// alice として alpha チームに参加（以後 send/inbox は自動解決される）。
	run(t, e, "join", "alpha", "alice")

	// alice → bob 宛て。bob 宛てを alice の inbox は受け取らない。
	run(t, e, "send", "bob", "hello bob")
	// SQL を破壊しうる本文も placeholder で安全に通る。
	run(t, e, "send", "alice", `tricky '); DROP TABLE messages; --`)

	out.Reset()
	run(t, e, "inbox") // alice 宛て（= 自分宛て）の未読を取得
	got := out.String()
	if !strings.Contains(got, "tricky '); DROP TABLE messages; --") {
		t.Errorf("inbox missing hostile body:\n%s", got)
	}
	if strings.Contains(got, "hello bob") {
		t.Errorf("inbox should not contain bob's message:\n%s", got)
	}

	// 2 回目の inbox は既読化済みで空。
	out.Reset()
	run(t, e, "inbox")
	if !strings.Contains(out.String(), "no unread") {
		t.Errorf("second inbox should be empty, got:\n%s", out.String())
	}
}

func TestWhoami(t *testing.T) {
	e, out := newEnv(t)

	run(t, e, "whoami")
	if !strings.Contains(out.String(), "not joined") {
		t.Errorf("whoami before join should report not joined:\n%s", out.String())
	}

	run(t, e, "join", "alpha", "alice")
	out.Reset()
	run(t, e, "whoami")
	if !strings.Contains(out.String(), "alice@alpha") {
		t.Errorf("whoami after join should report identity:\n%s", out.String())
	}
}

func TestLeaveStopsResolution(t *testing.T) {
	e, _ := newEnv(t)
	run(t, e, "join", "alpha", "alice")
	run(t, e, "leave", "alpha", "alice")

	// 解決できなくなるため send は明示フラグなしでは失敗する。
	err := Run(context.Background(), e, []string{"send", "bob", "x"})
	if err == nil {
		t.Error("send after leave should fail to resolve identity")
	}
}

// TestSendAmbiguousIdentity は複数 team に同条件で参加した場合、自動解決が
// 曖昧になり send が（フラグなしで）エラーになることを検証する (design.md §6)。
func TestSendAmbiguousIdentity(t *testing.T) {
	e, _ := newEnv(t)
	run(t, e, "join", "alpha", "alice")
	run(t, e, "join", "beta", "alice")

	if err := Run(context.Background(), e, []string{"send", "bob", "x"}); err == nil {
		t.Error("ambiguous identity should error without --team")
	}

	// --team で絞れば成功する。
	run(t, e, "send", "bob", "x", "--team", "beta")
}

func TestUnknownSubcommand(t *testing.T) {
	e, _ := newEnv(t)
	if err := Run(context.Background(), e, []string{"frobnicate"}); err == nil {
		t.Error("unknown subcommand should error")
	}
}
