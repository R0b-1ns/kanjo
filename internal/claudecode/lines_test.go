package claudecode

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func collect(t *testing.T, input string, max int) ([]string, int) {
	t.Helper()
	var got []string
	over, err := ForEachLine(strings.NewReader(input), max, func(b []byte) { got = append(got, string(b)) })
	if err != nil {
		t.Fatalf("ForEachLine: %v", err)
	}
	return got, over
}

func eq(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") && len(a) == len(b) }

func TestForEachLineDeliversLines(t *testing.T) {
	got, over := collect(t, "one\ntwo\n\nthree", 100)
	if want := []string{"one", "two", "three"}; !eq(got, want) || over != 0 {
		t.Fatalf("got %q over %d, want %q (blank lines skipped, last line without newline kept)", got, over, want)
	}
}

func TestForEachLineDiscardsOversizedAndContinues(t *testing.T) {
	got, over := collect(t, "ok\n"+strings.Repeat("x", 11)+"\nnext\n", 10)
	if want := []string{"ok", "next"}; !eq(got, want) || over != 1 {
		t.Fatalf("got %q over %d", got, over)
	}
}

func TestForEachLineAcceptsExactlyMax(t *testing.T) {
	line := strings.Repeat("y", 10)
	if got, over := collect(t, line+"\n", 10); !eq(got, []string{line}) || over != 0 {
		t.Fatalf("got %q over %d", got, over)
	}
}

func TestForEachLineCountsOversizedLastLineWithoutNewline(t *testing.T) {
	if got, over := collect(t, "ok\n"+strings.Repeat("z", 50), 10); !eq(got, []string{"ok"}) || over != 1 {
		t.Fatalf("got %q over %d", got, over)
	}
}

func TestForEachLineJoinsLongLinesUnderMax(t *testing.T) {
	long := strings.Repeat("a", 150_000) // longer than the internal read buffer
	if got, over := collect(t, long+"\nb\n", 200_000); !eq(got, []string{long, "b"}) || over != 0 {
		t.Fatalf("lines = %d over %d", len(got), over)
	}
}

func TestForEachLineDiscardsHugeLineAcrossBuffers(t *testing.T) {
	if got, over := collect(t, strings.Repeat("a", 300_000)+"\nb\n", 100_000); !eq(got, []string{"b"}) || over != 1 {
		t.Fatalf("got %d lines over %d", len(got), over)
	}
}

type failingReader struct{ err error }

func (f failingReader) Read([]byte) (int, error) { return 0, f.err }

func TestForEachLinePropagatesReadError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := ForEachLine(failingReader{boom}, 10, func([]byte) {}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := ForEachLine(io.MultiReader(strings.NewReader("a\n"), failingReader{boom}), 10, func([]byte) {}); !errors.Is(err, boom) {
		t.Fatalf("err after data = %v", err)
	}
}
