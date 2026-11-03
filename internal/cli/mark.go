package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"

	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/sqlitestore"
)

type markOpts struct{ session, dataDir string }

func markFlags(fs *flag.FlagSet) *markOpts {
	o := &markOpts{}
	fs.StringVar(&o.session, "session", "", "bind the marker to this Claude session id (default: global)")
	dataDirFlag(fs, &o.dataDir)
	return o
}

const markUsage = "usage: kanjo mark start <task> | end | current  [--session ID] [--data-dir DIR]\n"

func markCmd(args []string, stdout, stderr io.Writer, e env) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, markUsage)
		return 2
	}
	sub, args := args[0], args[1:]
	switch sub {
	case "start", "end", "current":
	default:
		fmt.Fprintf(stderr, "kanjo mark: unknown subcommand %q\n%s", sub, markUsage)
		return 2
	}
	flags := newFlagSet("mark "+sub, stderr)
	o := markFlags(flags)
	task, code := parseMarkArgs(flags, sub, args)
	if code != proceed {
		return code
	}
	if err := o.validate(sub, task); err != nil {
		fmt.Fprintf(stderr, "kanjo mark %s: %v\n%s", sub, err, markUsage)
		return 2
	}
	cfg, _, err := setup(e, o.dataDir)
	if err != nil {
		return fail(stderr, err)
	}
	switch sub {
	case "start":
		return markStart(cfg.DataDir, cfg.Markers.MaxEvents, task, o.session, stdout, stderr, e)
	case "end":
		return markEnd(cfg.DataDir, cfg.Markers.MaxEvents, o.session, stdout, stderr, e)
	}
	return markCurrent(cfg.DataDir, o.session, stdout, stderr, e)
}

// parseMarkArgs parses the flags of a subcommand. The task of "start" is its
// one positional argument, before or after the flags.
func parseMarkArgs(flags *flag.FlagSet, sub string, args []string) (task string, code int) {
	if code := parseFlagsLeaving(flags, args); code != proceed {
		return "", code
	}
	rest := flags.Args()
	if sub == "start" && len(rest) > 0 {
		task = rest[0]
		if code := parseFlagsLeaving(flags, rest[1:]); code != proceed {
			return "", code
		}
		rest = flags.Args()
	}
	if len(rest) > 0 {
		fmt.Fprintf(flags.Output(), "kanjo %s: unexpected argument %q\n", flags.Name(), rest[0])
		return "", 2
	}
	return task, proceed
}

// parseFlagsLeaving parses args and leaves the positional ones in flags.Args().
func parseFlagsLeaving(flags *flag.FlagSet, args []string) int {
	switch err := flags.Parse(args); {
	case err == flag.ErrHelp:
		return 0
	case err != nil:
		return 2
	}
	return proceed
}

// validate checks the task and session before any file is touched.
func (o *markOpts) validate(sub, task string) error {
	if sub == "start" && (task == "" || task == core.NoTask || !core.ValidTask(task)) {
		return errors.New("a valid task label is required (letters, digits, . _ - /, at most 64)")
	}
	if o.session != "" && !core.ValidSession(o.session) {
		return errors.New("invalid --session")
	}
	return nil
}

func scopeName(session string) string {
	if session == "" {
		return "global"
	}
	return "session " + session
}

func stamp(m core.Marker) string { return m.At.UTC().Format(core.MarkerTimeLayout) }

func markStart(dir string, maxEvents int, task, session string, stdout, stderr io.Writer, e env) int {
	st, err := sqlitestore.Open(dir)
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	m, started, err := core.StartTask(e.context(), st, e.clock, session, task, maxEvents)
	switch {
	case err != nil:
		return fail(stderr, err)
	case started:
		fmt.Fprintf(stdout, "started %s (%s) at %s\n", task, scopeName(session), stamp(m))
	default:
		fmt.Fprintf(stdout, "%s is already the active task (%s) since %s\n", task, scopeName(session), stamp(m))
	}
	return 0
}

func markEnd(dir string, maxEvents int, session string, stdout, stderr io.Writer, e env) int {
	st, err := sqlitestore.Open(dir)
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	m, err := core.EndTask(e.context(), st, e.clock, session, maxEvents)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "ended the active task (%s) at %s\n", scopeName(session), stamp(m))
	return 0
}

// markCurrent opens the database read-only: asking creates nothing.
func markCurrent(dir, session string, stdout, stderr io.Writer, e env) int {
	st, err := sqlitestore.OpenReadOnly(dir)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(stdout, "no active task")
		return 0
	}
	if err != nil {
		return fail(stderr, err)
	}
	defer st.Close()
	tl, err := timeline(e.context(), st)
	if err != nil {
		return fail(stderr, err)
	}
	m, ok := tl.ActiveAt(session, e.clock.Now())
	if !ok {
		fmt.Fprintln(stdout, "no active task")
		return 0
	}
	fmt.Fprintf(stdout, "%s since %s (%s)\n", printable(m.Task), stamp(m), scopeName(m.Session))
	return 0
}
