// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/GTKottman/skimdit/internal/config"
	"github.com/GTKottman/skimdit/internal/export"
	"github.com/GTKottman/skimdit/internal/importer"
)

func init() {
	extraCommands["import"] = cmdImport
	extraCommands["export"] = cmdExport
}

const importUsage = `usage:
  skimdit import wordpress export.xml   (WordPress: Tools › Export)
  skimdit import ghost export.json      (Ghost: Settings › Labs › Export)
  skimdit import markdown FOLDER        (Hugo, Jekyll, Eleventy or Skimdit content)
flags: --site DIR, --no-download, --images DIR, --base-url URL, --overwrite`

func cmdImport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("skimdit import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	siteDir := fs.String("site", ".", "site folder")
	noDownload := fs.Bool("no-download", false, "do not download images from the old site")
	images := fs.String("images", "", "folder with the old site's images (instead of downloading)")
	baseURL := fs.String("base-url", "", "the old site's address, for relative image links (Ghost: required for __GHOST_URL__)")
	overwrite := fs.Bool("overwrite", false, "replace posts that already exist")
	if len(args) < 2 {
		return usageError{importUsage}
	}
	kind, src := args[0], args[1]
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	cfg, err := config.Load(*siteDir)
	if err != nil {
		return err
	}
	opts := importer.Options{ContentDir: cfg.Path(cfg.Build.Content), Location: cfg.Location, Download: !*noDownload, ImagesDir: *images, BaseURL: *baseURL, Overwrite: *overwrite}
	var rep *importer.Report
	switch kind {
	case "wordpress":
		rep, err = importer.WordPress(src, opts)
	case "ghost":
		rep, err = importer.Ghost(src, opts)
	case "markdown":
		rep, err = importer.Markdown(src, opts)
	default:
		return usageError{importUsage}
	}
	if rep != nil {
		fmt.Fprint(stdout, rep.String())
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Next: run `skimdit check` and fix what it reports, then `skimdit build`.")
	return nil
}

func cmdExport(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("skimdit export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	siteDir := fs.String("site", ".", "site folder")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageError{"usage: skimdit export site-export.zip [--site DIR]"}
	}
	rep, err := export.Run(*siteDir, fs.Arg(0), now())
	if err != nil {
		return err
	}
	db := "no database (the site does not run a server)"
	if rep.DB {
		db = "the database"
	}
	fmt.Fprintf(stdout, "Exported %d files, %d drafts and %s to %s.\n", rep.Files, rep.Drafts, db, fs.Arg(0))
	return nil
}
