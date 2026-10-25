// SPDX-License-Identifier: AGPL-3.0-or-later

// Command skimdit is the Skimdit blog engine.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // timezones work in minimal containers

	"github.com/GTKottman/skimdit/internal/check"
	"github.com/GTKottman/skimdit/internal/config"
	"github.com/GTKottman/skimdit/internal/dev"
	"github.com/GTKottman/skimdit/internal/scaffold"
	"github.com/GTKottman/skimdit/internal/site"
	"github.com/GTKottman/skimdit/internal/version"
)

const usage = `Skimdit: a blog engine.

Usage:
  skimdit <command> [flags]

Commands:
  init [dir]        Create a new site (default: the current folder)
  new "Title"       Create a draft post (--page for a page)
  dev               Preview the site with live reload (drafts shown)
  build             Build the site into the output folder (dist/)
  check             Build into a scratch folder and report problems
  version           Print the version
  help              Show this help

Every command takes --site DIR (default: the current folder).
Run "skimdit <command> -h" for a command's flags.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// now is replaceable in tests.
var now = time.Now

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version.String())
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "init":
		err = cmdInit(rest, stdout)
	case "new":
		err = cmdNew(rest, stdout)
	case "build":
		err = cmdBuild(rest, stdout, stderr)
	case "check":
		return cmdCheck(rest, stdout, stderr)
	case "dev":
		err = cmdDev(rest, stderr)
	default:
		if c, ok := extraCommands[cmd]; ok {
			err = c(rest, stdout, stderr)
			break
		}
		fmt.Fprintf(stderr, "skimdit: unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		var ue usageError
		if errors.As(err, &ue) {
			fmt.Fprintf(stderr, "skimdit %s: %s\n", cmd, ue.msg)
			return 2
		}
		fmt.Fprintf(stderr, "skimdit %s: %v\n", cmd, err)
		return 1
	}
	return 0
}

// extraCommands are added by later milestones (serve, import, export, ...).
var extraCommands = map[string]func(args []string, stdout, stderr io.Writer) error{}

type usageError struct{ msg string }

func (u usageError) Error() string { return u.msg }

func flags(name string, out io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("skimdit "+name, flag.ContinueOnError)
	fs.SetOutput(out)
	siteDir := fs.String("site", ".", "site folder (holds config.toml)")
	return fs, siteDir
}

func cmdInit(args []string, stdout io.Writer) error {
	fs, _ := flags("init", stdout)
	title := fs.String("title", "", "site title")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := scaffold.Init(dir, *title, now()); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Created a new site in %s.\nNext: edit config.toml, then run:\n  cd %s && skimdit dev\n", dir, dir)
	return nil
}

func cmdNew(args []string, stdout io.Writer) error {
	fs, siteDir := flags("new", stdout)
	page := fs.Bool("page", false, "create a page instead of a post")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageError{`needs a title: skimdit new "My post"`}
	}
	cfg, err := config.Load(*siteDir)
	if err != nil {
		return err
	}
	file, err := scaffold.NewPost(cfg.Root, cfg.Build.Content, fs.Arg(0), *page, now(), cfg.Location)
	if err != nil {
		return err
	}
	rel, _ := filepath.Rel(cfg.Root, file)
	fmt.Fprintf(stdout, "Created %s (status: draft). Set status: published when it is ready.\n", rel)
	return nil
}

// reorder lets flags come after positional arguments: new "Title" --page.
func reorder(args []string) []string {
	var flagsOut, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") && a != "-" {
			flagsOut = append(flagsOut, a)
			if !strings.Contains(a, "=") && i+1 < len(args) && (a == "--site" || a == "-site" || a == "--title" || a == "-title" || a == "--out" || a == "-out") {
				flagsOut = append(flagsOut, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flagsOut, pos...)
}

func printDiags(w io.Writer, res *site.Result) {
	if res == nil {
		return
	}
	for _, d := range res.Diags {
		kind := "error"
		if d.Warning {
			kind = "warning"
		}
		fmt.Fprintf(w, "%s: %s\n", kind, d.Error())
	}
}

func cmdBuild(args []string, stdout, stderr io.Writer) error {
	fs, siteDir := flags("build", stderr)
	out := fs.String("out", "", "output folder (default: build.output from config, usually dist)")
	drafts := fs.Bool("drafts", false, "include drafts and scheduled posts (never for production)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	res, err := site.Run(site.Options{Root: *siteDir, Out: *out, Drafts: *drafts, Now: now(), RecordURLs: !*drafts && *out == ""})
	printDiags(stderr, res)
	if err != nil {
		if errors.Is(err, site.ErrBuild) {
			return errors.New("fix the errors above and build again")
		}
		return err
	}
	fmt.Fprintf(stdout, "Built %d entries, %d files into %s in %s.\n", res.Entries, len(res.Files), res.Out, res.Duration.Round(time.Millisecond))
	return nil
}

func cmdCheck(args []string, stdout, stderr io.Writer) int {
	fs, siteDir := flags("check", stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	rep, err := check.Run(*siteDir, now())
	if err != nil {
		fmt.Fprintf(stderr, "skimdit check: %v\n", err)
		return 1
	}
	errs, warns := 0, 0
	for _, d := range rep.Diags {
		kind := "error"
		if d.Warning {
			kind = "warning"
			warns++
		} else {
			errs++
		}
		fmt.Fprintf(stdout, "%s: %s\n", kind, d.Error())
	}
	if errs > 0 {
		fmt.Fprintf(stdout, "check failed: %d error(s), %d warning(s)\n", errs, warns)
		return 1
	}
	fmt.Fprintf(stdout, "check passed: %d pages, %d warning(s), built in %s\n", rep.Pages, warns, rep.Duration.Round(time.Millisecond))
	return 0
}

func cmdDev(args []string, stderr io.Writer) error {
	fs, siteDir := flags("dev", stderr)
	addr := fs.String("addr", "127.0.0.1:1414", "address to listen on")
	noDrafts := fs.Bool("no-drafts", false, "hide drafts and scheduled posts")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := config.Load(*siteDir); err != nil {
		return err
	}
	abs, err := filepath.Abs(*siteDir)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return dev.Run(ctx, dev.Options{Root: abs, Addr: *addr, Drafts: !*noDrafts, Log: log.New(stderr, "", log.Ltime)})
}
