// SPDX-License-Identifier: AGPL-3.0-or-later

// Command skimdit is the Skimdit blog engine.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/GTKottman/skimdit/internal/version"
)

const usage = `Skimdit: a blog engine.

Usage:
  skimdit <command> [arguments]

Commands:
  version   Print the version
  help      Show this help

More commands (init, new, dev, build, check, serve, ...) arrive with milestone M1 and later.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version.String())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "skimdit: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
