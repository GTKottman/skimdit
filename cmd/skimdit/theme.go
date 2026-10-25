// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/GTKottman/skimdit/internal/themecheck"
)

func init() {
	extraCommands["theme"] = cmdTheme
}

func cmdTheme(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "check" {
		return usageError{"usage: skimdit theme check [theme-folder] [--out DIR]"}
	}
	fs := flag.NewFlagSet("skimdit theme check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "", "keep the built sample site in this folder")
	if err := fs.Parse(reorder(args[1:])); err != nil {
		return err
	}
	dir := fs.Arg(0)
	rep, err := themecheck.Run(dir, *out)
	if err != nil {
		return err
	}
	for _, d := range rep.Diags {
		fmt.Fprintf(stdout, "error: %s\n", d.Error())
	}
	name := dir
	if name == "" {
		name = "the built-in theme"
	}
	if !rep.OK() {
		return fmt.Errorf("%s does not meet the theme contract (%d problems)", name, len(rep.Diags))
	}
	fmt.Fprintf(stdout, "%s meets the theme contract (%d sample pages built and checked).\n", name, rep.Pages)
	return nil
}
