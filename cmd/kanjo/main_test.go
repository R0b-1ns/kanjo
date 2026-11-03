package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestMain lets the test binary act as the kanjo binary when asked, so the
// real main function and its exit code can be checked.
func TestMain(m *testing.M) {
	if os.Getenv("KANJO_TEST_AS_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

func runMain(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "KANJO_TEST_AS_MAIN=1")
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), out.String(), errb.String()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, out.String(), errb.String()
}

func TestMainExitsWithTheCommandsCode(t *testing.T) {
	if code, out, _ := runMain(t, "version"); code != 0 || out != "kanjo dev\n" {
		t.Fatalf("version: code %d stdout %q", code, out)
	}
	if code, out, errb := runMain(t, "nope"); code != 2 || out != "" || !strings.Contains(errb, "unknown command") {
		t.Fatalf("unknown command: code %d stdout %q stderr %q", code, out, errb)
	}
}
