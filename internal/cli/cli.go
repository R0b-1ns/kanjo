// Package cli wires the adapters to the core and implements the commands.
package cli

import (
	"fmt"
	"io"
)

// Version is set at build time with -ldflags -X.
var Version = "dev"

const usageText = "usage: kanjo <command> [options]\ncommands: version\n"

// Run executes one command and returns the process exit code
// (0 ok, 1 error, 2 usage error).
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "kanjo %s\n", Version)
		return 0
	default:
		fmt.Fprintf(stderr, "kanjo: unknown command %q\n%s", args[0], usageText)
		return 2
	}
}
