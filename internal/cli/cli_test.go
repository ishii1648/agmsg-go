package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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

// TestSendDashDashBody は "--" sentinel で "--" 始まりの本文を送れることを検証する。
// body は不透明な TEXT なので、本文の先頭文字列で送信不能になってはならない。
func TestSendDashDashBody(t *testing.T) {
	e, out := newEnv(t)
	run(t, e, "join", "alpha", "alice")
	run(t, e, "send", "alice", "--", "--json={\"k\":1}")

	out.Reset()
	run(t, e, "inbox")
	if !strings.Contains(out.String(), `--json={"k":1}`) {
		t.Errorf("body starting with -- should be deliverable, got:\n%s", out.String())
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

// TestFlagRequiresValue は値必須フラグの値欠落が env/default へフォールバックして
// 誤送信になる footgun を防ぐ（値なし --team はエラー）。
func TestFlagRequiresValue(t *testing.T) {
	t.Setenv("AGMSG_TEAM", "alpha") // フォールバック先があっても化けないこと
	e, _ := newEnv(t)
	run(t, e, "join", "alpha", "alice")
	if err := Run(context.Background(), e, []string{"send", "bob", "hi", "--team"}); err == nil {
		t.Error("value-less --team must error, not fall back to AGMSG_TEAM")
	}
	// 末尾でなく別フラグが続く場合も値欠落として弾く。
	if err := Run(context.Background(), e, []string{"send", "bob", "hi", "--team", "--from", "alice"}); err == nil {
		t.Error("--team followed by another flag must error")
	}
}

// TestUnknownFlagRejected はフラグ typo の黙殺による誤送信を防ぐ。
func TestUnknownFlagRejected(t *testing.T) {
	t.Setenv("AGMSG_TEAM", "alpha")
	e, _ := newEnv(t)
	run(t, e, "join", "alpha", "alice")
	// `--teem` は `--team` の typo。黙って無視して alpha 宛てにせず、エラーにする。
	if err := Run(context.Background(), e, []string{"send", "bob", "hi", "--teem", "beta"}); err == nil {
		t.Error("unknown flag --teem must error, not be silently ignored")
	}
	// 正しいフラグは通る。
	run(t, e, "send", "bob", "hi", "--team", "alpha")

	// サブコマンドごとに許可集合が異なる: whoami は --team を受理しない。
	if err := Run(context.Background(), e, []string{"whoami", "--team", "alpha"}); err == nil {
		t.Error("whoami should reject --team")
	}
}

func TestUnknownSubcommand(t *testing.T) {
	e, _ := newEnv(t)
	if err := Run(context.Background(), e, []string{"frobnicate"}); err == nil {
		t.Error("unknown subcommand should error")
	}
}

// TestSendRejectsExtraArgs は本文の暗黙破棄（データ欠落）を防ぐため、
// 位置引数がちょうど 2 個でないと send が失敗することを検証する。
func TestSendRejectsExtraArgs(t *testing.T) {
	e, _ := newEnv(t)
	run(t, e, "join", "alpha", "alice")
	if err := Run(context.Background(), e, []string{"send", "bob", "hello", "world"}); err == nil {
		t.Error("send with 3 positional args should error (avoid silent body truncation)")
	}
	if err := Run(context.Background(), e, []string{"send", "bob"}); err == nil {
		t.Error("send with missing body should error")
	}
}

// TestWatchRejectsNonPositiveInterval は time.NewTicker の panic を防ぐ。
func TestWatchRejectsNonPositiveInterval(t *testing.T) {
	e, _ := newEnv(t)
	run(t, e, "join", "alpha", "alice")
	for _, v := range []string{"0", "-1s", "garbage"} {
		if err := Run(context.Background(), e, []string{"watch", "--interval", v}); err == nil {
			t.Errorf("watch --interval %q should error", v)
		}
	}
}

// TestSkillsInstall は埋め込み skills を dest へ展開し、*.sh が実行可能で
// SKILL.md が同梱されることを検証する（go:embed → install の結線確認）。
func TestSkillsInstall(t *testing.T) {
	e, out := newEnv(t)
	dest := t.TempDir()
	run(t, e, "skills", "install", "--dest", dest)

	for _, rel := range []string{
		"dispatch/dispatch.sh", "dispatch/SKILL.md",
		"review-loop/review-loop.sh", "review-loop/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Errorf("expected installed file %s: %v", rel, err)
		}
	}
	if fi, err := os.Stat(filepath.Join(dest, "dispatch", "dispatch.sh")); err == nil {
		if fi.Mode().Perm()&0o111 == 0 {
			t.Errorf("dispatch.sh should be executable, got %v", fi.Mode().Perm())
		}
	}
	if !strings.Contains(out.String(), "installed skills to "+dest) {
		t.Errorf("missing install summary:\n%s", out.String())
	}
}

// TestSkillsForceFlag は値を取らない --force が parse エラーにならず、
// 再 install で既存ファイルを上書きすることを検証する。
func TestSkillsForceFlag(t *testing.T) {
	e, _ := newEnv(t)
	dest := t.TempDir()
	run(t, e, "skills", "install", "--dest", dest)
	target := filepath.Join(dest, "dispatch", "SKILL.md")
	if err := os.WriteFile(target, []byte("EDIT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, e, "skills", "install", "--dest", dest, "--force")
	got, _ := os.ReadFile(target)
	if string(got) == "EDIT\n" {
		t.Error("--force should have overwritten the edited file")
	}
}

// TestSkillsUnknownAction は未知アクションがエラーになることを検証する。
func TestSkillsUnknownAction(t *testing.T) {
	e, _ := newEnv(t)
	if err := Run(context.Background(), e, []string{"skills", "frobnicate"}); err == nil {
		t.Error("unknown skills action should error")
	}
	if err := Run(context.Background(), e, []string{"skills"}); err == nil {
		t.Error("skills without action should error")
	}
}

// TestInboxEscapesNewlines は改行入り本文が 1 行に収まる（レコード境界が壊れない）
// ことを検証する。
func TestInboxEscapesNewlines(t *testing.T) {
	e, out := newEnv(t)
	run(t, e, "join", "alpha", "alice")
	run(t, e, "send", "alice", "line1\nline2\rcarriage")

	out.Reset()
	run(t, e, "inbox")
	got := strings.TrimRight(out.String(), "\n")
	if strings.Contains(got, "\n") {
		t.Errorf("inbox output must be a single line, got:\n%q", got)
	}
	if !strings.Contains(got, `line1\nline2\rcarriage`) {
		t.Errorf("newlines should be escaped in output, got:\n%q", got)
	}
}
