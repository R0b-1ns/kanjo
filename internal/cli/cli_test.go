package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"version"}, &out, &errb); code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errb.String())
	}
	if got, want := out.String(), "kanjo "+Version+"\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestRunUnknownCommandExits2(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"nope"}, &out, &errb); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "usage:") || out.Len() != 0 {
		t.Fatalf("stderr %q stdout %q", errb.String(), out.String())
	}
}
