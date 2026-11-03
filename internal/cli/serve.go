package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"

	"github.com/R0b-1ns/kanjo/internal/config"
	"github.com/R0b-1ns/kanjo/internal/core"
	"github.com/R0b-1ns/kanjo/internal/projects"
	"github.com/R0b-1ns/kanjo/internal/web"
)

type serveOpts struct {
	port    int
	dataDir string
}

func serveFlags(fs *flag.FlagSet) *serveOpts {
	o := &serveOpts{}
	fs.IntVar(&o.port, "port", 0, "port on 127.0.0.1, 0 picks a free one (default: serve.port from the config, 8787)")
	fs.StringVar(&o.dataDir, "data-dir", "", "data folder (default: data_dir from the config, ./.ai-usage)")
	return o
}

func serveCmd(args []string, stdout, stderr io.Writer, e env) int {
	fs := newFlagSet("serve", stderr)
	o := serveFlags(fs)
	if code := parseFlags(fs, args); code != proceed {
		return code
	}
	cfg, book, err := setup(e, o.dataDir)
	if err != nil {
		return fail(stderr, err)
	}
	port := cfg.Serve.Port
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			port = o.port
		}
	})
	if port < 0 || port > 65535 {
		fmt.Fprintf(stderr, "kanjo serve: invalid --port %d: want 0 to 65535\n", port)
		return 2
	}
	ln, err := web.ListenLoopback(port)
	if err != nil {
		return fail(stderr, err)
	}
	defer ln.Close()
	actual := ln.Addr().(*net.TCPAddr).Port
	fmt.Fprintf(stdout, "serving on http://127.0.0.1:%d/\n", actual)
	if err := web.Serve(e.context(), ln, web.NewHandler(actual, serveLoader(cfg), book)); err != nil {
		return fail(stderr, err)
	}
	return 0
}

// serveLoader reads the saved usage on every request, read-only: this
// project's data folder, or every project listed in the user config.
func serveLoader(cfg config.Config) web.Loader {
	return func(ctx context.Context, scope string) (web.Loaded, error) {
		if scope == "all" {
			res := projects.Read(ctx, cfg.Projects)
			return web.Loaded{Projects: res.Projects, Failed: res.Failed}, nil
		}
		rows, sessions, err := projects.ReadStore(cfg.DataDir)
		if err != nil {
			return web.Loaded{}, err
		}
		return web.Loaded{Projects: []core.ProjectUsage{{Name: "project", Rows: rows, Sessions: sessions}}}, nil
	}
}
