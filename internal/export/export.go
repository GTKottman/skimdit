// SPDX-License-Identifier: AGPL-3.0-or-later

// Package export writes everything a site is into one zip file: the
// Markdown content and settings, unpublished drafts as Markdown, and a
// copy of the database (comments, subscribers, stats). Unzipped, it is a
// working site folder.
package export

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/GTKottman/skimdit/internal/config"
	"github.com/GTKottman/skimdit/internal/content"
	"github.com/GTKottman/skimdit/internal/store"
)

// Report says what went into the archive.
type Report struct {
	Files  int
	Drafts int
	DB     bool
}

// siteParts are the files and folders that make up a site.
var siteParts = []string{"config.toml", "redirects.toml", "published-urls.txt", "static", "theme", "directives", "locales", ".gitignore"}

// Run writes the export of the site at root to out (a .zip file).
func Run(root, out string, now time.Time) (*Report, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	f, err := os.Create(out)
	if err != nil {
		return nil, err
	}
	zw := zip.NewWriter(f)
	rep := &Report{}
	add := func(name string, r io.Reader, mod time.Time) error {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: mod}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, r)
		rep.Files++
		return err
	}
	addTree := func(rel string) error {
		full := filepath.Join(cfg.Root, rel)
		st, err := os.Stat(full)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !st.IsDir() {
			fh, err := os.Open(full)
			if err != nil {
				return err
			}
			defer fh.Close()
			return add(filepath.ToSlash(rel), fh, st.ModTime())
		}
		return filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			r, _ := filepath.Rel(cfg.Root, p)
			info, err := d.Info()
			if err != nil {
				return err
			}
			fh, err := os.Open(p)
			if err != nil {
				return err
			}
			defer fh.Close()
			return add(filepath.ToSlash(r), fh, info.ModTime())
		})
	}
	parts := append([]string{cfg.Build.Content}, siteParts...)
	for _, p := range cfg.Plugins {
		parts = append(parts, p.Path)
	}
	if cfg.Theme.Name != "" && cfg.Theme.Name != "default" && !filepath.IsAbs(cfg.Theme.Name) {
		parts = append(parts, cfg.Theme.Name)
	}
	for _, p := range parts {
		if err := addTree(p); err != nil {
			zw.Close()
			f.Close()
			return nil, err
		}
	}

	// Drafts and the database, when the site runs a server.
	dataDir := cfg.Path(cfg.Server.Data)
	if _, err := os.Stat(filepath.Join(dataDir, store.FileName)); err == nil {
		st, err := store.Open(dataDir)
		if err != nil {
			zw.Close()
			f.Close()
			return nil, err
		}
		defer st.Close()
		drafts, _ := st.Drafts()
		for _, d := range drafts {
			var fields content.Fields
			_ = json.Unmarshal([]byte(d.Fields), &fields)
			text, err := content.FileText(fields, d.Body)
			if err != nil {
				continue
			}
			slug := firstNonEmpty(fields.Slug, content.Slugify(fields.Title), "untitled")
			name := fmt.Sprintf("drafts/%d-%s/index.md", d.ID, slug)
			if err := add(name, strings.NewReader(text), time.Unix(d.UpdatedAt, 0)); err != nil {
				return nil, err
			}
			rep.Drafts++
			up := filepath.Join(dataDir, "uploads", fmt.Sprint(d.ID))
			entries, _ := os.ReadDir(up)
			for _, e := range entries {
				fh, err := os.Open(filepath.Join(up, e.Name()))
				if err != nil {
					continue
				}
				_ = add(path.Join(path.Dir(name), e.Name()), fh, now)
				fh.Close()
			}
		}
		tmp, err := os.MkdirTemp("", "skimdit-export-")
		if err == nil {
			defer os.RemoveAll(tmp)
			copyPath := filepath.Join(tmp, store.FileName)
			if _, err := st.DB.Exec(`VACUUM INTO ?`, copyPath); err == nil {
				if fh, err := os.Open(copyPath); err == nil {
					_ = add("data/"+store.FileName, fh, now)
					fh.Close()
					rep.DB = true
				}
			}
		}
	}
	readme := "This is a complete export of a Skimdit site (" + cfg.Site.URL + "), made " + now.UTC().Format(time.RFC3339) + ".\n\n" +
		"- Unzip it and run `skimdit build` or `skimdit dev` inside: it is a working site folder.\n" +
		"- drafts/ holds unpublished drafts as Markdown; move one into content/posts/ to publish it.\n" +
		"- data/skimdit.db holds comments, subscribers (personal data: keep it safe) and stats. Put it in the server's data folder to restore them.\n"
	if err := add("EXPORT.md", strings.NewReader(readme), now); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return nil, err
	}
	return rep, f.Close()
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
