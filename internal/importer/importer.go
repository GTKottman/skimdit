// SPDX-License-Identifier: AGPL-3.0-or-later

// Package importer brings posts from WordPress, Ghost and Markdown folders
// (Hugo, Jekyll, Skimdit) into a Skimdit site. Old addresses become
// aliases, so existing links keep working; images are downloaded next to
// their posts, so readers make no requests to the old site.
package importer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/GTKottman/skimdit/internal/content"
	"github.com/GTKottman/skimdit/internal/media"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

// Options control an import.
type Options struct {
	// ContentDir is the site's content folder.
	ContentDir string
	// Location is the site timezone (for dates written without one).
	Location *time.Location
	// Download fetches remote images into post folders.
	Download bool
	// BaseURL resolves relative or placeholder image URLs (Ghost's __GHOST_URL__).
	BaseURL string
	// ImagesDir is a local folder of exported images to copy from instead of downloading.
	ImagesDir string
	// Client fetches images (tests swap it).
	Client *http.Client
	// Overwrite replaces posts whose folder already exists.
	Overwrite bool
}

// Post is one item to write.
type Post struct {
	Kind   content.Kind
	Fields content.Fields
	// HTML or Markdown body (exactly one is set).
	HTML     string
	Markdown string
	// Files are co-located files to copy (name -> source path).
	Files map[string]string
	// From names the source item for messages.
	From string
}

// Report says what happened.
type Report struct {
	Posts    int
	Pages    int
	Drafts   int
	Images   int
	Skipped  int
	Warnings []string
}

func (r *Report) warn(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// String summarizes a report.
func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Imported %d posts and %d pages (%d of them drafts), %d images; skipped %d.\n", r.Posts, r.Pages, r.Drafts, r.Images, r.Skipped)
	for _, w := range r.Warnings {
		b.WriteString("  warning: " + w + "\n")
	}
	if r.Drafts > 0 {
		b.WriteString("Drafts were written with status: draft. Do not commit them to a public repository; `skimdit check` will remind you.\n")
	}
	return b.String()
}

// write stores posts in the content folder.
func write(posts []*Post, opts Options, rep *Report) error {
	used := map[string]bool{}
	for _, p := range posts {
		f := &p.Fields
		base := content.Slugify(firstNonEmpty(f.Slug, f.Title))
		if base == "" {
			base = "untitled"
		}
		slug := base
		for i := 2; used[string(p.Kind)+slug]; i++ {
			slug = fmt.Sprintf("%s-%d", base, i)
		}
		used[string(p.Kind)+slug] = true
		f.Slug = ""
		if slug != content.Slugify(f.Title) {
			f.Slug = slug
		}
		dir := filepath.Join(opts.ContentDir, string(p.Kind)+"s", slug)
		if _, err := os.Stat(dir); err == nil && !opts.Overwrite {
			rep.warn("%s: %s already exists; skipped (use --overwrite to replace)", p.From, dir)
			rep.Skipped++
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		body := p.Markdown
		if p.HTML != "" {
			md, n, err := htmlToMarkdown(p.HTML, dir, opts, rep, p.From)
			if err != nil {
				return err
			}
			body = md
			rep.Images += n
		}
		for name, src := range p.Files {
			if err := copyFile(src, filepath.Join(dir, name)); err != nil {
				rep.warn("%s: could not copy %s: %v", p.From, name, err)
			}
		}
		if f.Cover != "" && (strings.HasPrefix(f.Cover, "http") || strings.HasPrefix(f.Cover, "/") || strings.HasPrefix(f.Cover, "__GHOST_URL__")) {
			if name, err := fetchImage(f.Cover, dir, opts); err != nil {
				rep.warn("%s: cover image %s: %v (cover dropped)", p.From, f.Cover, err)
				f.Cover, f.CoverAlt = "", ""
			} else {
				f.Cover = name
				rep.Images++
			}
		}
		if f.Cover != "" && f.CoverAlt == "" {
			f.CoverAlt = f.Title
			rep.warn("%s: the cover image has no description; used the title, please write a real one", p.From)
		}
		text, err := content.FileText(*f, body)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(text), 0o644); err != nil {
			return err
		}
		if p.Kind == content.KindPage {
			rep.Pages++
		} else {
			rep.Posts++
		}
		if f.Status == "draft" {
			rep.Drafts++
		}
	}
	return nil
}

var (
	imgMDRe    = regexp.MustCompile(`!\[([^\]]*)\]\(<?([^)\s>]+)>?(?:\s+"([^"]*)")?\)`)
	commentRe  = regexp.MustCompile(`(?s)<!--.*?-->`)
	blankRunRe = regexp.MustCompile(`\n{3,}`)
)

// htmlToMarkdown converts HTML to Markdown and brings images local.
func htmlToMarkdown(h, dir string, opts Options, rep *Report, from string) (string, int, error) {
	h = commentRe.ReplaceAllString(h, "")
	md, err := htmltomarkdown.ConvertString(h)
	if err != nil {
		return "", 0, fmt.Errorf("%s: converting HTML: %w", from, err)
	}
	n := 0
	md = imgMDRe.ReplaceAllStringFunc(md, func(m string) string {
		sub := imgMDRe.FindStringSubmatch(m)
		alt, src, title := sub[1], sub[2], sub[3]
		if alt == "" {
			alt = "Image"
			rep.warn("%s: an image has no description; wrote \"Image\", please describe it", from)
		}
		name, err := fetchImage(src, dir, opts)
		if err != nil {
			rep.warn("%s: image %s: %v (kept as a link)", from, src, err)
			return "[" + alt + "](" + src + ")"
		}
		n++
		if title != "" {
			return "![" + alt + "](" + name + " \"" + title + "\")"
		}
		return "![" + alt + "](" + name + ")"
	})
	return strings.TrimSpace(blankRunRe.ReplaceAllString(md, "\n\n")) + "\n", n, nil
}

var nameCleanRe = regexp.MustCompile(`[^a-z0-9.]+`)

// fetchImage stores an image next to a post and returns its file name.
func fetchImage(src, dir string, opts Options) (string, error) {
	src = strings.ReplaceAll(src, "__GHOST_URL__", strings.TrimSuffix(opts.BaseURL, "/"))
	u, err := url.Parse(src)
	if err != nil {
		return "", err
	}
	if !u.IsAbs() && opts.BaseURL != "" {
		base, _ := url.Parse(opts.BaseURL)
		u = base.ResolveReference(u)
	}
	name := path.Base(u.Path)
	name = strings.Trim(nameCleanRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	ext := strings.ToLower(path.Ext(name))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg":
	default:
		return "", fmt.Errorf("not an image file")
	}
	dst := filepath.Join(dir, name)
	for i := 2; ; i++ {
		if _, err := os.Stat(dst); err != nil {
			break
		}
		name = strings.TrimSuffix(path.Base(u.Path), ext) + fmt.Sprintf("-%d", i) + ext
		name = strings.Trim(nameCleanRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
		dst = filepath.Join(dir, name)
	}
	// A local export folder wins over downloading.
	if opts.ImagesDir != "" {
		for _, cand := range []string{filepath.Join(opts.ImagesDir, filepath.FromSlash(strings.TrimPrefix(u.Path, "/"))), filepath.Join(opts.ImagesDir, path.Base(u.Path))} {
			if _, err := os.Stat(cand); err == nil {
				return name, copyFile(cand, dst)
			}
		}
	}
	if !opts.Download || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("not downloaded (run with downloads on, or give --images)")
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "Skimdit importer")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server answered %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 50<<20))
	if err != nil {
		return "", err
	}
	if ext == ".svg" {
		if _, err := media.CheckSVG(data); err != nil {
			return "", err
		}
	} else if !bytes.HasPrefix([]byte(http.DetectContentType(data)), []byte("image/")) {
		return "", fmt.Errorf("the download is not an image")
	}
	return name, os.WriteFile(dst, data, 0o644)
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}

// aliasFrom turns an old absolute URL into a site path for aliases, when it
// differs from where the post now lives.
func aliasFrom(old string) string {
	u, err := url.Parse(strings.TrimSpace(old))
	if err != nil || u.Path == "" || u.Path == "/" || u.RawQuery != "" {
		return ""
	}
	p := u.Path
	if !strings.HasSuffix(p, "/") && path.Ext(p) == "" {
		p += "/"
	}
	return p
}

func dateString(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return t.In(loc).Format("2006-01-02T15:04:05-07:00")
}

func uniqueSorted(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		x = strings.TrimSpace(x)
		k := strings.ToLower(x)
		if x == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}
