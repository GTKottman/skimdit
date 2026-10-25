// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"html/template"
	"time"

	"github.com/GTKottman/skimdit/internal/content"
	"github.com/GTKottman/skimdit/internal/markdown"
)

// The types in this file are the theme contract (engine_api 1). Field names
// are frozen: they may be added to, never renamed or removed, without an
// engine_api bump. See docs/THEMES.md.

// SiteData describes the whole site.
type SiteData struct {
	Title       string
	Description string
	// URL is the absolute site URL including the base path, with a trailing slash.
	URL string
	// BasePath is "/" or "/blog/".
	BasePath  string
	Language  string
	Copyright string
	Intro     template.HTML
	Author    AuthorData
	Social    []string
	Pages     []*Entry
	Tags      []*Term
	Series    []*SeriesData
	Feeds     []FeedLink
	Features  Features
	Generator string
	// Dev is true in the dev server.
	Dev bool
	// Year is the year of the newest post (stable across builds).
	Year int
}

// AuthorData is the site author.
type AuthorData struct {
	Name   string
	Email  string
	URL    string
	Avatar string
}

// Features says which optional parts are on.
type Features struct {
	Search       bool
	TagPages     bool
	Comments     bool
	ReplyByEmail bool
	Newsletter   bool
	Webmentions  bool
	Fediverse    bool
	// FediverseHandle is "@name@domain" when the fediverse is on.
	FediverseHandle string
	// API is the base path of the dynamic endpoints ("" when the site is static only).
	API string
}

// FeedLink is one feed in <head> and the footer.
type FeedLink struct {
	Title string
	URL   string
	Type  string
}

// Entry is a post or a page.
type Entry struct {
	Kind        string // "post" or "page"
	Title       string
	Slug        string
	URL         string // site path including base path, e.g. "/posts/hello/"
	Permalink   string // absolute URL
	Date        time.Time
	Updated     time.Time
	Content     template.HTML
	Excerpt     string
	Description string
	Tags        []*Term
	Series      *SeriesData
	SeriesPart  int
	Words       int
	ReadingTime int
	TOC         []markdown.Heading
	ShowTOC     bool
	Cover       *ImageData
	Prev        *Entry
	Next        *Entry
	SeriesPrev  *Entry
	SeriesNext  *Entry
	Related     []*Entry
	Backlinks   []*Entry
	HasMath     bool
	HasCode     bool
	HasEmbed    bool
	Status      string
	Unlisted    bool
	Lang        string
	Canonical   string
	Comments    bool
	Extra       map[string]any
	Source      string

	item        *content.Item
	links       []string
	doc         *markdown.Doc
	socialImage string
}

// ImageData is a processed image for templates.
type ImageData struct {
	HTML   template.HTML // a <picture> element
	URL    string        // largest fallback, site path
	AbsURL string
	Width  int
	Height int
	Alt    string
}

// Term is a tag.
type Term struct {
	Name    string
	Slug    string
	URL     string
	Count   int
	Entries []*Entry
}

// SeriesData is a series of posts in reading order.
type SeriesData struct {
	Name    string
	Slug    string
	URL     string
	Entries []*Entry
}

// Pagination links the pages of a long list.
type Pagination struct {
	Page    int
	Pages   int
	PrevURL string
	NextURL string
	Links   []PageLink
}

// PageLink is one numbered page link.
type PageLink struct {
	Number  int
	URL     string
	Current bool
}

// Archive groups posts by date.
type Archive struct {
	Years []*ArchiveYear
	// Year, Month and Day are set on a single-period page.
	Year  int
	Month int
	Day   int
}

// ArchiveYear is one year of posts.
type ArchiveYear struct {
	Year    int
	URL     string
	Months  []*ArchiveMonth
	Entries []*Entry
}

// ArchiveMonth is one month of posts.
type ArchiveMonth struct {
	Year    int
	Month   int
	Name    string
	URL     string
	Entries []*Entry
}

// Page is the root object every page template receives.
type Page struct {
	Site *SiteData
	// Kind is home, post, page, tag, tags, series, series-list, archive, search, 404 or redirect.
	Kind        string
	Title       string
	Description string
	URL         string
	Permalink   string
	Canonical   string
	NoIndex     bool
	Lang        string
	// Image is the absolute URL of the social sharing image.
	Image      string
	Entry      *Entry
	List       []*Entry
	Term       *Term
	Series     *SeriesData
	Pagination *Pagination
	Archive    *Archive
	// Target is the destination of a redirect page.
	Target string
	// JSONLD is the page's structured data.
	JSONLD template.JS
	Recent []*Entry
	// Assets is the fingerprinted asset folder, e.g. "/assets/3fa2c1d0/".
	Assets  string
	SiteCSS string
	NeedsJS bool
}
