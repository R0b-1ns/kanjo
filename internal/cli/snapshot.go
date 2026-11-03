package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"

	"github.com/R0b-1ns/kanjo/internal/claudecode"
	"github.com/R0b-1ns/kanjo/internal/config"
	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/pricesfile"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

type snapshotOpts struct{ dataDir string }

func snapshotFlags(fs *flag.FlagSet) *snapshotOpts {
	o := &snapshotOpts{}
	fs.StringVar(&o.dataDir, "data-dir", "", "data folder (default: data_dir from the config, ./.ai-usage)")
	return o
}

func snapshotCmd(args []string, stdout, stderr io.Writer, e env) int {
	fs := newFlagSet("snapshot", stderr)
	o := snapshotFlags(fs)
	if code := parseFlags(fs, args); code != proceed {
		return code
	}
	cfg, book, err := setup(e, o.dataDir)
	if err != nil {
		return fail(stderr, err)
	}
	src, err := claudeSource(cfg, book)
	if err != nil {
		return fail(stderr, err)
	}
	st, err := sqlitestore.Open(cfg.DataDir)
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	log, err := core.Snapshot(context.Background(), src, st, e.clock)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "snapshot: %d sessions, %d rows, %d lines skipped -> %s\n", log.Sessions, log.Rows, log.Skipped, printable(sqlitestore.Path(cfg.DataDir)))
	if log.Skipped > 0 {
		fmt.Fprintf(stderr, "warning: %d transcript lines skipped (unreadable or invalid)\n", log.Skipped)
	}
	return 0
}

// claudeSource builds the transcript source from the configuration.
func claudeSource(cfg config.Config, book *pricesfile.Book) (claudecode.Source, error) {
	src := claudecode.Source{
		ProjectsDir:  cfg.Claude.ProjectsDir,
		Prefixes:     cfg.Claude.ProjectPrefixes,
		MaxLineBytes: cfg.Claude.MaxLineBytes,
		LongContext:  book.LongContext,
	}
	if cfg.Task.Pattern != "" {
		re, err := regexp.Compile(cfg.Task.Pattern)
		if err != nil {
			return src, fmt.Errorf("task.pattern: %w", err)
		}
		src.TaskPattern = re
	}
	m, err := readTaskMapFile(cfg.Task.MapFile)
	src.TaskMap = m
	return src, err
}

// readTaskMapFile reads the manual task map; a missing file is an empty map.
func readTaskMapFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := claudecode.ReadTaskMap(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}
