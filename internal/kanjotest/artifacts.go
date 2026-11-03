package kanjotest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"

	"github.com/R0b-1ns/kanjo/internal/core"
)

// MemArtifacts is an in-memory core.Artifacts.
type MemArtifacts struct {
	mu    sync.Mutex
	files map[artKey][]byte
}

type artKey struct {
	session string
	name    core.ArtifactName
}

// NewMemArtifacts returns an empty store.
func NewMemArtifacts() *MemArtifacts { return &MemArtifacts{files: map[artKey][]byte{}} }

func (m *MemArtifacts) claim(session string, name core.ArtifactName) (artKey, error) {
	k := artKey{session, name}
	if !core.ValidSession(session) || !name.Valid() {
		return k, core.ErrBadArtifact
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[k]; ok {
		return k, core.ErrArtifactExists
	}
	m.files[k] = []byte{}
	return k, nil
}

// Create implements core.Artifacts.
func (m *MemArtifacts) Create(_ context.Context, session string, name core.ArtifactName) (io.WriteCloser, error) {
	k, err := m.claim(session, name)
	if err != nil {
		return nil, err
	}
	return &memFile{m: m, k: k}, nil
}

// Write implements core.Artifacts.
func (m *MemArtifacts) Write(_ context.Context, session string, name core.ArtifactName, data []byte) error {
	k, err := m.claim(session, name)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.files[k] = slices.Clone(data)
	return nil
}

// Read returns a file of a run; ok is false when it was not created.
func (m *MemArtifacts) Read(session string, name core.ArtifactName) (data []byte, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.files[artKey{session, name}]
	return slices.Clone(d), ok
}

// All returns every file as "session/name", for canary searches.
func (m *MemArtifacts) All() map[string][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]byte{}
	for k, v := range m.files {
		out[k.session+"/"+string(k.name)] = slices.Clone(v)
	}
	return out
}

type memFile struct {
	m *MemArtifacts
	k artKey
}

func (f *memFile) Write(p []byte) (int, error) {
	f.m.mu.Lock()
	defer f.m.mu.Unlock()
	f.m.files[f.k] = append(f.m.files[f.k], p...)
	return len(p), nil
}

func (f *memFile) Close() error { return nil }

// ArtifactsFixture is what ArtifactsContract needs: the port and a way to read
// back a file it created.
type ArtifactsFixture struct {
	Artifacts core.Artifacts
	Read      func(session string, name core.ArtifactName) (data []byte, ok bool)
}

const artSession = "ses_20261103T120000_a1b2c3"

var artifactsCases = []contractCase[ArtifactsFixture]{
	{"write then read round-trips", func(t *testing.T, fx ArtifactsFixture) {
		if err := fx.Artifacts.Write(context.Background(), artSession, core.ArtReportJSON, []byte(`{"a":1}`)); err != nil {
			t.Fatal(err)
		}
		if got, ok := fx.Read(artSession, core.ArtReportJSON); !ok || string(got) != `{"a":1}` {
			t.Fatalf("got %q, %v", got, ok)
		}
	}},
	{"a created file holds what was streamed", func(t *testing.T, fx ArtifactsFixture) {
		w, err := fx.Artifacts.Create(context.Background(), artSession, core.ArtAgentLog)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, "one ")
		io.WriteString(w, "two")
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if got, ok := fx.Read(artSession, core.ArtAgentLog); !ok || string(got) != "one two" {
			t.Fatalf("got %q, %v", got, ok)
		}
	}},
	{"a name is created once", func(t *testing.T, fx ArtifactsFixture) {
		ctx := context.Background()
		if err := fx.Artifacts.Write(ctx, artSession, core.ArtPatch, []byte("first")); err != nil {
			t.Fatal(err)
		}
		if err := fx.Artifacts.Write(ctx, artSession, core.ArtPatch, []byte("second")); !errors.Is(err, core.ErrArtifactExists) {
			t.Fatalf("Write again: %v", err)
		}
		if _, err := fx.Artifacts.Create(ctx, artSession, core.ArtPatch); !errors.Is(err, core.ErrArtifactExists) {
			t.Fatalf("Create after Write: %v", err)
		}
		if w, err := fx.Artifacts.Create(ctx, artSession, core.ArtTestLog); err != nil {
			t.Fatal(err)
		} else {
			w.Close()
		}
		if err := fx.Artifacts.Write(ctx, artSession, core.ArtTestLog, nil); !errors.Is(err, core.ErrArtifactExists) {
			t.Fatalf("Write after Create: %v", err)
		}
		if got, _ := fx.Read(artSession, core.ArtPatch); string(got) != "first" {
			t.Fatalf("the first content was replaced: %q", got)
		}
	}},
	{"a session that fails ValidSession is refused", func(t *testing.T, fx ArtifactsFixture) {
		for _, s := range []string{"", "../x", "a/b", "-x", "ses 1"} {
			if err := fx.Artifacts.Write(context.Background(), s, core.ArtPatch, nil); !errors.Is(err, core.ErrBadArtifact) {
				t.Errorf("Write(%q): %v", s, err)
			}
			if _, err := fx.Artifacts.Create(context.Background(), s, core.ArtPatch); !errors.Is(err, core.ErrBadArtifact) {
				t.Errorf("Create(%q): %v", s, err)
			}
		}
	}},
	{"names outside the closed set are refused", func(t *testing.T, fx ArtifactsFixture) {
		for _, n := range []core.ArtifactName{"evil.sh", "../report.json", "", "check-0.log", "check-33.log", "check-01.log", "check-1.log/x", "report.json "} {
			if err := fx.Artifacts.Write(context.Background(), artSession, n, nil); !errors.Is(err, core.ErrBadArtifact) {
				t.Errorf("Write(%q): %v", n, err)
			}
		}
	}},
	{"check logs are numbered from 1 and capped at 32", func(t *testing.T, fx ArtifactsFixture) {
		for _, n := range []int{1, 2, core.MaxCheckLogs} {
			if err := fx.Artifacts.Write(context.Background(), artSession, core.ArtCheckLog(n), []byte("x")); err != nil {
				t.Errorf("check %d: %v", n, err)
			}
		}
		if err := fx.Artifacts.Write(context.Background(), artSession, core.ArtCheckLog(core.MaxCheckLogs+1), nil); !errors.Is(err, core.ErrBadArtifact) {
			t.Errorf("check 33: %v", err)
		}
	}},
	{"files of two sessions do not mix", func(t *testing.T, fx ArtifactsFixture) {
		other := "ses_20261103T120001_ffffff"
		fx.Artifacts.Write(context.Background(), artSession, core.ArtPatch, []byte("mine"))
		if err := fx.Artifacts.Write(context.Background(), other, core.ArtPatch, []byte("theirs")); err != nil {
			t.Fatal(err)
		}
		if got, _ := fx.Read(artSession, core.ArtPatch); !bytes.Equal(got, []byte("mine")) {
			t.Fatalf("got %q", got)
		}
	}},
}

// ArtifactsContract checks the behavior every core.Artifacts must have. open
// must return a fresh, empty store.
func ArtifactsContract(t *testing.T, open func(t *testing.T) ArtifactsFixture) {
	runCases(t, artifactsCases, open)
}
