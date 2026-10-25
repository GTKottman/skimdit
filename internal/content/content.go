// SPDX-License-Identifier: AGPL-3.0-or-later

// Package content loads posts and pages from a site's content folder and
// validates their frontmatter.
package content

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the frontmatter schema this engine writes and reads.
// Files with a newer schema_version need a newer engine.
const SchemaVersion = 1

// Kind is a post (dated, in feeds) or a page (undated).
type Kind string

const (
	KindPost Kind = "post"
	KindPage Kind = "page"
)

// Status says who may see an item.
type Status string

const (
	StatusDraft     Status = "draft"
	StatusPublished Status = "published"
	StatusScheduled Status = "scheduled"
	StatusUnlisted  Status = "unlisted"
)

// Format is the body format.
type Format string

const (
	FormatMarkdown Format = "markdown"
	FormatHTML     Format = "html"
)

// Item is one post or page as read from disk.
type Item struct {
	Kind   Kind
	Format Format
	// Source is the file path relative to the site root, with forward slashes.
	Source string
	// Dir is the absolute folder that holds co-located files ("" for a lone file).
	Dir string
	// Body is the content after the frontmatter.
	Body []byte
	// BodyLine is the 1-based line in Source where Body starts.
	BodyLine int

	Title       string
	Slug        string
	Date        time.Time
	Updated     time.Time
	Status      Status
	Tags        []string
	Series      string
	SeriesOrder int
	Excerpt     string
	Description string
	Cover       string
	CoverAlt    string
	SocialImage string
	Aliases     []string
	Canonical   string
	Lang        string
	TOC         *bool
	Comments    *bool
	Schema      int
	Extra       map[string]any
}

// Visible reports whether the item is public at time now.
func (it *Item) Visible(now time.Time) bool {
	switch it.Status {
	case StatusPublished, StatusUnlisted, StatusScheduled:
		return it.Kind == KindPage || !it.Date.After(now)
	}
	return false
}

// Listed reports whether the item appears in lists, feeds, the sitemap and search.
func (it *Item) Listed(now time.Time) bool {
	return it.Visible(now) && it.Status != StatusUnlisted
}

// Diagnostic is one problem found in a file.
type Diagnostic struct {
	File    string
	Line    int
	Message string
	Warning bool
}

func (d Diagnostic) Error() string {
	if d.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", d.File, d.Line, d.Message)
	}
	if d.File != "" {
		return fmt.Sprintf("%s: %s", d.File, d.Message)
	}
	return d.Message
}

// Diagnostics is a list of problems; it is an error when any is not a warning.
type Diagnostics []Diagnostic

func (ds Diagnostics) Error() string {
	var b strings.Builder
	for i, d := range ds {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(d.Error())
	}
	return b.String()
}

// HasErrors reports whether any diagnostic is an error.
func (ds Diagnostics) HasErrors() bool {
	for _, d := range ds {
		if !d.Warning {
			return true
		}
	}
	return false
}

// Errors returns only the errors.
func (ds Diagnostics) Errors() Diagnostics {
	var out Diagnostics
	for _, d := range ds {
		if !d.Warning {
			out = append(out, d)
		}
	}
	return out
}

var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidSlug reports whether s is a lowercase, dash-separated slug.
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// Slugify turns a title into a slug: "Hello, World!" -> "hello-world".
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == '\'' || r == '’':
			// "don't" -> "dont"
		default:
			if r > 127 {
				if t, ok := translit[r]; ok {
					b.WriteString(t)
					dash = false
					continue
				}
			}
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

var translit = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'æ': "ae", 'ç': "c",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ì': "i", 'í': "i", 'î': "i", 'ï': "i",
	'ñ': "n", 'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ù': "u",
	'ú': "u", 'û': "u", 'ü': "u", 'ý': "y", 'ÿ': "y", 'ß': "ss", 'œ': "oe",
}

// Load reads every post and page under contentDir. root is the site root,
// used to make Source paths relative. loc is the site timezone for dates
// written without one.
func Load(root, contentDir string, loc *time.Location) ([]*Item, Diagnostics) {
	var items []*Item
	var diags Diagnostics
	for _, kind := range []Kind{KindPost, KindPage} {
		dir := filepath.Join(contentDir, string(kind)+"s")
		found, ds := loadKind(root, dir, kind, loc)
		items = append(items, found...)
		diags = append(diags, ds...)
	}
	diags = append(diags, checkDuplicates(items)...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Source < items[j].Source })
	return items, diags
}

func loadKind(root, dir string, kind Kind, loc *time.Location) ([]*Item, Diagnostics) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, Diagnostics{{File: rel(root, dir), Message: err.Error()}}
	}
	var items []*Item
	var diags Diagnostics
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		path := filepath.Join(dir, name)
		var file, itemDir, defSlug string
		if e.IsDir() {
			itemDir = path
			defSlug = name
			for _, n := range []string{"index.md", "index.html"} {
				if _, err := os.Stat(filepath.Join(path, n)); err == nil {
					if file != "" {
						diags = append(diags, Diagnostic{File: rel(root, path), Message: "has both index.md and index.html; keep one"})
					}
					file = filepath.Join(path, n)
				}
			}
			if file == "" {
				diags = append(diags, Diagnostic{File: rel(root, path), Message: "folder has no index.md or index.html", Warning: true})
				continue
			}
		} else {
			ext := filepath.Ext(name)
			if ext != ".md" && ext != ".html" {
				diags = append(diags, Diagnostic{File: rel(root, path), Message: "ignored: only .md and .html files and post folders belong here", Warning: true})
				continue
			}
			file = path
			defSlug = strings.TrimSuffix(name, ext)
		}
		it, ds := parseFile(root, file, kind, defSlug, loc)
		diags = append(diags, ds...)
		if it != nil {
			it.Dir = itemDir
			items = append(items, it)
		}
	}
	return items, diags
}

func parseFile(root, file string, kind Kind, defSlug string, loc *time.Location) (*Item, Diagnostics) {
	src, err := os.ReadFile(file)
	relPath := rel(root, file)
	if err != nil {
		return nil, Diagnostics{{File: relPath, Message: err.Error()}}
	}
	it, diags := Parse(src, relPath, kind, defSlug, loc)
	return it, diags
}

// Parse parses one file's bytes. It is exported for the editor and tests.
func Parse(src []byte, relPath string, kind Kind, defSlug string, loc *time.Location) (*Item, Diagnostics) {
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	fmBytes, body, bodyLine, ok := splitFrontmatter(src)
	if !ok {
		return nil, Diagnostics{{File: relPath, Line: 1, Message: "missing frontmatter: the file must start with a line of three dashes (---)"}}
	}
	it := &Item{
		Kind:     kind,
		Format:   FormatMarkdown,
		Source:   relPath,
		Body:     body,
		BodyLine: bodyLine,
	}
	if strings.HasSuffix(relPath, ".html") {
		it.Format = FormatHTML
	}
	diags := decodeFrontmatter(fmBytes, it, relPath, loc)
	if diags.HasErrors() {
		return nil, diags
	}
	if it.Slug == "" {
		it.Slug = defSlug
	}
	if !ValidSlug(it.Slug) {
		diags = append(diags, Diagnostic{File: relPath, Message: fmt.Sprintf("slug %q must be lowercase letters, digits and single dashes (try %q)", it.Slug, Slugify(it.Slug))})
	}
	if kind == KindPost && it.Date.IsZero() {
		diags = append(diags, Diagnostic{File: relPath, Line: 1, Message: "posts need a date"})
	}
	if diags.HasErrors() {
		return nil, diags
	}
	return it, diags
}

func splitFrontmatter(src []byte) (fm, body []byte, bodyLine int, ok bool) {
	if !bytes.HasPrefix(src, []byte("---\n")) {
		return nil, nil, 0, false
	}
	rest := src[4:]
	line := 2
	for off := 0; off <= len(rest); {
		end := bytes.IndexByte(rest[off:], '\n')
		var ln []byte
		if end < 0 {
			ln = rest[off:]
		} else {
			ln = rest[off : off+end]
		}
		if string(bytes.TrimRight(ln, " \t")) == "---" {
			fm = rest[:off]
			if end < 0 {
				return fm, nil, line + 1, true
			}
			return fm, rest[off+end+1:], line + 1, true
		}
		if end < 0 {
			break
		}
		off += end + 1
		line++
	}
	return nil, nil, 0, false
}

func checkDuplicates(items []*Item) Diagnostics {
	var diags Diagnostics
	seen := map[string]*Item{}
	for _, it := range items {
		key := string(it.Kind) + ":" + it.Slug
		if prev, ok := seen[key]; ok {
			diags = append(diags, Diagnostic{File: it.Source, Message: fmt.Sprintf("slug %q is already used by %s", it.Slug, prev.Source)})
			continue
		}
		seen[key] = it
	}
	return diags
}

func rel(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

// TagSlug is the URL form of a tag name.
func TagSlug(tag string) string { return Slugify(tag) }
