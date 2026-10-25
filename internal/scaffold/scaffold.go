// SPDX-License-Identifier: AGPL-3.0-or-later

// Package scaffold creates new sites and posts.
package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GTKottman/skimdit/internal/content"
)

const configTemplate = `# Skimdit site configuration. Every key is documented in docs/CONFIG.md.

[site]
title = %q
url = "https://example.com"
description = "Writing about things worth thinking about."
language = "en"
timezone = "UTC"
intro = "Hello! This is a new Skimdit blog."

[author]
name = "Your Name"
# email = "you@example.com"   # shows "Reply by email" under posts

[comments]
# Comments, the newsletter and stats need ` + "`skimdit serve`" + ` running.
enabled = false

[stats]
enabled = false
`

const helloPost = `---
title: Hello, world
date: %s
tags: [meta]
---

This is your first post. Edit it in ` + "`content/posts/hello-world/index.md`" + `, or delete it.

## What you can write

Markdown with tables, footnotes,[^1] code, math like $e^{i\pi} + 1 = 0$, and callouts:

:::callout{type=tip title="Tip"}
Run ` + "`skimdit dev`" + ` and this page reloads every time you save.
:::

[^1]: Footnotes look like this.
`

const aboutPage = `---
title: About
---

Tell readers who you are and what this blog is about.
`

const gitignore = `/dist/
/.skimdit/
`

// Init creates a new site in dir. It refuses to overwrite a config.
func Init(dir, title string, now time.Time) error {
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); err == nil {
		return fmt.Errorf("%s already has a config.toml", dir)
	}
	if title == "" {
		title = "My Blog"
	}
	files := map[string]string{
		"config.toml":                        fmt.Sprintf(configTemplate, title),
		"content/posts/hello-world/index.md": fmt.Sprintf(helloPost, now.UTC().Format("2006-01-02")),
		"content/pages/about.md":             aboutPage,
		".gitignore":                         gitignore,
		"static/.gitkeep":                    "",
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if _, err := os.Stat(p); err == nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// NewPost creates a draft post (or page) folder and returns its index file.
func NewPost(root, contentDir, title string, page bool, now time.Time, loc *time.Location) (string, error) {
	slug := content.Slugify(title)
	if slug == "" {
		return "", errors.New("the title needs at least one letter or digit")
	}
	kind := "posts"
	if page {
		kind = "pages"
	}
	dir := filepath.Join(root, contentDir, kind, slug)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("%s already exists", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlString(title))
	if !page {
		fmt.Fprintf(&b, "date: %s\n", now.In(loc).Format("2006-01-02T15:04:05-07:00"))
		b.WriteString("tags: []\n")
	}
	b.WriteString("status: draft\n---\n\n")
	file := filepath.Join(dir, "index.md")
	return file, os.WriteFile(file, []byte(b.String()), 0o644)
}

func yamlString(s string) string {
	if strings.ContainsAny(s, ":#'\"{}[]&*!|>%@`") || strings.TrimSpace(s) != s {
		return "\"" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + "\""
	}
	return s
}
