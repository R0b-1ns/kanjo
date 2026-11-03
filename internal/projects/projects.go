// Package projects reads the saved usage of other projects. Their databases
// are untrusted: they are opened read-only, never created, never followed
// through a link, and one that cannot be read never hides the others.
package projects

import (
	"context"
	"errors"
	"io/fs"
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
		rows, sessions, err := ReadStore(filepath.Join(f, DataFolder))
		if err != nil {
			res.Failed = append(res.Failed, name)
			continue
		}
		res.Projects = append(res.Projects, core.ProjectUsage{Name: name, Rows: rows, Sessions: sessions})
	}
	return res
}
