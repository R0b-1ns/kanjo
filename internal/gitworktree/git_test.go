package gitworktree

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestExitCode(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 3").Run()
	if got := exitCode(err); got != 3 {
		t.Errorf("exitCode = %d, want 3", got)
	}
	if got := exitCode(errors.New("not an exit error")); got != -1 {
		t.Errorf("exitCode = %d, want -1", got)
	}
	if got := exitCode(nil); got != -1 {
		t.Errorf("exitCode(nil) = %d, want -1", got)
	}
}

func TestOneLine(t *testing.T) {
	if got := oneLine("  a\nb\x1b[31mc\x7fd\te  "); got != "a b [31mc d e" {
		t.Errorf("oneLine = %q", got)
	}
	if strings.ContainsAny(oneLine("x\r\n\x00y\u009bz"), "\r\n\x00\u009b") {
		t.Error("a control character survived")
	}
}

func TestLimitedKeepsTheFirstBytesAndNeverFails(t *testing.T) {
	var b strings.Builder
	l := &limited{w: &b, left: 4}
	for _, chunk := range []string{"ab", "cdef", "gh"} {
		if n, err := l.Write([]byte(chunk)); n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", chunk, n, err)
		}
	}
	if b.String() != "abcd" {
		t.Fatalf("kept %q", b.String())
	}
}

func TestGitErrorNamesTheCommandAndKeepsOneShortLine(t *testing.T) {
	_, err := git(context.Background(), t.TempDir(), nil, nil, "rev-parse", "--verify", "no-such-thing\nline")
	if err == nil {
		t.Skip("git is not installed or accepted the argument")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, "git rev-parse: ") || strings.ContainsAny(msg, "\n\x1b") {
		t.Fatalf("message = %q", msg)
	}
	if len(msg) > 300+100 {
		t.Fatalf("message is %d bytes", len(msg))
	}
}
