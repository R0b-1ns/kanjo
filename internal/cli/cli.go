// Package cli wires the adapters to the core and implements the commands.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"unicode"

	"github.com/R0b-1ns/kanjo/internal/config"
	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/pricesfile"
	"github.com/R0b-1ns/kanjo/internal/selfupdate"
	"github.com/R0b-1ns/kanjo/internal/sysclock"
)

// Version is set at build time with -ldflags -X.
var Version = "dev"

// env is everything a command reads from the machine, injected for tests.
type env struct {
	home, cwd, xdg string
	clock          core.Clock
	update         selfupdate.Updater
	ctx            context.Context // ends serve and run in tests; nil means only a signal ends them
	ports          *delegatePorts  // replaces the workspace, sandbox and agent of run in tests
}

func (e env) context() context.Context {
	if e.ctx == nil {
		return context.Background()
	}
	return e.ctx
}

type command func(args []string, stdout, stderr io.Writer, e env) int

var commands = map[string]command{
	"mark":        markCmd,
	"prices":      pricesCmd,
	"run":         runCmd,
	"self-update": selfUpdateCmd,
	"serve":       serveCmd,
	"snapshot":    snapshotCmd,
	"usage":       usageCmd,
	"version": func(_ []string, stdout, _ io.Writer, _ env) int {
		fmt.Fprintf(stdout, "kanjo %s\n", Version)
		return 0
	},
}

func usageText() string {
	return "usage: kanjo <command> [options]\ncommands: " + strings.Join(slices.Sorted(maps.Keys(commands)), ", ") + "\n"
}

// Run executes one command and returns the process exit code
// (0 ok, 1 error, 2 usage error).
func Run(args []string, stdout, stderr io.Writer) int {
	home, err := os.UserHomeDir()
	if err != nil {
		return fail(stderr, fmt.Errorf("cannot find the home folder: %w", err))
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail(stderr, fmt.Errorf("cannot find the working folder: %w", err))
	}
	return run(args, stdout, stderr, env{
		home: home, cwd: cwd, xdg: os.Getenv("XDG_CONFIG_HOME"), clock: sysclock.Clock{},
		update: productionUpdater(runningBinary()),
	})
}

func run(args []string, stdout, stderr io.Writer, e env) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText())
		return 2
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "kanjo: unknown command %q\n%s", args[0], usageText())
		return 2
	}
	return cmd(args[1:], stdout, stderr, e)
}

// setup is the configuration and price book every report or snapshot needs.
func setup(e env, dataDir string) (config.Config, *pricesfile.Book, error) {
	cfg, err := config.Load(e.home, e.cwd, e.xdg, config.Overrides{DataDir: dataDir})
	if err != nil {
		return cfg, nil, err
	}
	book, err := pricesfile.Load(cfg.PricesFile)
	return cfg, book, err
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "kanjo: %s\n", printable(err.Error()))
	return 1
}

// printable replaces every character a terminal could act on (escape
// sequences, bell, newlines) with "?". Paths come from config files and may
// carry them.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return '?'
	}, s)
}

// proceed is what parseFlags returns when the command should go on.
const proceed = -1

// parseFlags parses args into fs. It returns proceed, or the exit code the
// command must stop with (help requested, bad flag, or stray argument).
func parseFlags(fs *flag.FlagSet, args []string) int {
	if code := parseFlagsLeaving(fs, args); code != proceed {
		return code
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(fs.Output(), "kanjo %s: unexpected argument %q\n", fs.Name(), fs.Arg(0))
		return 2
	}
	return proceed
}

// parseFlagsLeaving parses args and leaves the positional ones in fs.Args().
func parseFlagsLeaving(fs *flag.FlagSet, args []string) int {
	switch err := fs.Parse(args); {
	case err == flag.ErrHelp:
		return 0
	case err != nil:
		return 2
	}
	return proceed
}

// dataDirFlag registers --data-dir, shared by every command that reads data.
func dataDirFlag(fs *flag.FlagSet, dir *string) {
	fs.StringVar(dir, "data-dir", "", "data folder (default: data_dir from the config, ./.ai-usage)")
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}
