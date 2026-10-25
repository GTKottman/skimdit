// SPDX-License-Identifier: AGPL-3.0-or-later

// Package site builds a whole site: it loads content, renders every page
// through the theme and writes a static output folder atomically.
package site

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GTKottman/skimdit/internal/config"
	"github.com/GTKottman/skimdit/internal/content"
	"github.com/GTKottman/skimdit/internal/locale"
	"github.com/GTKottman/skimdit/internal/markdown"
	"github.com/GTKottman/skimdit/internal/media"
	"github.com/GTKottman/skimdit/internal/theme"
	"github.com/GTKottman/skimdit/internal/version"
)

// Options control a build.
type Options struct {
	// Root is the site folder (holding config.toml).
	Root string
	// Config, when set, is used instead of loading Root/config.toml.
	Config *config.Config
	// Out overrides the output folder.
	Out string
	// Drafts includes drafts and scheduled posts (dev server only).
	Drafts bool
	// Dev marks pages as built by the dev server.
	Dev bool
	// Now is the time used for scheduling. Zero means time.Now().
	Now time.Time
	// Workers limits parallel rendering. Zero means the number of CPUs.
	Workers int
	// RecordURLs updates published-urls.txt and redirects.toml (production builds).
	RecordURLs bool
	// Extra hooks run after pages are rendered and before the output is swapped in.
	Extra []func(*Build) error
}

// Build is the state of one build. Later stages (feeds, sitemap, social
// cards) and plugins read it.
type Build struct {
	Opts     Options
	Config   *config.Config
	Locale   *locale.Locale
	Theme    *theme.Theme
	Site     *SiteData
	Entries  []*Entry // every visible entry
	Posts    []*Entry // listed posts, newest first
	Pages    []*Entry // listed pages
	Redirect map[string]string
	Diags    content.Diagnostics
	Out      string
	Now      time.Time

	md          *markdown.Renderer
	media       *media.Pipeline
	files       map[string]string // output path -> who wrote it
	fmu         sync.Mutex
	dmu         sync.Mutex
	bySlug      map[string]*Entry
	byURL       map[string]*Entry
	copies      map[string]string
	mediaRes    map[string]*media.Result
	mmu         sync.Mutex
	tmp         string
	siteCSS     string
	siteCSSPath string
	directive   map[string]*template.Template
}

// Result summarizes a build.
type Result struct {
	Out      string
	Files    []string
	Diags    content.Diagnostics
	Entries  int
	Duration time.Duration
	Build    *Build
}

// ErrBuild is returned when content has errors; the diagnostics say which.
var ErrBuild = errors.New("build failed")

// Run builds the site.
func Run(opts Options) (*Result, error) {
	start := time.Now()
	b, err := newBuild(opts)
	if err != nil {
		return nil, err
	}
	res, err := b.run()
	if res != nil {
		res.Duration = time.Since(start)
	}
	return res, err
}

func newBuild(opts Options) (*Build, error) {
	cfg := opts.Config
	if cfg == nil {
		var err error
		if cfg, err = config.Load(opts.Root); err != nil {
			return nil, err
		}
	}
	loc, err := locale.Load(cfg.Root, cfg.Site.Language)
	if err != nil {
		return nil, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	workers := opts.Workers
	if workers < 1 {
		workers = runtime.NumCPU()
	}
	out := opts.Out
	if out == "" {
		out = cfg.Path(cfg.Build.Output)
	}
	b := &Build{
		Opts:     opts,
		Config:   cfg,
		Locale:   loc,
		Now:      now,
		Out:      out,
		Redirect: map[string]string{},
		md:       markdown.New(markdown.Options{RawHTML: cfg.Markdown.RawHTML, Typographer: cfg.Markdown.Typographer}),
		media: media.New(media.Settings{
			Widths: cfg.Images.Widths, Formats: cfg.Images.Formats, Quality: cfg.Images.Quality, MaxSize: cfg.Images.MaxFileSize,
		}, filepath.Join(cfg.Path(cfg.Build.Cache), "media"), max(1, workers/2)),
		files:    map[string]string{},
		bySlug:   map[string]*Entry{},
		byURL:    map[string]*Entry{},
		copies:   map[string]string{},
		mediaRes: map[string]*media.Result{},
	}
	b.Opts.Workers = workers
	th, err := theme.Load(cfg.Root, cfg.Theme.Name, b.funcs())
	if err != nil {
		return nil, err
	}
	b.Theme = th
	if th.Deprecated() {
		b.warn("", 0, "theme %q uses engine_api %d, which is deprecated and will stop working in a future release; update the theme", th.Manifest.Name, th.Manifest.EngineAPI)
	}
	return b, nil
}

func (b *Build) diag(file string, line int, warning bool, format string, args ...any) {
	b.dmu.Lock()
	defer b.dmu.Unlock()
	b.Diags = append(b.Diags, content.Diagnostic{File: file, Line: line, Message: fmt.Sprintf(format, args...), Warning: warning})
}

func (b *Build) warn(file string, line int, format string, args ...any) {
	b.diag(file, line, true, format, args...)
}

func (b *Build) run() (*Result, error) {
	cfg := b.Config
	items, diags := content.Load(cfg.Root, cfg.Path(cfg.Build.Content), cfg.Location)
	b.Diags = append(b.Diags, diags...)
	if err := b.loadDirectives(); err != nil {
		return nil, err
	}
	b.prepareEntries(items)
	b.renderBodies()
	b.relate()
	b.buildSiteData()
	if b.Diags.HasErrors() {
		return &Result{Diags: b.sortedDiags(), Build: b}, ErrBuild
	}

	parent := filepath.Dir(b.Out)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(b.Out)+".tmp-")
	if err != nil {
		return nil, err
	}
	b.tmp = tmp
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()
	steps := []func() error{
		b.writeAssets,
		b.writeStatic,
		b.socialCards,
		b.renderPages,
		b.feeds,
		b.sitemap,
		b.robots,
		b.searchIndexIfOn,
		b.redirectsFile,
		b.writeCopies,
		b.writeMedia,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	for _, extra := range b.Opts.Extra {
		if err := extra(b); err != nil {
			return nil, err
		}
	}
	if b.Diags.HasErrors() {
		return &Result{Diags: b.sortedDiags(), Build: b}, ErrBuild
	}
	if err := swap(tmp, b.Out); err != nil {
		return nil, err
	}
	ok = true
	files := make([]string, 0, len(b.files))
	for f := range b.files {
		files = append(files, f)
	}
	sort.Strings(files)
	return &Result{Out: b.Out, Files: files, Diags: b.sortedDiags(), Entries: len(b.Entries), Build: b}, nil
}

func (b *Build) sortedDiags() content.Diagnostics {
	ds := append(content.Diagnostics(nil), b.Diags...)
	sort.SliceStable(ds, func(i, j int) bool {
		if ds[i].File != ds[j].File {
			return ds[i].File < ds[j].File
		}
		return ds[i].Line < ds[j].Line
	})
	return ds
}

// swap replaces out with tmp in two renames, so readers never see a half-written site.
func swap(tmp, out string) error {
	old := ""
	if _, err := os.Stat(out); err == nil {
		old = out + ".old-" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if err := os.Rename(out, old); err != nil {
			return err
		}
	}
	if err := os.Rename(tmp, out); err != nil {
		if old != "" {
			_ = os.Rename(old, out)
		}
		return err
	}
	if old != "" {
		return os.RemoveAll(old)
	}
	return nil
}

// reserved are first path segments the engine owns.
var reserved = map[string]bool{
	"tags": true, "series": true, "archive": true, "search": true, "page": true, "assets": true,
	"media": true, "admin": true, "api": true, "ap": true, ".well-known": true, "webmention": true,
	"feed": true, "feeds": true, "social": true,
}

func (b *Build) visible(it *content.Item) bool {
	if b.Opts.Drafts {
		return true
	}
	return it.Visible(b.Now)
}

func (b *Build) prepareEntries(items []*content.Item) {
	cfg := b.Config
	for _, it := range items {
		if !b.visible(it) {
			continue
		}
		e := &Entry{
			Kind:        string(it.Kind),
			Title:       it.Title,
			Slug:        it.Slug,
			Date:        it.Date.In(cfg.Location),
			Updated:     it.Updated,
			Status:      string(it.Status),
			Unlisted:    it.Status == content.StatusUnlisted,
			Lang:        it.Lang,
			Canonical:   it.Canonical,
			Extra:       it.Extra,
			Source:      it.Source,
			Description: it.Description,
			item:        it,
		}
		if !e.Updated.IsZero() {
			e.Updated = e.Updated.In(cfg.Location)
		}
		if e.Lang == "" {
			e.Lang = cfg.Site.Language
		}
		pattern := cfg.Permalinks.Post
		if it.Kind == content.KindPage {
			pattern = cfg.Permalinks.Page
			first := strings.SplitN(strings.TrimPrefix(b.permalink(pattern, it.Slug, e.Date), cfg.Site.BasePath), "/", 2)[0]
			if reserved[first] {
				b.diag(it.Source, 0, false, "page URL /%s/ is reserved by Skimdit; pick another slug", first)
				continue
			}
		}
		e.URL = b.permalink(pattern, it.Slug, e.Date)
		e.Permalink = cfg.AbsURL(e.URL)
		if prev, ok := b.byURL[e.URL]; ok {
			b.diag(it.Source, 0, false, "URL %s is already used by %s", e.URL, prev.Source)
			continue
		}
		b.byURL[e.URL] = e
		b.bySlug[e.Kind+":"+e.Slug] = e
		e.Comments = cfg.Comments.Enabled && it.Kind == content.KindPost && (it.Comments == nil && cfg.Comments.Default || it.Comments != nil && *it.Comments)
		b.Entries = append(b.Entries, e)
	}
}

func (b *Build) permalink(pattern, slug string, date time.Time) string {
	r := strings.NewReplacer(
		":slug", slug,
		":year", strconv.Itoa(date.Year()),
		":month", fmt.Sprintf("%02d", int(date.Month())),
		":day", fmt.Sprintf("%02d", date.Day()),
	)
	return b.Config.Rel(r.Replace(pattern))
}

// folderURL is the URL folder that holds an entry's co-located files.
func folderURL(u string) string {
	if strings.HasSuffix(u, "/") {
		return u
	}
	return path.Dir(u) + "/"
}

func (b *Build) renderBodies() {
	work := make(chan *Entry)
	var wg sync.WaitGroup
	for i := 0; i < b.Opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range work {
				b.renderEntry(e)
			}
		}()
	}
	for _, e := range b.Entries {
		work <- e
	}
	close(work)
	wg.Wait()
}

func (b *Build) renderEntry(e *Entry) {
	it := e.item
	var doc *markdown.Doc
	if it.Format == content.FormatHTML {
		doc = b.md.RenderHTML(it.Body)
	} else {
		var err error
		doc, err = b.md.Render(it.Body, markdown.Hooks{
			Link:       b.linkHook(e),
			Image:      b.imageHook(e),
			Directive:  b.directiveHook(),
			RequireAlt: b.Config.Images.RequireAlt,
		})
		if err != nil {
			b.diag(it.Source, 0, false, "rendering: %v", err)
			return
		}
	}
	for _, d := range doc.Diags {
		line := 0
		if d.Line > 0 {
			line = it.BodyLine + d.Line - 1
		}
		b.diag(it.Source, line, d.Warning, "%s", d.Message)
	}
	e.doc = doc
	e.Content = template.HTML(doc.HTML)
	e.Words = doc.Words
	e.ReadingTime = int(math.Max(1, math.Ceil(float64(doc.Words)/230)))
	e.HasMath, e.HasCode, e.HasEmbed = doc.HasMath, doc.HasCode, doc.HasEmbed
	e.links = doc.Links
	e.Excerpt = it.Excerpt
	if e.Excerpt == "" {
		e.Excerpt = doc.Excerpt
	}
	if e.Description == "" {
		e.Description = markdown.Truncate(e.Excerpt, 160)
	}
	for _, h := range doc.Headings {
		if h.Level >= 2 && h.Level <= 4 {
			e.TOC = append(e.TOC, h)
		}
	}
	e.ShowTOC = len(e.TOC) >= b.Config.Markdown.TOCMinHeadings
	if it.TOC != nil {
		e.ShowTOC = *it.TOC && len(e.TOC) > 0
	}
	if it.Cover != "" {
		img, err := b.imageHook(e)(it.Cover)
		if err != nil {
			b.diag(it.Source, 1, false, "cover %q: %v", it.Cover, err)
		} else {
			e.Cover = &ImageData{
				HTML: template.HTML(markdown.PictureHTML(img, it.CoverAlt, true)),
				URL:  img.Src, AbsURL: b.Config.AbsURL(img.Src), Width: img.Width, Height: img.Height, Alt: it.CoverAlt,
			}
		}
	}
}

// sourceDir is where relative paths in an entry resolve.
func sourceDir(b *Build, e *Entry) string {
	if e.item.Dir != "" {
		return e.item.Dir
	}
	return filepath.Dir(filepath.Join(b.Config.Root, filepath.FromSlash(e.item.Source)))
}

func hasScheme(s string) bool {
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return false
	}
	for _, r := range s[:i] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}

func (b *Build) linkHook(e *Entry) func(string) (string, error) {
	return func(dest string) (string, error) {
		switch {
		case dest == "" || strings.HasPrefix(dest, "#") || strings.HasPrefix(dest, "?"):
			return dest, nil
		case strings.HasPrefix(dest, "post:") || strings.HasPrefix(dest, "page:"):
			kind, rest, _ := strings.Cut(dest, ":")
			slug, frag, _ := strings.Cut(rest, "#")
			t := b.bySlug[kind+":"+slug]
			if t == nil {
				return dest, fmt.Errorf("link %q: there is no published %s with slug %q", dest, kind, slug)
			}
			if frag != "" {
				return t.URL + "#" + frag, nil
			}
			return t.URL, nil
		case strings.HasPrefix(dest, "//") || hasScheme(dest):
			return dest, nil
		case strings.HasPrefix(dest, "/"):
			if b.Config.Site.BasePath != "/" && !strings.HasPrefix(dest, b.Config.Site.BasePath) {
				return b.Config.Rel(dest), nil
			}
			return dest, nil
		}
		p, suffix := dest, ""
		if i := strings.IndexAny(dest, "?#"); i >= 0 {
			p, suffix = dest[:i], dest[i:]
		}
		clean := path.Clean(p)
		if strings.HasPrefix(clean, "..") || strings.HasSuffix(p, "/") || path.Ext(clean) == "" {
			// A relative page link; the link checker verifies it.
			return dest, nil
		}
		file := filepath.Join(sourceDir(b, e), filepath.FromSlash(clean))
		st, err := os.Stat(file)
		if err != nil || st.IsDir() {
			return dest, fmt.Errorf("link %q: file not found next to the post", dest)
		}
		u := folderURL(e.URL) + clean
		b.fmu.Lock()
		b.copies[u] = file
		b.fmu.Unlock()
		return u + suffix, nil
	}
}

func (b *Build) resolveSource(e *Entry, src string) (string, error) {
	var file string
	if strings.HasPrefix(src, "/") {
		file = filepath.Join(b.Config.Root, "static", filepath.FromSlash(path.Clean(src)))
	} else {
		clean := path.Clean(src)
		if strings.HasPrefix(clean, "..") && e.item.Dir != "" {
			return "", errors.New("must be inside the post folder (or start with / for the site's static folder)")
		}
		file = filepath.Join(sourceDir(b, e), filepath.FromSlash(clean))
	}
	rel, err := filepath.Rel(b.Config.Root, file)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", errors.New("must be inside the site folder")
	}
	if st, err := os.Stat(file); err != nil || st.IsDir() {
		return "", errors.New("file not found")
	}
	return file, nil
}

func (b *Build) imageHook(e *Entry) func(string) (*markdown.Image, error) {
	return func(src string) (*markdown.Image, error) {
		file, err := b.resolveSource(e, strings.SplitN(src, "?", 2)[0])
		if err != nil {
			return nil, err
		}
		res, err := b.media.Process(file)
		if err != nil {
			return nil, err
		}
		b.mmu.Lock()
		for _, v := range res.Variants {
			b.mediaRes[v.Name] = res
		}
		b.mmu.Unlock()
		return b.toImage(res), nil
	}
}

// toImage turns pipeline variants into <picture> data.
func (b *Build) toImage(res *media.Result) *markdown.Image {
	img := &markdown.Image{Width: res.Width, Height: res.Height}
	byFormat := map[string][]media.Variant{}
	for _, v := range res.Variants {
		byFormat[v.Format] = append(byFormat[v.Format], v)
	}
	url := func(v media.Variant) string { return b.Config.Rel("/media/" + v.Name) }
	srcset := func(vs []media.Variant) string {
		parts := make([]string, len(vs))
		for i, v := range vs {
			parts[i] = url(v) + " " + strconv.Itoa(v.Width) + "w"
		}
		return strings.Join(parts, ", ")
	}
	for _, f := range []string{"jpeg", "png", "gif", "svg"} {
		if vs := byFormat[f]; len(vs) > 0 {
			img.Src = url(vs[len(vs)-1])
			if len(vs) > 1 {
				img.Srcset = srcset(vs)
			}
		}
	}
	for _, f := range []string{"avif", "webp"} {
		if vs := byFormat[f]; len(vs) > 0 {
			img.Sources = append(img.Sources, markdown.ImageSource{Type: "image/" + f, Srcset: srcset(vs)})
		}
	}
	return img
}

func (b *Build) loadDirectives() error {
	b.directive = map[string]*template.Template{}
	dir := filepath.Join(b.Config.Root, "directives")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, en := range entries {
		if en.IsDir() || filepath.Ext(en.Name()) != ".html" {
			continue
		}
		name := strings.TrimSuffix(en.Name(), ".html")
		src, err := os.ReadFile(filepath.Join(dir, en.Name()))
		if err != nil {
			return err
		}
		t, err := template.New(name).Funcs(b.funcs()).Parse(string(src))
		if err != nil {
			return fmt.Errorf("directives/%s: %w", en.Name(), err)
		}
		b.directive[name] = t
	}
	return nil
}

// DirectiveData is what a site directive template receives.
type DirectiveData struct {
	Attrs map[string]string
	Inner template.HTML
}

func (b *Build) directiveHook() func(string, map[string]string, string) (string, bool, error) {
	return func(name string, attrs map[string]string, inner string) (string, bool, error) {
		t, ok := b.directive[name]
		if !ok {
			return "", false, nil
		}
		var buf bytes.Buffer
		if err := t.Execute(&buf, DirectiveData{Attrs: attrs, Inner: template.HTML(inner)}); err != nil {
			return "", true, err
		}
		return buf.String(), true, nil
	}
}

func (b *Build) relate() {
	cfg := b.Config
	var posts, pages []*Entry
	for _, e := range b.Entries {
		if e.doc == nil {
			continue
		}
		if e.Unlisted || (!b.Opts.Drafts && !e.item.Listed(b.Now)) {
			continue
		}
		if e.Kind == "post" {
			posts = append(posts, e)
		} else {
			pages = append(pages, e)
		}
	}
	sort.SliceStable(posts, func(i, j int) bool {
		if !posts[i].Date.Equal(posts[j].Date) {
			return posts[i].Date.After(posts[j].Date)
		}
		return posts[i].Slug < posts[j].Slug
	})
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Title < pages[j].Title })
	b.Posts, b.Pages = posts, pages
	for i, p := range posts {
		if i+1 < len(posts) {
			p.Prev = posts[i+1]
		}
		if i > 0 {
			p.Next = posts[i-1]
		}
	}

	// Tags.
	tags := map[string]*Term{}
	for _, e := range b.Entries {
		seen := map[string]bool{}
		for _, name := range e.item.Tags {
			slug := content.TagSlug(name)
			if slug == "" {
				b.diag(e.Source, 0, false, "tag %q has no letters or digits", name)
				continue
			}
			if seen[slug] {
				continue
			}
			seen[slug] = true
			t := tags[slug]
			if t == nil {
				t = &Term{Name: name, Slug: slug, URL: b.permalink(cfg.Permalinks.Tag, slug, time.Time{})}
				tags[slug] = t
			}
			e.Tags = append(e.Tags, t)
		}
	}
	for _, p := range posts {
		for _, t := range p.Tags {
			t.Entries = append(t.Entries, p)
		}
	}
	terms := make([]*Term, 0, len(tags))
	for _, t := range tags {
		t.Count = len(t.Entries)
		if t.Count > 0 {
			terms = append(terms, t)
		}
	}
	sort.Slice(terms, func(i, j int) bool { return terms[i].Slug < terms[j].Slug })

	// Series.
	series := map[string]*SeriesData{}
	for _, p := range posts {
		if p.item.Series == "" {
			continue
		}
		slug := content.Slugify(p.item.Series)
		s := series[slug]
		if s == nil {
			s = &SeriesData{Name: p.item.Series, Slug: slug, URL: b.permalink(cfg.Permalinks.Series, slug, time.Time{})}
			series[slug] = s
		}
		s.Entries = append(s.Entries, p)
		p.Series = s
	}
	seriesList := make([]*SeriesData, 0, len(series))
	for _, s := range series {
		sort.SliceStable(s.Entries, func(i, j int) bool {
			a, c := s.Entries[i], s.Entries[j]
			if a.item.SeriesOrder != c.item.SeriesOrder {
				if a.item.SeriesOrder == 0 || c.item.SeriesOrder == 0 {
					return c.item.SeriesOrder == 0
				}
				return a.item.SeriesOrder < c.item.SeriesOrder
			}
			return a.Date.Before(c.Date)
		})
		for i, e := range s.Entries {
			e.SeriesPart = i + 1
			if i > 0 {
				e.SeriesPrev = s.Entries[i-1]
			}
			if i+1 < len(s.Entries) {
				e.SeriesNext = s.Entries[i+1]
			}
		}
		seriesList = append(seriesList, s)
	}
	sort.Slice(seriesList, func(i, j int) bool { return seriesList[i].Slug < seriesList[j].Slug })

	// Related posts: shared tags, then same series, then closeness in time.
	for _, p := range posts {
		type scored struct {
			e     *Entry
			score float64
		}
		var cands []scored
		for _, q := range posts {
			if q == p {
				continue
			}
			s := 0.0
			for _, t := range p.Tags {
				for _, u := range q.Tags {
					if t == u {
						s += 10
					}
				}
			}
			if p.Series != nil && p.Series == q.Series {
				s += 5
			}
			if s == 0 {
				continue
			}
			days := math.Abs(p.Date.Sub(q.Date).Hours() / 24)
			s += 1 / (1 + days/30)
			cands = append(cands, scored{q, s})
		}
		sort.SliceStable(cands, func(i, j int) bool {
			if cands[i].score != cands[j].score {
				return cands[i].score > cands[j].score
			}
			return cands[i].e.Slug < cands[j].e.Slug
		})
		for i := 0; i < len(cands) && i < 3; i++ {
			p.Related = append(p.Related, cands[i].e)
		}
	}

	// Backlinks.
	listed := map[*Entry]bool{}
	for _, e := range append(append([]*Entry{}, posts...), pages...) {
		listed[e] = true
	}
	for _, src := range b.Entries {
		if !listed[src] {
			continue
		}
		seen := map[*Entry]bool{}
		for _, l := range src.links {
			u := strings.SplitN(l, "#", 2)[0]
			if t := b.byURL[u]; t != nil && t != src && !seen[t] {
				seen[t] = true
				t.Backlinks = append(t.Backlinks, src)
			}
		}
	}
	for _, e := range b.Entries {
		sort.SliceStable(e.Backlinks, func(i, j int) bool { return e.Backlinks[i].Date.After(e.Backlinks[j].Date) })
	}

	b.Site = &SiteData{Tags: terms, Series: seriesList, Pages: pages}
}

func (b *Build) buildSiteData() {
	cfg := b.Config
	s := b.Site
	s.Title = cfg.Site.Title
	s.Description = cfg.Site.Description
	s.URL = cfg.AbsURL(cfg.Site.BasePath)
	s.BasePath = cfg.Site.BasePath
	s.Language = cfg.Site.Language
	s.Copyright = cfg.Site.Copyright
	s.Author = AuthorData{Name: cfg.Author.Name, Email: cfg.Author.Email, URL: cfg.Author.URL, Avatar: cfg.Author.Avatar}
	s.Social = cfg.Social.Links
	s.Generator = "Skimdit " + version.Version
	s.Dev = b.Opts.Dev
	if cfg.Site.Intro != "" {
		doc, err := b.md.Render([]byte(cfg.Site.Intro), markdown.Hooks{})
		if err != nil {
			b.diag(config.FileName, 0, false, "site.intro: %v", err)
		} else {
			s.Intro = template.HTML(doc.HTML)
		}
	}
	if len(b.Posts) > 0 {
		s.Year = b.Posts[0].Date.Year()
	}
	s.Features = Features{
		Search:       cfg.Search.Enabled,
		TagPages:     cfg.Taxonomies.TagPages,
		Comments:     cfg.Comments.Enabled,
		ReplyByEmail: cfg.Comments.ReplyByEmail,
		Newsletter:   cfg.Newsletter.Enabled,
		Webmentions:  cfg.Webmention.Receive,
		Fediverse:    cfg.Fediverse.Enabled,
	}
	if cfg.Fediverse.Enabled {
		s.Features.FediverseHandle = "@" + cfg.Fediverse.Handle + "@" + cfg.Domain()
	}
	s.Features.API = cfg.Rel("/api/")
	if cfg.Feeds.Atom {
		s.Feeds = append(s.Feeds, FeedLink{Title: cfg.Site.Title + " (Atom)", URL: cfg.Rel("/feed.xml"), Type: "application/atom+xml"})
	}
	if cfg.Feeds.RSS {
		s.Feeds = append(s.Feeds, FeedLink{Title: cfg.Site.Title + " (RSS)", URL: cfg.Rel("/rss.xml"), Type: "application/rss+xml"})
	}
	if cfg.Feeds.JSON {
		s.Feeds = append(s.Feeds, FeedLink{Title: cfg.Site.Title + " (JSON Feed)", URL: cfg.Rel("/feed.json"), Type: "application/feed+json"})
	}
	var css strings.Builder
	css.WriteString("/* Generated from config.toml */\n")
	if a := strings.TrimSpace(cfg.Theme.Accent); a != "" {
		if strings.ContainsAny(a, ";{}<>\\") {
			b.diag(config.FileName, 0, false, "theme.accent must be a plain CSS colour such as \"#7c5cff\"")
		} else {
			fmt.Fprintf(&css, ":root{--accent:%s}\n", a)
		}
	}
	b.siteCSS = css.String()
}

// write stores one output file. p is a path relative to the output root.
func (b *Build) write(p string, data []byte, who string) error {
	p = strings.TrimPrefix(path.Clean("/"+p), "/")
	b.fmu.Lock()
	if prev, ok := b.files[p]; ok {
		b.fmu.Unlock()
		b.diag("", 0, false, "two things want the same output file %s: %s and %s", p, prev, who)
		return nil
	}
	b.files[p] = who
	b.fmu.Unlock()
	full := filepath.Join(b.tmp, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

// WriteFile lets later build stages and plugins add an output file.
func (b *Build) WriteFile(p string, data []byte, who string) error { return b.write(p, data, who) }

// Tmp is the folder being built (before the swap).
func (b *Build) Tmp() string { return b.tmp }

// urlFile maps a site URL (with base path) to an output path.
func (b *Build) urlFile(u string) string {
	p := strings.TrimPrefix(u, b.Config.Site.BasePath)
	if p == "" || strings.HasSuffix(p, "/") {
		p += "index.html"
	}
	return p
}

func (b *Build) writeAssets() error {
	names, err := b.Theme.Assets()
	if err != nil {
		return err
	}
	dir := strings.TrimPrefix(b.Theme.AssetDir(), "/")
	for _, n := range names {
		data, err := b.Theme.ReadAsset(n)
		if err != nil {
			return err
		}
		if err := b.write(dir+n, data, "theme asset"); err != nil {
			return err
		}
	}
	sum := sha256.Sum256([]byte(b.siteCSS))
	b.siteCSSPath = "assets/site-" + hex.EncodeToString(sum[:5]) + ".css"
	return b.write(b.siteCSSPath, []byte(b.siteCSS), "config")
}

func (b *Build) writeStatic() error {
	root := filepath.Join(b.Config.Root, "static")
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return b.write(filepath.ToSlash(rel), data, "static/"+filepath.ToSlash(rel))
	})
}

func (b *Build) writeCopies() error {
	urls := make([]string, 0, len(b.copies))
	for u := range b.copies {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	for _, u := range urls {
		data, err := os.ReadFile(b.copies[u])
		if err != nil {
			return err
		}
		if err := b.write(strings.TrimPrefix(u, b.Config.Site.BasePath), data, "co-located file"); err != nil {
			return err
		}
	}
	return nil
}

func (b *Build) writeMedia() error {
	seen := map[*media.Result]bool{}
	names := make([]string, 0, len(b.mediaRes))
	for n := range b.mediaRes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		res := b.mediaRes[n]
		if seen[res] {
			continue
		}
		seen[res] = true
		if err := b.media.CopyTo(res, filepath.Join(b.tmp, "media")); err != nil {
			return err
		}
		for _, v := range res.Variants {
			b.fmu.Lock()
			b.files["media/"+v.Name] = "image"
			b.fmu.Unlock()
		}
	}
	return nil
}

// Media returns the processed-image lookup, for social cards.
func (b *Build) Media() *media.Pipeline { return b.media }

// Markdown returns the renderer, for the editor preview.
func (b *Build) Markdown() *markdown.Renderer { return b.md }
