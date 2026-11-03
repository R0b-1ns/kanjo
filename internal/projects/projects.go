// Package projects reads the saved usage of other projects. Their databases
// are untrusted: they are opened read-only, never created, never followed
// through a link, and one that cannot be read never hides the others.
package projects

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/R0b-1ns/kanjo/internal/config"
	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

// DataFolder is the name of a project's data folder inside the project.
const DataFolder = ".ai-usage"

// Result is what Read found. Failed holds the bare names (never paths) of the
// projects that could not be read.
type Result struct {
	Projects []core.ProjectUsage
	Failed   []string
}

// ReadStore reads every saved row and session of one data folder without
// writing anything: a folder with no database yet reads as empty.
func ReadStore(dir string) ([]core.UsageRow, []core.SessionInfo, error) {
	st, err := sqlitestore.OpenReadOnly(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer st.Close()
	rows, err := st.Rows(context.Background())
	if err != nil {
		return nil, nil, err
	}
	sessions, err := st.Sessions(context.Background())
	return rows, sessions, err
}

// Read reads the database of each project folder, in order. The project's own
// kanjo.toml is never read: the data is always <folder>/.ai-usage.
func Read(_ context.Context, folders []string) Result {
	res := Result{Projects: []core.ProjectUsage{}, Failed: []string{}}
	for _, f := range folders {
		name := config.ProjectName(f)
		rows, sessions, err := readProject(f)
		if err != nil {
			res.Failed = append(res.Failed, name)
			continue
		}
		res.Projects = append(res.Projects, core.ProjectUsage{Name: name, Rows: rows, Sessions: sessions})
	}
	return res
}

// readProject reads one listed project. The folder must exist (a typo or a
// moved project is a failure, not an empty project), and its data folder must
// not be a link: the project is untrusted, and a link could point at any
// database. A project that was never snapshotted has no data folder and reads
// as empty.
func readProject(folder string) ([]core.UsageRow, []core.SessionInfo, error) {
	info, err := os.Stat(folder)
	if err != nil {
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a folder", DataFolder)
	}
	data := filepath.Join(folder, DataFolder)
	switch li, err := os.Lstat(data); {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil, nil
	case err != nil:
		return nil, nil, err
	case li.Mode()&os.ModeSymlink != 0:
		return nil, nil, fmt.Errorf("%s is a link", DataFolder)
	}
	return ReadStore(data)
}
