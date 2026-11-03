package main

import (
	"os"

	"github.com/R0b-1ns/kanjo/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
