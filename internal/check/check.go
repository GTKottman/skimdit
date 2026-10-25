// SPDX-License-Identifier: AGPL-3.0-or-later

// Package check builds a site into a scratch folder and inspects the
// result: broken internal links and anchors, drafts committed to git, page
// weight budgets, and anything later stages register.
package check

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GTKottman/skimdit/internal/config"
	"github.com/GTKottman/skimdit/internal/content"
	"github.com/GTKottman/skimdit/internal/site"
	"golang.org/x/net/html"
)

// Report is the outcome of a check.
type Report struct {
	Diags    content.Diagnostics
	Pages    int
	Duration time.Duration
}

// OK reports whether nothing failed.
func (r *Report) OK() bool { return !r.Diags.HasErrors() }

// Extra checks run on the built output folder (registered by later stages).
var Extra []func(b *site.Build, out string, add func(content.Diagnostic))

// Run checks the site at root.
func Run(root string, now time.Time) (*Report, error) { return RunWith(root, now, "") }

// RunWith checks the site and keeps the built output in keep when it is set.
func RunWith(root string, now time.Time, keep string) (*Report, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "skimdit-check-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "site")
	if keep != "" {
		out = keep
	}
	start := time.Now()
	res, err := site.Run(site.Options{Root: root, Config: cfg, Out: out, Now: now})
	rep := &Report{}
	if res != nil {
		rep.Diags = append(rep.Diags, res.Diags...)
	}
	if err != nil {
		if err == site.ErrBuild {
			return rep, nil
		}
		return nil, err
	}
	rep.Duration = time.Since(start)
	add := func(d content.Diagnostic) { rep.Diags = append(rep.Diags, d) }
	if s := cfg.Check.BuildBudgetSeconds; s > 0 && rep.Duration > time.Duration(s)*time.Second {
		add(content.Diagnostic{Message: fmt.Sprintf("build took %s, over the %ds budget (check.build_budget_seconds)", rep.Duration.Round(time.Millisecond), s)})
	}
	rep.Pages = checkOutput(cfg, out, res.Files, add)
	checkDraftsInGit(cfg, add)
	for _, f := range Extra {
		f(res.Build, out, add)
	}
	sort.SliceStable(rep.Diags, func(i, j int) bool {
		if rep.Diags[i].Warning != rep.Diags[j].Warning {
			return !rep.Diags[i].Warning
		}
		return rep.Diags[i].File < rep.Diags[j].File
	})
	return rep, nil
}

type pageInfo struct {
	ogImage string
	jsonLD  []string
	ids     map[string]bool
	links   []link
	css     []string
	js      []string
	size    int
}

type link struct {
	attr, val string
}

func checkOutput(cfg *config.Config, out string, files []string, add func(content.Diagnostic)) int {
	exists := map[string]bool{}
	for _, f := range files {
		exists[f] = true
	}
	pages := map[string]*pageInfo{}
	for _, f := range files {
		if !strings.HasSuffix(f, ".html") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(f)))
		if err != nil {
			add(content.Diagnostic{File: f, Message: err.Error()})
			continue
		}
		pages[f] = parsePage(b)
	}
	// Resolve a URL found on page f to an output file and fragment.
	resolve := func(f, raw string) (string, string, bool) {
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", false
		}
		if u.Scheme != "" || u.Host != "" {
			if cfg.BaseURL == nil || u.Host != cfg.BaseURL.Host {
				return "", "", false
			}
		}
		p := u.Path
		if p == "" {
			return f, u.Fragment, true
		}
		if !strings.HasPrefix(p, "/") {
			dir := "/" + path.Dir(f) + "/"
			p = path.Join(dir, p)
			if strings.HasSuffix(u.Path, "/") {
				p += "/"
			}
		} else {
			if !strings.HasPrefix(p, cfg.Site.BasePath) {
				return strings.TrimPrefix(p, "/"), "", true
			}
			p = "/" + strings.TrimPrefix(p, cfg.Site.BasePath)
		}
		file := strings.TrimPrefix(p, "/")
		if file == "" || strings.HasSuffix(file, "/") {
			file += "index.html"
		}
		return file, u.Fragment, true
	}
	var htmlBytes int
	for f, pg := range pages {
		for _, l := range pg.links {
			file, frag, internal := resolve(f, l.val)
			if !internal {
				continue
			}
			if !exists[file] && !exists[file+"/index.html"] {
				add(content.Diagnostic{File: f, Message: fmt.Sprintf("broken link: %s=%q (nothing at %s)", l.attr, l.val, file)})
				continue
			}
			if frag != "" && l.attr == "href" {
				target := pages[file]
				if target != nil && !target.ids[frag] {
					add(content.Diagnostic{File: f, Message: fmt.Sprintf("broken anchor: %q (no element with id %q on %s)", l.val, frag, file)})
				}
			}
		}
		if pg.ogImage != "" {
			if file, _, internal := resolve(f, pg.ogImage); internal && !exists[file] {
				add(content.Diagnostic{File: f, Message: fmt.Sprintf("social image %s does not exist", pg.ogImage)})
			}
		}
		for _, ld := range pg.jsonLD {
			var v map[string]any
			if err := json.Unmarshal([]byte(ld), &v); err != nil {
				add(content.Diagnostic{File: f, Message: fmt.Sprintf("structured data (JSON-LD) is not valid JSON: %v", err)})
			} else if v["@context"] != "https://schema.org" {
				add(content.Diagnostic{File: f, Message: "structured data (JSON-LD) has no schema.org @context"})
			}
		}
		// Weight budget: compressed HTML + CSS, and JS.
		size := gzSize(filepath.Join(out, filepath.FromSlash(f)))
		for _, c := range pg.css {
			if file, _, ok := resolve(f, c); ok {
				size += gzSize(filepath.Join(out, filepath.FromSlash(file)))
			}
		}
		js := 0
		for _, j := range pg.js {
			if file, _, ok := resolve(f, j); ok {
				js += gzSize(filepath.Join(out, filepath.FromSlash(file)))
			}
		}
		htmlBytes += size
		if lim := cfg.Check.PageBudgetKB; lim > 0 && size > lim*1024 && !strings.HasPrefix(f, "search-") {
			add(content.Diagnostic{File: f, Message: fmt.Sprintf("page weighs %.1f KB compressed (HTML+CSS), over the %d KB budget", float64(size)/1024, lim), Warning: true})
		}
		if lim := cfg.Check.JSBudgetKB; lim > 0 && js > lim*1024 {
			add(content.Diagnostic{File: f, Message: fmt.Sprintf("page loads %.1f KB of compressed JavaScript, over the %d KB budget", float64(js)/1024, lim)})
		}
	}
	checkSitemap(cfg, out, exists, add)
	return len(pages)
}

// checkSitemap requires every sitemap URL to be a page that exists.
func checkSitemap(cfg *config.Config, out string, exists map[string]bool, add func(content.Diagnostic)) {
	b, err := os.ReadFile(filepath.Join(out, "sitemap.xml"))
	if err != nil {
		return
	}
	var doc struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		add(content.Diagnostic{File: "sitemap.xml", Message: "not valid XML: " + err.Error()})
		return
	}
	prefix := cfg.AbsURL(cfg.Site.BasePath)
	for _, u := range doc.URLs {
		if !strings.HasPrefix(u.Loc, prefix) {
			add(content.Diagnostic{File: "sitemap.xml", Message: u.Loc + " is not on this site"})
			continue
		}
		file := strings.TrimPrefix(u.Loc, prefix)
		if file == "" || strings.HasSuffix(file, "/") {
			file += "index.html"
		}
		if !exists[file] {
			add(content.Diagnostic{File: "sitemap.xml", Message: u.Loc + " is listed but does not exist"})
		}
	}
}

func gzSize(file string) int {
	b, err := os.ReadFile(file)
	if err != nil {
		return 0
	}
	var buf bytes.Buffer
	w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	w.Write(b)
	w.Close()
	return buf.Len()
}

func parsePage(b []byte) *pageInfo {
	pg := &pageInfo{ids: map[string]bool{}, size: len(b)}
	z := html.NewTokenizer(bytes.NewReader(b))
	inLD := false
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return pg
		}
		if tt == html.TextToken && inLD {
			pg.jsonLD = append(pg.jsonLD, string(z.Text()))
			continue
		}
		if tt == html.EndTagToken {
			inLD = false
			continue
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		attrs := map[string]string{}
		for _, a := range tok.Attr {
			attrs[a.Key] = a.Val
			if a.Key == "id" {
				pg.ids[a.Val] = true
			}
			if a.Key == "name" && tok.Data == "a" {
				pg.ids[a.Val] = true
			}
		}
		switch tok.Data {
		case "meta":
			if attrs["property"] == "og:image" {
				pg.ogImage = attrs["content"]
			}
		case "a":
			if h, ok := attrs["href"]; ok && !strings.HasPrefix(h, "mailto:") {
				pg.links = append(pg.links, link{"href", h})
			}
		case "img", "source", "video", "audio", "script":
			if tok.Data == "script" && attrs["type"] == "application/ld+json" {
				inLD = true
			}
			if s, ok := attrs["src"]; ok && s != "" {
				pg.links = append(pg.links, link{"src", s})
				if tok.Data == "script" {
					pg.js = append(pg.js, s)
				}
			}
			if s, ok := attrs["srcset"]; ok {
				for _, part := range strings.Split(s, ",") {
					if fields := strings.Fields(part); len(fields) > 0 {
						pg.links = append(pg.links, link{"srcset", fields[0]})
					}
				}
			}
		case "link":
			if h, ok := attrs["href"]; ok && attrs["rel"] != "canonical" && attrs["rel"] != "me" && attrs["rel"] != "webmention" {
				pg.links = append(pg.links, link{"href", h})
				if attrs["rel"] == "stylesheet" {
					pg.css = append(pg.css, h)
				}
			}
		}
	}
}

// checkDraftsInGit fails when a draft file is tracked by git: the posts
// repo is public, so committing a draft publishes it.
func checkDraftsInGit(cfg *config.Config, add func(content.Diagnostic)) {
	git, err := exec.LookPath("git")
	if err != nil {
		return
	}
	cmd := exec.Command(git, "-C", cfg.Root, "ls-files", "-z", "--", cfg.Build.Content)
	outB, err := cmd.Output()
	if err != nil {
		return
	}
	items, _ := content.Load(cfg.Root, cfg.Path(cfg.Build.Content), cfg.Location)
	tracked := map[string]bool{}
	for _, f := range strings.Split(string(outB), "\x00") {
		if f != "" {
			tracked[filepath.ToSlash(filepath.Join(filepath.Base(cfg.Root), f))] = true
			tracked[f] = true
		}
	}
	for _, it := range items {
		if it.Status == content.StatusDraft && tracked[it.Source] {
			add(content.Diagnostic{File: it.Source, Message: "this draft is committed to git; anyone can read it in the repository. Remove it from git (git rm --cached) or publish it"})
		}
	}
}
