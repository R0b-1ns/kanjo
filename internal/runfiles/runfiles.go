// Package runfiles is the core.Artifacts adapter: the private files of each run
// under <data folder>/runs/<session>/. Folders are 0700, files 0600, a name is
// created once, and a symbolic link in the way is refused.
package runfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/R0b-1ns/kanjo/internal/core"
)

const truncatedMarker = "\n[truncated]\n"

// Files implements core.Artifacts.
type Files struct {
	dataDir string
	maxLog  int64
}

// New returns the store of a data folder; logs created with Create are capped
// at maxLog bytes.
func New(dataDir string, maxLog int64) *Files { return &Files{dataDir: dataDir, maxLog: maxLog} }

// folder returns the session folder, created if needed. Neither it nor runs/
// may be a link.
func (f *Files) folder(session string, name core.ArtifactName) (string, error) {
	if !core.ValidSession(session) || !name.Valid() {
		return "", core.ErrBadArtifact
	}
	runs := filepath.Join(f.dataDir, "runs")
	dir := filepath.Join(runs, session)
	for _, d := range []string{runs, dir} {
		if err := realDir(d); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// realDir makes d a real directory (mode 0700) or checks that it is one.
func realDir(d string) error {
	fi, err := os.Lstat(d)
	if errors.Is(err, fs.ErrNotExist) {
		if err = os.Mkdir(d, 0o700); err == nil {
			return nil
		}
		fi, err = os.Lstat(d) // lost a race with another run: check what is there
	}
	switch {
	case err != nil:
		return err
	case !fi.IsDir():
		return fmt.Errorf("%s is not a real directory", filepath.Base(d))
	}
	return nil
}

// Create implements core.Artifacts.
func (f *Files) Create(_ context.Context, session string, name core.ArtifactName) (io.WriteCloser, error) {
	dir, err := f.folder(session, name)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, string(name)), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil, core.ErrArtifactExists
	}
	if err != nil {
		return nil, err
	}
	return &cappedFile{f: file, left: f.maxLog}, nil
}

// Write implements core.Artifacts: the data goes to a temporary file that is
// hard-linked to the final name, so the name appears whole or not at all and
// an existing file is never replaced.
func (f *Files) Write(_ context.Context, session string, name core.ArtifactName, data []byte) error {
	dir, err := f.folder(session, name)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*") // mode 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Close()); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), filepath.Join(dir, string(name))); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return core.ErrArtifactExists
		}
		return err
	}
	return nil
}

// cappedFile drops what is written past left bytes and notes it once. Write
// never fails because of the cap: a command must not be stopped by its log.
type cappedFile struct {
	f    *os.File
	left int64
	cut  bool
}

func (c *cappedFile) Write(p []byte) (int, error) {
	n := int64(len(p))
	if n > c.left {
		p, c.left = p[:c.left], 0
		if !c.cut {
			c.cut = true
			p = append(append([]byte(nil), p...), truncatedMarker...)
		}
	} else {
		c.left -= n
	}
	if _, err := c.f.Write(p); err != nil {
		return 0, err
	}
	return int(n), nil
}

func (c *cappedFile) Close() error { return c.f.Close() }
