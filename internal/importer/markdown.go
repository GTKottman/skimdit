// SPDX-License-Identifier: AGPL-3.0-or-later

package importer

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/GTKottman/skimdit/internal/content"
	"go.yaml.in/yaml/v3"
)

var jekyllName = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})-(.+)$`)

// Markdown imports a folder of Markdown files with YAML (---) or TOML (+++)
// frontmatter: Hugo, Jekyll, Eleventy or another Skimdit site. Files in
// folders named pages/ (or with type: page) become pages.
func Markdown(dir string, opts Options) (*Report, error) {
	rep := &Report{}
	var posts []*Post
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); p != dir && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "public" || name == "_site" || name == "dist") {
				return filepath.SkipDir
			}
			return nil
		}
		if ext := strings.ToLower(filepath.Ext(p)); ext != ".md" && ext != ".markdown" {
			return nil
		}
		post, err := readMarkdown(p, dir, opts, rep)
		if err != nil {
			rep.warn("%s: %v (skipped)", p, err)
			rep.Skipped++
			return nil
		}
		if post != nil {
			posts = append(posts, post)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rep, write(posts, opts, rep)
}

func readMarkdown(file, root string, opts Options, rep *Report) (*Post, error) {
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	meta := map[string]any{}
	body := src
	switch {
	case bytes.HasPrefix(src, []byte("---\n")):
		end := bytes.Index(src[4:], []byte("\n---"))
		if end < 0 {
			return nil, fmt.Errorf("frontmatter is not closed")
		}
		if err := yaml.Unmarshal(src[4:4+end], &meta); err != nil {
			return nil, fmt.Errorf("frontmatter: %v", err)
		}
		body = src[4+end+4:]
	case bytes.HasPrefix(src, []byte("+++\n")):
		end := bytes.Index(src[4:], []byte("\n+++"))
		if end < 0 {
			return nil, fmt.Errorf("frontmatter is not closed")
		}
		if _, err := toml.Decode(string(src[4:4+end]), &meta); err != nil {
			return nil, fmt.Errorf("frontmatter: %v", err)
		}
		body = src[4+end+4:]
	}
	rel, _ := filepath.Rel(root, file)
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	bundle := base == "index" || base == "_index"
	if bundle {
		base = filepath.Base(filepath.Dir(file))
		if base == "." || filepath.Dir(file) == root {
			return nil, nil // a section index, not a post
		}
	}
	p := &Post{Kind: content.KindPost, From: rel, Markdown: strings.TrimLeft(string(body), "\n")}
	f := &p.Fields
	str := func(keys ...string) string {
		for _, k := range keys {
			switch v := meta[k].(type) {
			case string:
				if strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
			case time.Time:
				return v.Format(time.RFC3339)
			}
		}
		return ""
	}
	list := func(keys ...string) []string {
		var out []string
		for _, k := range keys {
			switch v := meta[k].(type) {
			case []any:
				for _, x := range v {
					if s, ok := x.(string); ok {
						out = append(out, s)
					}
				}
			case string:
				for _, s := range strings.Split(v, ",") {
					out = append(out, strings.TrimSpace(s))
				}
			}
		}
		return out
	}
	f.Title = str("title")
	if m := jekyllName.FindStringSubmatch(base); m != nil {
		base = m[2]
		if str("date") == "" {
			meta["date"] = m[1]
		}
	}
	f.Slug = firstNonEmpty(str("slug"), base)
	if f.Title == "" {
		f.Title = strings.ReplaceAll(base, "-", " ")
		rep.warn("%s: no title; used the file name", rel)
	}
	if strings.Contains(filepath.ToSlash(rel), "pages/") || str("type", "layout") == "page" {
		p.Kind = content.KindPage
	}
	if d := str("date", "publishDate", "pubDate"); d != "" && p.Kind == content.KindPost {
		t, err := content.ParseDate(d, opts.Location)
		if err != nil {
			if t2, err2 := time.Parse(time.RFC3339, d); err2 == nil {
				t = t2
			} else {
				return nil, err
			}
		}
		f.Date = dateString(t, opts.Location)
	} else if p.Kind == content.KindPost {
		st, _ := os.Stat(file)
		f.Date = dateString(st.ModTime(), opts.Location)
		rep.warn("%s: no date; used the file's modification time", rel)
	}
	if u := str("lastmod", "updated", "modified"); u != "" {
		if t, err := content.ParseDate(u, opts.Location); err == nil {
			f.Updated = dateString(t, opts.Location)
		}
	}
	if v, ok := meta["draft"].(bool); ok && v {
		f.Status = "draft"
	}
	if v, ok := meta["published"].(bool); ok && !v {
		f.Status = "draft"
	}
	if st := str("status"); st == "draft" || st == "unlisted" || st == "scheduled" {
		f.Status = st
	}
	f.Tags = uniqueSorted(append(list("tags"), list("categories")...))
	if s := list("series"); len(s) > 0 {
		f.Series = s[0]
	}
	f.Excerpt = str("excerpt", "summary")
	f.Description = str("description")
	f.Canonical = str("canonical", "canonical_url", "canonicalURL")
	f.Cover = str("cover", "image", "featured_image", "featureImage", "cover_image")
	f.CoverAlt = str("cover_alt", "image_alt", "imageAlt")
	aliases := list("aliases", "redirect_from")
	if pl := str("url", "permalink"); pl != "" && strings.HasPrefix(pl, "/") {
		aliases = append(aliases, pl)
	}
	f.Aliases = uniqueSorted(aliases)
	extra := map[string]any{}
	known := map[string]bool{"title": true, "date": true, "publishDate": true, "pubDate": true, "slug": true, "draft": true, "published": true,
		"status": true, "tags": true, "categories": true, "series": true, "excerpt": true, "summary": true, "description": true, "canonical": true,
		"canonical_url": true, "canonicalURL": true, "cover": true, "image": true, "featured_image": true, "featureImage": true, "cover_image": true,
		"cover_alt": true, "image_alt": true, "imageAlt": true, "aliases": true, "redirect_from": true, "url": true, "permalink": true,
		"lastmod": true, "updated": true, "modified": true, "layout": true, "type": true}
	for k, v := range meta {
		if !known[k] {
			extra[k] = v
		}
	}
	if len(extra) > 0 {
		f.Extra = extra
	}
	if strings.Contains(p.Markdown, "{{<") || strings.Contains(p.Markdown, "{{%") || strings.Contains(p.Markdown, "{%") {
		rep.warn("%s: contains template shortcodes ({{< >}} or {%% %%}); rewrite them as Skimdit directives", rel)
	}
	// Page bundles bring their files along; local image paths stay valid.
	if bundle {
		p.Files = map[string]string{}
		entries, _ := os.ReadDir(filepath.Dir(file))
		for _, e := range entries {
			if !e.IsDir() && e.Name() != filepath.Base(file) {
				p.Files[e.Name()] = filepath.Join(filepath.Dir(file), e.Name())
			}
		}
	}
	return p, nil
}
