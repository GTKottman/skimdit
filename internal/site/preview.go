// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/GTKottman/skimdit/internal/content"
	"github.com/GTKottman/skimdit/internal/markdown"
)

// Preview renders one draft through the theme, using this build's site data.
// Relative files resolve to fileDir (uploads) or the published post folder,
// and are linked under fileURL.
func (b *Build) Preview(src []byte, kind content.Kind, slug, source, fileDir, fileURL string) ([]byte, error) {
	it, diags := content.Parse(src, "draft", kind, slug, b.Config.Location)
	if diags.HasErrors() {
		return nil, diags
	}
	if source != "" && (strings.HasSuffix(source, "/index.md") || strings.HasSuffix(source, "/index.html")) {
		it.Dir = filepath.Dir(filepath.Join(b.Config.Root, filepath.FromSlash(source)))
	}
	it.Source = "draft"
	e := &Entry{
		Kind: string(it.Kind), Title: it.Title, Slug: it.Slug, Date: it.Date.In(b.Config.Location), Updated: it.Updated,
		Status: "draft", Lang: b.Config.Site.Language, Extra: it.Extra, Source: "draft", Description: it.Description, item: it,
	}
	pattern := b.Config.Permalinks.Post
	if it.Kind == content.KindPage {
		pattern = b.Config.Permalinks.Page
	}
	e.URL = b.permalink(pattern, it.Slug, e.Date)
	e.Permalink = b.Config.AbsURL(e.URL)
	exists := func(name string) bool {
		for _, dir := range []string{fileDir, it.Dir} {
			if dir == "" {
				continue
			}
			if st, err := os.Stat(filepath.Join(dir, name)); err == nil && !st.IsDir() {
				return true
			}
		}
		return false
	}
	var problems []string
	hooks := markdown.Hooks{
		Link: func(dest string) (string, error) {
			switch {
			case strings.HasPrefix(dest, "post:") || strings.HasPrefix(dest, "page:"):
				k, rest, _ := strings.Cut(dest, ":")
				s, frag, _ := strings.Cut(rest, "#")
				t := b.bySlug[k+":"+s]
				if t == nil {
					return dest, fmt.Errorf("link %q: there is no published %s with slug %q", dest, k, s)
				}
				if frag != "" {
					return t.URL + "#" + frag, nil
				}
				return t.URL, nil
			case strings.HasPrefix(dest, "#") || strings.HasPrefix(dest, "/") || hasScheme(dest) || dest == "":
				return dest, nil
			}
			clean := path.Clean(dest)
			if exists(clean) {
				return fileURL + clean, nil
			}
			return dest, nil
		},
		Image: func(src string) (*markdown.Image, error) {
			if strings.HasPrefix(src, "/") {
				return &markdown.Image{Src: b.Config.Rel(src)}, nil
			}
			clean := path.Clean(src)
			if !exists(clean) {
				return nil, errors.New("file not found (upload it first)")
			}
			return &markdown.Image{Src: fileURL + clean}, nil
		},
		Directive: b.directiveHook(),
	}
	doc, err := b.md.Render(it.Body, hooks)
	if err != nil {
		return nil, err
	}
	for _, d := range doc.Diags {
		if !d.Warning {
			problems = append(problems, fmt.Sprintf("line %d: %s", it.BodyLine+d.Line-1, d.Message))
		}
	}
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	e.doc = doc
	e.Content = template.HTML(doc.HTML)
	e.Words = doc.Words
	e.ReadingTime = int(math.Max(1, math.Ceil(float64(doc.Words)/230)))
	e.HasMath, e.HasCode, e.HasEmbed = doc.HasMath, doc.HasCode, doc.HasEmbed
	e.Excerpt = firstNonEmpty(it.Excerpt, doc.Excerpt)
	for _, h := range doc.Headings {
		if h.Level >= 2 && h.Level <= 4 {
			e.TOC = append(e.TOC, h)
		}
	}
	e.ShowTOC = len(e.TOC) >= b.Config.Markdown.TOCMinHeadings
	if it.TOC != nil {
		e.ShowTOC = *it.TOC && len(e.TOC) > 0
	}
	for _, name := range it.Tags {
		e.Tags = append(e.Tags, &Term{Name: name, Slug: content.TagSlug(name), URL: b.permalink(b.Config.Permalinks.Tag, content.TagSlug(name), e.Date)})
	}
	if it.Cover != "" && exists(path.Clean(it.Cover)) {
		e.Cover = &ImageData{HTML: template.HTML(markdown.PictureHTML(&markdown.Image{Src: fileURL + path.Clean(it.Cover)}, it.CoverAlt, true)), Alt: it.CoverAlt}
	}
	p := b.page(e.Kind, e.Title, e.URL)
	p.Entry = e
	p.NoIndex = true
	p.Description = e.Description
	tmpl := "post"
	if e.Kind == "page" {
		tmpl = "page"
	}
	var buf bytes.Buffer
	if err := b.Theme.Render(&buf, tmpl, p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
