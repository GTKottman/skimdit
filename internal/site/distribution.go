// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/GTKottman/skimdit/internal/cards"
	"golang.org/x/text/unicode/norm"
)

func (b *Build) searchIndexIfOn() error {
	if !b.Config.Search.Enabled {
		return nil
	}
	return b.searchIndex()
}

// ---- absolute URLs in feed content ----

var (
	attrURLRe     = regexp.MustCompile(`\b(href|src|poster)="(/[^"]*)"`)
	srcsetRe      = regexp.MustCompile(`\bsrcset="([^"]*)"`)
	headingAnchor = regexp.MustCompile(` ?<a class="heading-anchor"[^>]*>#</a>`)
)

// absolutize makes site paths in HTML absolute and drops heading anchors,
// so the HTML works in feed readers and email.
func (b *Build) absolutize(h string) string {
	h = headingAnchor.ReplaceAllString(h, "")
	h = attrURLRe.ReplaceAllStringFunc(h, func(m string) string {
		sub := attrURLRe.FindStringSubmatch(m)
		if strings.HasPrefix(sub[2], "//") {
			return m
		}
		return sub[1] + `="` + b.Config.AbsURL(sub[2]) + `"`
	})
	return srcsetRe.ReplaceAllStringFunc(h, func(m string) string {
		parts := strings.Split(srcsetRe.FindStringSubmatch(m)[1], ",")
		for i, p := range parts {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") {
				p = b.Config.AbsURL(p)
			}
			parts[i] = p
		}
		return `srcset="` + strings.Join(parts, ", ") + `"`
	})
}

// FeedHTML is an entry's content ready for feeds and email.
func (b *Build) FeedHTML(e *Entry) string { return b.absolutize(string(e.Content)) }

// entryID is a stable feed id: a tag URI from the domain, the first
// publication date and the slug. It never changes when the URL pattern does.
func (b *Build) entryID(e *Entry) string {
	return fmt.Sprintf("tag:%s,%s:%s", b.Config.Domain(), e.Date.Format("2006-01-02"), e.Slug)
}

func lastChange(e *Entry) time.Time {
	if !e.Updated.IsZero() && e.Updated.After(e.Date) {
		return e.Updated
	}
	return e.Date
}

// ---- feeds ----

type feedSet struct {
	title, path, selfURL, pageURL string
	entries                       []*Entry
}

func (b *Build) feeds() error {
	cfg := b.Config
	limit := func(es []*Entry) []*Entry {
		if cfg.Feeds.Limit > 0 && len(es) > cfg.Feeds.Limit {
			return es[:cfg.Feeds.Limit]
		}
		return es
	}
	main := feedSet{title: cfg.Site.Title, pageURL: cfg.Rel("/"), entries: limit(b.Posts)}
	if cfg.Feeds.Atom || cfg.Feeds.RSS {
		if err := b.write("feed.xsl", []byte(feedXSL), "feed stylesheet"); err != nil {
			return err
		}
	}
	if cfg.Feeds.Atom {
		f := main
		f.path = "feed.xml"
		if err := b.atom(f); err != nil {
			return err
		}
	}
	if cfg.Feeds.RSS {
		f := main
		f.path = "rss.xml"
		if err := b.rss(f); err != nil {
			return err
		}
	}
	if cfg.Feeds.JSON {
		f := main
		f.path = "feed.json"
		if err := b.jsonFeed(f); err != nil {
			return err
		}
	}
	if cfg.Feeds.PerTag && cfg.Taxonomies.TagPages {
		for _, t := range b.Site.Tags {
			f := feedSet{title: cfg.Site.Title + ": " + t.Name, path: b.urlFile(t.URL+"feed.xml"), pageURL: t.URL, entries: limit(t.Entries)}
			if err := b.atom(f); err != nil {
				return err
			}
		}
	}
	if cfg.Feeds.PerSeries {
		for _, s := range b.Site.Series {
			es := append([]*Entry(nil), s.Entries...)
			sort.SliceStable(es, func(i, j int) bool { return es[i].Date.After(es[j].Date) })
			f := feedSet{title: cfg.Site.Title + ": " + s.Name, path: b.urlFile(s.URL+"feed.xml"), pageURL: s.URL, entries: limit(es)}
			if err := b.atom(f); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Build) feedUpdated(es []*Entry) time.Time {
	var t time.Time
	for _, e := range es {
		if c := lastChange(e); c.After(t) {
			t = c
		}
	}
	if t.IsZero() {
		t = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return t
}

type xmlText struct {
	Type string `xml:"type,attr,omitempty"`
	Body string `xml:",chardata"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr,omitempty"`
	Type string `xml:"type,attr,omitempty"`
}

type atomPerson struct {
	Name  string `xml:"name"`
	URI   string `xml:"uri,omitempty"`
	Email string `xml:"email,omitempty"`
}

type atomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr,omitempty"`
}

type atomEntry struct {
	Title      xmlText        `xml:"title"`
	Links      []atomLink     `xml:"link"`
	ID         string         `xml:"id"`
	Published  string         `xml:"published"`
	Updated    string         `xml:"updated"`
	Author     *atomPerson    `xml:"author,omitempty"`
	Categories []atomCategory `xml:"category"`
	Summary    *xmlText       `xml:"summary,omitempty"`
	Content    *xmlText       `xml:"content,omitempty"`
}

type atomFeed struct {
	XMLName   xml.Name    `xml:"http://www.w3.org/2005/Atom feed"`
	Lang      string      `xml:"xml:lang,attr,omitempty"`
	Title     xmlText     `xml:"title"`
	Subtitle  *xmlText    `xml:"subtitle,omitempty"`
	Links     []atomLink  `xml:"link"`
	ID        string      `xml:"id"`
	Updated   string      `xml:"updated"`
	Author    *atomPerson `xml:"author,omitempty"`
	Generator string      `xml:"generator"`
	Icon      string      `xml:"icon,omitempty"`
	Entries   []atomEntry `xml:"entry"`
}

const xmlHeader = `<?xml version="1.0" encoding="utf-8"?>` + "\n"

func (b *Build) writeXML(path string, v any, stylesheet bool) error {
	var buf bytes.Buffer
	buf.WriteString(xmlHeader)
	if stylesheet {
		fmt.Fprintf(&buf, `<?xml-stylesheet href="%s" type="text/xsl"?>`+"\n", b.Config.Rel("/feed.xsl"))
	}
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	buf.WriteByte('\n')
	// Validate: the output must parse back.
	if err := xml.Unmarshal(buf.Bytes(), new(struct{})); err != nil {
		return fmt.Errorf("%s is not well-formed XML: %w", path, err)
	}
	return b.write(path, buf.Bytes(), "feed")
}

func (b *Build) atom(f feedSet) error {
	cfg := b.Config
	author := &atomPerson{Name: cfg.Author.Name, URI: cfg.Author.URL}
	if author.Name == "" {
		author.Name = cfg.Site.Title
	}
	feed := atomFeed{
		Lang:      cfg.Site.Language,
		Title:     xmlText{Type: "text", Body: f.title},
		ID:        cfg.AbsURL(cfg.Rel("/" + f.path)),
		Updated:   b.feedUpdated(f.entries).Format(time.RFC3339),
		Author:    author,
		Generator: b.Site.Generator,
		Links: []atomLink{
			{Href: cfg.AbsURL(cfg.Rel("/" + f.path)), Rel: "self", Type: "application/atom+xml"},
			{Href: cfg.AbsURL(f.pageURL), Rel: "alternate", Type: "text/html"},
		},
	}
	if cfg.Site.Description != "" {
		feed.Subtitle = &xmlText{Type: "text", Body: cfg.Site.Description}
	}
	for _, e := range f.entries {
		ae := atomEntry{
			Title:     xmlText{Type: "text", Body: e.Title},
			Links:     []atomLink{{Href: e.Permalink, Rel: "alternate", Type: "text/html"}},
			ID:        b.entryID(e),
			Published: e.Date.Format(time.RFC3339),
			Updated:   lastChange(e).Format(time.RFC3339),
		}
		for _, t := range e.Tags {
			ae.Categories = append(ae.Categories, atomCategory{Term: t.Slug, Label: t.Name})
		}
		if e.Excerpt != "" {
			ae.Summary = &xmlText{Type: "text", Body: e.Excerpt}
		}
		if cfg.Feeds.FullContent {
			ae.Content = &xmlText{Type: "html", Body: b.FeedHTML(e)}
		}
		feed.Entries = append(feed.Entries, ae)
	}
	return b.writeXML(f.path, feed, true)
}

type rssGUID struct {
	IsPermaLink string `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	GUID        rssGUID  `xml:"guid"`
	PubDate     string   `xml:"pubDate"`
	Description string   `xml:"description"`
	Categories  []string `xml:"category"`
}

type rssAtomLink struct {
	Href    string   `xml:"href,attr"`
	Rel     string   `xml:"rel,attr"`
	Type    string   `xml:"type,attr"`
}

type rssChannel struct {
	Title         string      `xml:"title"`
	Link          string      `xml:"link"`
	Description   string      `xml:"description"`
	Language      string      `xml:"language,omitempty"`
	LastBuildDate string      `xml:"lastBuildDate"`
	Generator     string      `xml:"generator"`
	AtomLink      rssAtomLink `xml:"http://www.w3.org/2005/Atom link"`
	Items         []rssItem   `xml:"item"`
}

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Channel rssChannel `xml:"channel"`
}

func (b *Build) rss(f feedSet) error {
	cfg := b.Config
	desc := cfg.Site.Description
	if desc == "" {
		desc = cfg.Site.Title
	}
	ch := rssChannel{
		Title:         f.title,
		Link:          cfg.AbsURL(f.pageURL),
		Description:   desc,
		Language:      cfg.Site.Language,
		LastBuildDate: b.feedUpdated(f.entries).Format(time.RFC1123Z),
		Generator:     b.Site.Generator,
		AtomLink:      rssAtomLink{Href: cfg.AbsURL(cfg.Rel("/" + f.path)), Rel: "self", Type: "application/rss+xml"},
	}
	for _, e := range f.entries {
		it := rssItem{
			Title:   e.Title,
			Link:    e.Permalink,
			GUID:    rssGUID{IsPermaLink: "false", Value: b.entryID(e)},
			PubDate: e.Date.Format(time.RFC1123Z),
		}
		if cfg.Feeds.FullContent {
			it.Description = b.FeedHTML(e)
		} else {
			it.Description = template.HTMLEscapeString(e.Excerpt)
		}
		for _, t := range e.Tags {
			it.Categories = append(it.Categories, t.Name)
		}
		ch.Items = append(ch.Items, it)
	}
	return b.writeXML(f.path, rssFeed{Version: "2.0", Channel: ch}, true)
}

type jsonAuthor struct {
	Name   string `json:"name"`
	URL    string `json:"url,omitempty"`
	Avatar string `json:"avatar,omitempty"`
}

type jsonItem struct {
	ID            string   `json:"id"`
	URL           string   `json:"url"`
	Title         string   `json:"title"`
	ContentHTML   string   `json:"content_html,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	Image         string   `json:"image,omitempty"`
	DatePublished string   `json:"date_published"`
	DateModified  string   `json:"date_modified,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	Language      string   `json:"language,omitempty"`
}

type jsonFeedDoc struct {
	Version     string       `json:"version"`
	Title       string       `json:"title"`
	HomePageURL string       `json:"home_page_url"`
	FeedURL     string       `json:"feed_url"`
	Description string       `json:"description,omitempty"`
	Language    string       `json:"language,omitempty"`
	Authors     []jsonAuthor `json:"authors,omitempty"`
	Items       []jsonItem   `json:"items"`
}

func (b *Build) jsonFeed(f feedSet) error {
	cfg := b.Config
	doc := jsonFeedDoc{
		Version:     "https://jsonfeed.org/version/1.1",
		Title:       f.title,
		HomePageURL: cfg.AbsURL(f.pageURL),
		FeedURL:     cfg.AbsURL(cfg.Rel("/" + f.path)),
		Description: cfg.Site.Description,
		Language:    cfg.Site.Language,
		Items:       []jsonItem{},
	}
	if cfg.Author.Name != "" {
		doc.Authors = []jsonAuthor{{Name: cfg.Author.Name, URL: cfg.Author.URL, Avatar: cfg.Author.Avatar}}
	}
	for _, e := range f.entries {
		it := jsonItem{
			ID:            b.entryID(e),
			URL:           e.Permalink,
			Title:         e.Title,
			Summary:       e.Excerpt,
			DatePublished: e.Date.Format(time.RFC3339),
			Language:      e.Lang,
		}
		if !e.Updated.IsZero() {
			it.DateModified = e.Updated.Format(time.RFC3339)
		}
		if cfg.Feeds.FullContent {
			it.ContentHTML = b.FeedHTML(e)
		}
		if img := b.socialImage(e); img != "" {
			it.Image = img
		}
		for _, t := range e.Tags {
			it.Tags = append(it.Tags, t.Name)
		}
		doc.Items = append(doc.Items, it)
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return b.write(f.path, append(out, '\n'), "feed")
}

// ---- sitemap ----

type sitemapURL struct {
	Loc     string `xml:"loc"`
	LastMod string `xml:"lastmod,omitempty"`
}

type sitemapDoc struct {
	XMLName xml.Name     `xml:"http://www.sitemaps.org/schemas/sitemap/0.9 urlset"`
	URLs    []sitemapURL `xml:"url"`
}

func (b *Build) sitemap() error {
	cfg := b.Config
	doc := sitemapDoc{}
	add := func(u string, t time.Time) {
		su := sitemapURL{Loc: cfg.AbsURL(u)}
		if !t.IsZero() {
			su.LastMod = t.Format("2006-01-02")
		}
		doc.URLs = append(doc.URLs, su)
	}
	var newest time.Time
	if len(b.Posts) > 0 {
		newest = lastChange(b.Posts[0])
	}
	add(cfg.Rel("/"), newest)
	for _, e := range b.Posts {
		if e.Canonical == "" {
			add(e.URL, lastChange(e))
		}
	}
	for _, e := range b.Pages {
		if e.Canonical == "" {
			add(e.URL, e.Updated)
		}
	}
	if cfg.Taxonomies.TagPages && !cfg.Taxonomies.NoindexTags {
		for _, t := range b.Site.Tags {
			add(t.URL, b.feedUpdated(t.Entries))
		}
	}
	for _, s := range b.Site.Series {
		add(s.URL, b.feedUpdated(s.Entries))
	}
	return b.writeXML("sitemap.xml", doc, false)
}

// ---- robots.txt ----

// AI crawlers that collect training data. Search and answer bots that fetch
// pages to cite them are listed separately.
var (
	aiTrainingBots = []string{"GPTBot", "ClaudeBot", "anthropic-ai", "CCBot", "Google-Extended", "Applebot-Extended",
		"Meta-ExternalAgent", "Bytespider", "cohere-training-data-crawler", "Diffbot", "Omgilibot", "Timpibot", "AI2Bot"}
	aiAnswerBots = []string{"ChatGPT-User", "OAI-SearchBot", "Claude-User", "Claude-SearchBot", "PerplexityBot",
		"Perplexity-User", "cohere-ai", "MistralAI-User", "DuckAssistBot"}
)

// Staging is set (SKIMDIT_STAGING=1) for any build that is not the real site:
// every crawler is turned away and every page is marked noindex.
func (b *Build) staging() bool { return os.Getenv("SKIMDIT_STAGING") == "1" }

func (b *Build) robots() error {
	cfg := b.Config
	var r strings.Builder
	r.WriteString("# robots.txt for " + cfg.AbsURL(cfg.Site.BasePath) + "\n")
	r.WriteString("# These are requests; well-behaved crawlers follow them.\n\n")
	if b.staging() {
		r.WriteString("User-agent: *\nDisallow: /\n")
		return b.write("robots.txt", []byte(r.String()), "robots")
	}
	block := func(bots []string) {
		for _, bot := range bots {
			r.WriteString("User-agent: " + bot + "\n")
		}
		r.WriteString("Disallow: /\n\n")
	}
	switch cfg.Robots.AI {
	case "block-training":
		r.WriteString("# AI training crawlers are asked to stay out.\n")
		block(aiTrainingBots)
	case "block-all":
		r.WriteString("# All AI crawlers are asked to stay out.\n")
		block(append(append([]string{}, aiTrainingBots...), aiAnswerBots...))
	}
	r.WriteString("User-agent: *\n")
	for _, p := range []string{"/admin/", "/api/"} {
		r.WriteString("Disallow: " + cfg.Rel(p) + "\n")
	}
	for _, p := range cfg.Robots.Disallow {
		r.WriteString("Disallow: " + cfg.Rel("/"+strings.TrimPrefix(p, "/")) + "\n")
	}
	r.WriteString("Allow: " + cfg.Site.BasePath + "\n\n")
	r.WriteString("Sitemap: " + cfg.AbsURL(cfg.Rel("/sitemap.xml")) + "\n")
	return b.write("robots.txt", []byte(r.String()), "robots")
}

// ---- search index ----

type searchEntry struct {
	URL     string `json:"u"`
	Title   string `json:"title"`
	Date    string `json:"d"`
	Excerpt string `json:"excerpt"`
	T       string `json:"t"` // normalized title words
	G       string `json:"g"` // normalized tags
	X       string `json:"x"` // normalized excerpt words
	B       string `json:"b"` // normalized body words (capped)
}

const (
	searchChunk     = 200
	searchBodyWords = 400
)

// SearchWords normalizes text the same way the theme's search script does:
// lower case, accents removed, split on anything that is not a letter or digit.
func SearchWords(s string) []string {
	var b strings.Builder
	for _, r := range norm.NFKD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Fields(b.String())
}

func (b *Build) searchIndex() error {
	entries := append(append([]*Entry{}, b.Posts...), b.Pages...)
	var all []searchEntry
	for _, e := range entries {
		seen := map[string]bool{}
		var body []string
		for _, w := range SearchWords(stripTags(string(e.Content))) {
			if len(w) < 3 || seen[w] {
				continue
			}
			seen[w] = true
			body = append(body, w)
			if len(body) >= searchBodyWords {
				break
			}
		}
		var tags []string
		for _, t := range e.Tags {
			tags = append(tags, SearchWords(t.Name)...)
		}
		date := ""
		if e.Kind == "post" {
			date = b.Locale.Date(e.Date)
		}
		all = append(all, searchEntry{
			URL: e.URL, Title: e.Title, Date: date, Excerpt: e.Excerpt,
			T: strings.Join(SearchWords(e.Title), " "), G: strings.Join(tags, " "),
			X: strings.Join(SearchWords(e.Excerpt), " "), B: strings.Join(body, " "),
		})
	}
	var chunks []string
	for i := 0; i < len(all) || i == 0; i += searchChunk {
		end := min(i+searchChunk, len(all))
		name := fmt.Sprintf("search/index-%d.json", i/searchChunk)
		data, err := json.Marshal(map[string]any{"entries": all[i:end]})
		if err != nil {
			return err
		}
		if err := b.write(name, data, "search index"); err != nil {
			return err
		}
		chunks = append(chunks, b.Config.Rel("/"+name))
		if len(all) == 0 {
			break
		}
	}
	manifest, _ := json.Marshal(map[string]any{"chunks": chunks, "count": len(all)})
	return b.write("search-index.json", manifest, "search index")
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func stripTags(s string) string { return html.UnescapeString(tagRe.ReplaceAllString(s, " ")) }

// ---- social cards ----

func (b *Build) socialImage(e *Entry) string {
	if e.socialImage != "" {
		return e.socialImage
	}
	if e.Cover != nil {
		return e.Cover.AbsURL
	}
	if b.Config.Social.Image != "" {
		return b.Config.AbsURL(b.Config.Rel("/" + strings.TrimPrefix(b.Config.Social.Image, "/")))
	}
	return ""
}

func (b *Build) socialCards() error {
	cfg := b.Config
	accent, ok := cards.ParseHex(cfg.Theme.Accent)
	if !ok {
		accent, _ = cards.ParseHex("#7fb0ff")
	}
	cacheDir := filepath.Join(cfg.Path(cfg.Build.Cache), "cards")
	type job struct {
		e    *Entry
		card cards.Card
		key  string
	}
	var jobs []job
	for _, e := range b.Entries {
		if e.doc == nil {
			continue
		}
		if s := e.item.SocialImage; s != "" {
			img, err := b.imageHook(e)(s)
			if err != nil {
				b.diag(e.Source, 1, false, "social_image %q: %v", s, err)
				continue
			}
			e.socialImage = cfg.AbsURL(img.Src)
			continue
		}
		if !cfg.Social.Cards || e.Kind != "post" {
			continue
		}
		card := cards.Card{Title: e.Title, Site: cfg.Site.Title, Detail: b.Locale.Date(e.Date), Accent: accent}
		sum := sha256.Sum256([]byte(fmt.Sprintf("v1|%s|%s|%s|%v", card.Title, card.Site, card.Detail, card.Accent)))
		jobs = append(jobs, job{e, card, hex.EncodeToString(sum[:8])})
	}
	if len(jobs) == 0 {
		return nil
	}
	_ = os.MkdirAll(cacheDir, 0o755)
	work := make(chan job)
	errs := make(chan error, len(jobs))
	var wg sync.WaitGroup
	for i := 0; i < b.Opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range work {
				cached := filepath.Join(cacheDir, j.key+".png")
				data, err := os.ReadFile(cached)
				if err != nil {
					if data, err = cards.Render(j.card); err != nil {
						errs <- err
						continue
					}
					_ = os.WriteFile(cached, data, 0o644)
				}
				name := "social/" + j.e.Slug + "-" + j.key[:8] + ".png"
				if err := b.write(name, data, "social card"); err != nil {
					errs <- err
					continue
				}
				j.e.socialImage = cfg.AbsURL(cfg.Rel("/" + name))
			}
		}()
	}
	for _, j := range jobs {
		work <- j
	}
	close(work)
	wg.Wait()
	close(errs)
	return <-errs
}

// ---- JSON-LD ----

func init() { SetJSONLD(buildJSONLD) }

func buildJSONLD(b *Build, p *Page) template.JS {
	cfg := b.Config
	person := map[string]any{"@type": "Person", "name": cfg.Author.Name}
	if cfg.Author.URL != "" {
		person["url"] = cfg.Author.URL
	}
	if len(cfg.Social.Links) > 0 {
		person["sameAs"] = cfg.Social.Links
	}
	var doc any
	switch p.Kind {
	case "home":
		doc = map[string]any{
			"@context":    "https://schema.org",
			"@type":       "WebSite",
			"name":        cfg.Site.Title,
			"url":         b.Site.URL,
			"description": cfg.Site.Description,
			"inLanguage":  cfg.Site.Language,
			"author":      person,
			"potentialAction": map[string]any{
				"@type":       "SearchAction",
				"target":      cfg.AbsURL(cfg.Rel("/search/")) + "?q={search_term_string}",
				"query-input": "required name=search_term_string",
			},
		}
	case "post":
		e := p.Entry
		post := map[string]any{
			"@type":            "BlogPosting",
			"headline":         e.Title,
			"description":      e.Description,
			"datePublished":    e.Date.Format(time.RFC3339),
			"dateModified":     lastChange(e).Format(time.RFC3339),
			"author":           person,
			"publisher":        person,
			"url":              e.Permalink,
			"mainEntityOfPage": map[string]any{"@type": "WebPage", "@id": e.Permalink},
			"inLanguage":       e.Lang,
			"wordCount":        e.Words,
		}
		if img := b.socialImage(e); img != "" {
			post["image"] = img
		}
		var kw []string
		for _, t := range e.Tags {
			kw = append(kw, t.Name)
		}
		if len(kw) > 0 {
			post["keywords"] = strings.Join(kw, ", ")
		}
		crumbs := map[string]any{
			"@type": "BreadcrumbList",
			"itemListElement": []any{
				map[string]any{"@type": "ListItem", "position": 1, "name": cfg.Site.Title, "item": b.Site.URL},
				map[string]any{"@type": "ListItem", "position": 2, "name": e.Title, "item": e.Permalink},
			},
		}
		doc = map[string]any{"@context": "https://schema.org", "@graph": []any{post, crumbs}}
	case "page":
		doc = map[string]any{"@context": "https://schema.org", "@type": "WebPage", "name": p.Title, "url": p.Permalink, "inLanguage": p.Lang}
	case "tag", "series":
		doc = map[string]any{
			"@context": "https://schema.org",
			"@type":    "BreadcrumbList",
			"itemListElement": []any{
				map[string]any{"@type": "ListItem", "position": 1, "name": cfg.Site.Title, "item": b.Site.URL},
				map[string]any{"@type": "ListItem", "position": 2, "name": p.Title, "item": p.Permalink},
			},
		}
	default:
		return ""
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(doc); err != nil {
		return ""
	}
	return template.JS(strings.TrimSpace(buf.String()))
}
