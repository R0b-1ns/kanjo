package runfiles_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/kanjotest"
	"github.com/R0b-1ns/kanjo/internal/runfiles"
)

const session = "ses_20261103T120000_a1b2c3"

func TestArtifactsContract(t *testing.T) {
	kanjotest.ArtifactsContract(t, func(t *testing.T) kanjotest.ArtifactsFixture {
		dir := t.TempDir()
		return kanjotest.ArtifactsFixture{
			Artifacts: runfiles.New(dir, 1<<20),
			Read: func(session string, name core.ArtifactName) ([]byte, bool) {
				b, err := os.ReadFile(filepath.Join(dir, "runs", session, string(name)))
				return b, err == nil
			},
		}
	})
}

func TestFilesArePrivate(t *testing.T) {
	dir := t.TempDir()
	f := runfiles.New(dir, 1<<20)
	if err := f.Write(context.Background(), session, core.ArtReportJSON, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	w, err := f.Create(context.Background(), session, core.ArtAgentLog)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	for path, want := range map[string]os.FileMode{
		filepath.Join(dir, "runs"):                         0o700,
		filepath.Join(dir, "runs", session):                0o700,
		filepath.Join(dir, "runs", session, "report.json"): 0o600,
		filepath.Join(dir, "runs", session, "agent.log"):   0o600,
	} {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v, err %v, want %v", path, fi.Mode().Perm(), err, want)
		}
	}
}

func TestLogIsCapped(t *testing.T) {
	dir := t.TempDir()
	f := runfiles.New(dir, 10)
	w, err := f.Create(context.Background(), session, core.ArtAgentLog)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if n, err := io.WriteString(w, "0123456789"); err != nil || n != 10 {
			t.Fatalf("Write = %d, %v (the writer must not fail the command)", n, err)
		}
	}
	w.Close()
	got, _ := os.ReadFile(filepath.Join(dir, "runs", session, "agent.log"))
	if !strings.HasPrefix(string(got), "0123456789") || strings.Count(string(got), "[truncated]") != 1 || len(got) > 10+len("\n[truncated]\n") {
		t.Fatalf("log = %q", got)
	}
}

func TestLogWithinTheCapIsNotMarked(t *testing.T) {
	dir := t.TempDir()
	w, _ := runfiles.New(dir, 10).Create(context.Background(), session, core.ArtAgentLog)
	io.WriteString(w, "0123456789")
	w.Close()
	if got, _ := os.ReadFile(filepath.Join(dir, "runs", session, "agent.log")); string(got) != "0123456789" {
		t.Fatalf("log = %q", got)
	}
}

func TestRefusesALinkedRunsFolder(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(dir, "runs")); err != nil {
		t.Fatal(err)
	}
	err := runfiles.New(dir, 1<<20).Write(context.Background(), session, core.ArtPatch, []byte("x"))
	if err == nil {
		t.Fatal("wrote through a linked runs folder")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("files appeared outside: %v", entries)
	}
}

func TestRefusesALinkedSessionFolder(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	os.Mkdir(filepath.Join(dir, "runs"), 0o700)
	if err := os.Symlink(elsewhere, filepath.Join(dir, "runs", session)); err != nil {
		t.Fatal(err)
	}
	f := runfiles.New(dir, 1<<20)
	if err := f.Write(context.Background(), session, core.ArtPatch, []byte("x")); err == nil {
		t.Fatal("wrote through a linked session folder")
	}
	if _, err := f.Create(context.Background(), session, core.ArtAgentLog); err == nil {
		t.Fatal("created through a linked session folder")
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Fatalf("files appeared outside: %v", entries)
	}
}

func TestRefusesALinkedTargetFile(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(dir, "runs", session), 0o700)
	victim := filepath.Join(elsewhere, "victim")
	os.Symlink(victim, filepath.Join(dir, "runs", session, "agent.log"))
	f := runfiles.New(dir, 1<<20)
	if _, err := f.Create(context.Background(), session, core.ArtAgentLog); err == nil {
		t.Fatal("created through a linked file")
	}
	if err := f.Write(context.Background(), session, core.ArtAgentLog, []byte("x")); err == nil {
		t.Fatal("wrote through a linked file")
	}
	if _, err := os.Lstat(victim); err == nil {
		t.Fatal("the link target was created")
	}
}

func TestWriteIsAtomic(t *testing.T) {
	dir := t.TempDir()
	f := runfiles.New(dir, 1<<20)
	f.Write(context.Background(), session, core.ArtReportJSON, []byte("first"))
	if err := f.Write(context.Background(), session, core.ArtReportJSON, []byte("second")); !errors.Is(err, core.ErrArtifactExists) {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "runs", session))
	if len(entries) != 1 || entries[0].Name() != "report.json" {
		t.Fatalf("leftovers: %v", entries)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "runs", session, "report.json")); string(got) != "first" {
		t.Fatalf("content = %q", got)
	}
}
