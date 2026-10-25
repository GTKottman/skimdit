// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (b *Build) funcs() template.FuncMap {
	return template.FuncMap{
		"t": func(key string, pairs ...any) string { return b.Locale.T(key, pairs...) },
		"date": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return b.Locale.Date(t)
		},
		"monthName": func(m int) string { return b.Locale.MonthName(m) },
		"isoDate": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Format(time.RFC3339)
		},
		"dateOnly": func(t time.Time) string { return t.Format("2006-01-02") },
		// rel prefixes a site path with the base path.
		"rel": func(p string) string { return b.Config.Rel(p) },
		// abs makes a site path (with base path) absolute.
		"abs": func(p string) string { return b.Config.AbsURL(p) },
		"asset": func(name string) string {
			return b.Config.Rel(b.Theme.AssetDir() + strings.TrimPrefix(name, "/"))
		},
		"postsCount": func(n int) string {
			if n == 1 {
				return b.Locale.T("posts_count_one")
			}
			return b.Locale.T("posts_count", "n", n)
		},
		"json": func(v any) (template.JS, error) {
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(true)
			if err := enc.Encode(v); err != nil {
				return "", err
			}
			return template.JS(strings.TrimSpace(buf.String())), nil
		},
		"add":   func(a, c int) int { return a + c },
		"sub":   func(a, c int) int { return a - c },
		"lower": strings.ToLower,
		"first": func(n int, es []*Entry) []*Entry {
			if len(es) > n {
				return es[:n]
			}
			return es
		},
		"mailto": func(addr, subject string) template.URL {
			return template.URL("mailto:" + addr + "?subject=" + strings.ReplaceAll(template.URLQueryEscaper(subject), "+", "%20"))
		},
		"dict": func(kv ...any) (map[string]any, error) {
			if len(kv)%2 != 0 {
				return nil, fmt.Errorf("dict needs key, value pairs")
			}
			m := map[string]any{}
			for i := 0; i < len(kv); i += 2 {
				k, ok := kv[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				m[k] = kv[i+1]
			}
			return m, nil
		},
	}
}

func (b *Build) page(kind, title, url string) *Page {
	cfg := b.Config
	p := &Page{
		Site:        b.Site,
		Kind:        kind,
		Title:       title,
		Description: cfg.Site.Description,
		URL:         url,
		Permalink:   cfg.AbsURL(url),
		Lang:        cfg.Site.Language,
		Assets:      cfg.Rel(b.Theme.AssetDir()),
		SiteCSS:     cfg.Rel("/" + b.siteCSSPath),
		Recent:      firstN(b.Posts, 5),
	}
	p.Canonical = p.Permalink
	p.NoIndex = b.staging()
	if cfg.Social.Image != "" {
		p.Image = cfg.AbsURL(cfg.Rel("/" + strings.TrimPrefix(cfg.Social.Image, "/")))
	}
	return p
}

func firstN(es []*Entry, n int) []*Entry {
	if len(es) > n {
		return es[:n]
	}
	return es
}

func (b *Build) emit(name string, p *Page) error {
	if p.Entry != nil && p.Entry.HasEmbed || b.Site.Features.Search && p.Kind == "search" {
		p.NeedsJS = true
	}
	if p.JSONLD == "" {
		p.JSONLD = b.jsonLD(p)
	}
	var buf bytes.Buffer
	if err := b.Theme.Render(&buf, name, p); err != nil {
		return fmt.Errorf("rendering %s (%s template): %w", p.URL, name, err)
	}
	file := b.urlFile(p.URL)
	if p.Kind == "404" {
		file = "404.html"
	}
	return b.write(file, buf.Bytes(), name+" page")
}

// PageHook lets later stages fill in page fields (for example JSON-LD) before rendering.
var jsonLDHook func(b *Build, p *Page) template.JS

func (b *Build) jsonLD(p *Page) template.JS {
	if jsonLDHook != nil {
		return jsonLDHook(b, p)
	}
	return ""
}

// SetJSONLD installs the structured-data builder (set by the seo stage).
func SetJSONLD(f func(b *Build, p *Page) template.JS) { jsonLDHook = f }

func (b *Build) renderPages() error {
	cfg := b.Config
	// Entries.
	for _, e := range b.Entries {
		if e.doc == nil {
			continue
		}
		p := b.page(e.Kind, e.Title, e.URL)
		p.Entry = e
		p.Description = e.Description
		p.Lang = e.Lang
		if e.Canonical != "" {
			p.Canonical = e.Canonical
		}
		p.NoIndex = e.Unlisted || e.Status == "draft" || (e.Kind == "post" && e.Date.After(b.Now))
		if img := b.socialImage(e); img != "" {
			p.Image = img
		}
		if b.staging() {
			p.NoIndex = true
		}
		tmpl := "post"
		if e.Kind == "page" {
			tmpl = "page"
		}
		if err := b.emit(tmpl, p); err != nil {
			return err
		}
		for _, alias := range e.item.Aliases {
			if err := b.redirect(cfg.Rel(alias), e.URL); err != nil {
				return err
			}
		}
	}

	// Home, paginated.
	if err := b.paginate("home", b.Site.Title, cfg.Rel("/"), b.Posts, func(p *Page) {}); err != nil {
		return err
	}

	// Tags.
	if cfg.Taxonomies.TagPages {
		p := b.page("tags", b.Locale.T("tags"), cfg.Rel("/tags/"))
		if err := b.emit("tags", p); err != nil {
			return err
		}
		for _, t := range b.Site.Tags {
			t := t
			title := b.Locale.T("posts_tagged", "name", t.Name)
			if err := b.paginate("list", title, t.URL, t.Entries, func(p *Page) {
				p.Kind = "tag"
				p.Term = t
				p.NoIndex = cfg.Taxonomies.NoindexTags
			}); err != nil {
				return err
			}
		}
	}

	// Series.
	if len(b.Site.Series) > 0 {
		p := b.page("series-list", b.Locale.T("series"), cfg.Rel("/series/"))
		if err := b.emit("series-list", p); err != nil {
			return err
		}
		for _, s := range b.Site.Series {
			p := b.page("series", s.Name, s.URL)
			p.Series = s
			p.List = s.Entries
			if err := b.emit("list", p); err != nil {
				return err
			}
		}
	}

	// Archive.
	if err := b.renderArchive(); err != nil {
		return err
	}

	// Search.
	if cfg.Search.Enabled {
		if err := b.emit("search", b.page("search", b.Locale.T("search"), cfg.Rel("/search/"))); err != nil {
			return err
		}
	}

	// 404.
	nf := b.page("404", b.Locale.T("not_found_title"), cfg.Rel("/404.html"))
	nf.NoIndex = true
	return b.emit("404", nf)
}

func (b *Build) redirect(from, to string) error {
	if from == to {
		return nil
	}
	if _, taken := b.byURL[from]; taken {
		b.diag("", 0, false, "alias %s points at %s but a page already lives there", from, to)
		return nil
	}
	b.Redirect[from] = to
	p := b.page("redirect", b.Locale.T("redirecting", "name", to), from)
	p.Target = to
	p.Canonical = b.Config.AbsURL(to)
	p.NoIndex = true
	return b.emit("redirect", p)
}

func (b *Build) paginate(tmpl, title, base string, list []*Entry, set func(*Page)) error {
	per := b.Config.Site.PostsPerPage
	pages := (len(list) + per - 1) / per
	if pages == 0 {
		pages = 1
	}
	pageURL := func(n int) string {
		if n == 1 {
			return base
		}
		return base + "page/" + strconv.Itoa(n) + "/"
	}
	for n := 1; n <= pages; n++ {
		lo, hi := (n-1)*per, min(n*per, len(list))
		p := b.page(tmpl, title, pageURL(n))
		p.List = list[lo:hi]
		pg := &Pagination{Page: n, Pages: pages}
		if n > 1 {
			pg.PrevURL = pageURL(n - 1)
		}
		if n < pages {
			pg.NextURL = pageURL(n + 1)
		}
		for i := 1; i <= pages; i++ {
			pg.Links = append(pg.Links, PageLink{Number: i, URL: pageURL(i), Current: i == n})
		}
		p.Pagination = pg
		set(p)
		if err := b.emit(tmpl, p); err != nil {
			return err
		}
	}
	return nil
}

func (b *Build) renderArchive() error {
	cfg := b.Config
	years := map[int]*ArchiveYear{}
	for _, e := range b.Posts {
		y := e.Date.Year()
		ay := years[y]
		if ay == nil {
			ay = &ArchiveYear{Year: y, URL: cfg.Rel(fmt.Sprintf("/archive/%d/", y))}
			years[y] = ay
		}
		ay.Entries = append(ay.Entries, e)
		m := int(e.Date.Month())
		if len(ay.Months) == 0 || ay.Months[len(ay.Months)-1].Month != m {
			ay.Months = append(ay.Months, &ArchiveMonth{Year: y, Month: m, Name: b.Locale.MonthName(m), URL: cfg.Rel(fmt.Sprintf("/archive/%d/%02d/", y, m))})
		}
		am := ay.Months[len(ay.Months)-1]
		am.Entries = append(am.Entries, e)
	}
	list := make([]*ArchiveYear, 0, len(years))
	for _, y := range years {
		list = append(list, y)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Year > list[j].Year })

	all := b.page("archive", b.Locale.T("archive"), cfg.Rel("/archive/"))
	all.Archive = &Archive{Years: list}
	all.List = b.Posts
	if err := b.emit("archive", all); err != nil {
		return err
	}
	for _, y := range list {
		p := b.page("archive", b.Locale.T("posts_in_year", "name", y.Year), y.URL)
		p.Archive = &Archive{Years: []*ArchiveYear{y}, Year: y.Year}
		p.List = y.Entries
		if err := b.emit("archive", p); err != nil {
			return err
		}
		if !cfg.Taxonomies.ArchiveMonths {
			continue
		}
		for _, m := range y.Months {
			mp := b.page("archive", b.Locale.T("posts_in_year", "name", b.Locale.Month(time.Date(m.Year, time.Month(m.Month), 1, 0, 0, 0, 0, time.UTC))), m.URL)
			mp.Archive = &Archive{Years: []*ArchiveYear{{Year: y.Year, URL: y.URL, Months: []*ArchiveMonth{m}, Entries: m.Entries}}, Year: y.Year, Month: m.Month}
			mp.List = m.Entries
			if err := b.emit("archive", mp); err != nil {
				return err
			}
			if !cfg.Taxonomies.ArchiveDays {
				continue
			}
			days := map[int][]*Entry{}
			for _, e := range m.Entries {
				days[e.Date.Day()] = append(days[e.Date.Day()], e)
			}
			for d, es := range days {
				u := cfg.Rel(fmt.Sprintf("/archive/%d/%02d/%02d/", m.Year, m.Month, d))
				dp := b.page("archive", b.Locale.T("posts_in_year", "name", b.Locale.Date(time.Date(m.Year, time.Month(m.Month), d, 0, 0, 0, 0, time.UTC))), u)
				dp.Archive = &Archive{Year: m.Year, Month: m.Month, Day: d}
				dp.List = es
				if err := b.emit("archive", dp); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
