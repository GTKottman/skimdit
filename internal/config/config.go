// SPDX-License-Identifier: AGPL-3.0-or-later

// Package config loads and validates a site's config.toml.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// FileName is the config file at the root of every site.
const FileName = "config.toml"

// Config is a whole site configuration. Every key is documented in
// docs/CONFIG.md; unknown keys are rejected so typos never pass silently.
type Config struct {
	Site       Site       `toml:"site"`
	Author     Author     `toml:"author"`
	Permalinks Permalinks `toml:"permalinks"`
	Build      Build      `toml:"build"`
	Markdown   Markdown   `toml:"markdown"`
	Images     Images     `toml:"images"`
	Theme      Theme      `toml:"theme"`
	Taxonomies Taxonomies `toml:"taxonomies"`
	Check      Check      `toml:"check"`
	Feeds      Feeds      `toml:"feeds"`
	Robots     Robots     `toml:"robots"`
	Social     Social     `toml:"social"`
	Search     Search     `toml:"search"`
	Server     Server     `toml:"server"`
	Comments   Comments   `toml:"comments"`
	Newsletter Newsletter `toml:"newsletter"`
	Stats      Stats      `toml:"stats"`
	Webmention Webmention `toml:"webmention"`
	Fediverse  Fediverse  `toml:"fediverse"`
	Crosspost  Crosspost  `toml:"crosspost"`
	Plugins    []Plugin   `toml:"plugins"`

	// Root is the site directory. It is not read from the file.
	Root string `toml:"-"`
	// Location is the parsed Site.Timezone.
	Location *time.Location `toml:"-"`
	// BaseURL is the parsed Site.URL.
	BaseURL *url.URL `toml:"-"`
}

type Site struct {
	Title        string `toml:"title"`
	URL          string `toml:"url"`
	Description  string `toml:"description"`
	Language     string `toml:"language"`
	Timezone     string `toml:"timezone"`
	BasePath     string `toml:"base_path"`
	Intro        string `toml:"intro"`
	PostsPerPage int    `toml:"posts_per_page"`
	Copyright    string `toml:"copyright"`
}

type Author struct {
	Name   string `toml:"name"`
	Email  string `toml:"email"`
	URL    string `toml:"url"`
	Avatar string `toml:"avatar"`
}

type Permalinks struct {
	Post   string `toml:"post"`
	Page   string `toml:"page"`
	Tag    string `toml:"tag"`
	Series string `toml:"series"`
}

type Build struct {
	Content string `toml:"content"`
	Output  string `toml:"output"`
	Cache   string `toml:"cache"`
}

type Markdown struct {
	RawHTML     bool `toml:"raw_html"`
	Typographer bool `toml:"typographer"`
	// TOCMinHeadings is how many headings a post needs before it gets a table of contents.
	TOCMinHeadings int `toml:"toc_min_headings"`
}

type Images struct {
	Widths      []int    `toml:"widths"`
	Formats     []string `toml:"formats"`
	Quality     int      `toml:"quality"`
	RequireAlt  bool     `toml:"require_alt"`
	MaxFileSize int64    `toml:"max_file_size"`
}

type Theme struct {
	// Name is "default" or a path to a theme folder (relative to the site).
	Name string `toml:"name"`
	// Accent overrides the theme's accent colour (a CSS colour).
	Accent string `toml:"accent"`
}

type Taxonomies struct {
	TagPages      bool `toml:"tag_pages"`
	NoindexTags   bool `toml:"noindex_tags"`
	ArchiveMonths bool `toml:"archive_months"`
	ArchiveDays   bool `toml:"archive_days"`
}

type Check struct {
	// PageBudgetKB is the largest compressed HTML+CSS a page may have.
	PageBudgetKB int `toml:"page_budget_kb"`
	// JSBudgetKB is the largest compressed JavaScript a page may load.
	JSBudgetKB int `toml:"js_budget_kb"`
	// BuildBudgetSeconds fails `check` when a full build takes longer.
	BuildBudgetSeconds int `toml:"build_budget_seconds"`
}

type Feeds struct {
	Atom        bool `toml:"atom"`
	RSS         bool `toml:"rss"`
	JSON        bool `toml:"json"`
	PerTag      bool `toml:"per_tag"`
	PerSeries   bool `toml:"per_series"`
	Limit       int  `toml:"limit"`
	FullContent bool `toml:"full_content"`
}

type Robots struct {
	// AI is "allow" (all crawlers), "block-training" or "block-all".
	AI string `toml:"ai"`
	// Disallow lists extra paths every crawler is asked to skip.
	Disallow []string `toml:"disallow"`
}

type Social struct {
	Cards bool `toml:"cards"`
	// Image is the default social image (site-relative path) when cards are off.
	Image string `toml:"image"`
	// Links are profile URLs shown in the footer and in JSON-LD sameAs.
	Links []string `toml:"links"`
}

type Search struct {
	Enabled bool `toml:"enabled"`
}

type Server struct {
	Listen string `toml:"listen"`
	// Data is the directory that holds the SQLite database and uploads.
	Data string `toml:"data"`
	// Repo is the git checkout of the site the server builds from (default: the site root).
	Repo string `toml:"repo"`
	// Push makes the server push publish commits to the git remote.
	Push bool `toml:"push"`
	// Branch is the git branch publish commits go to.
	Branch string `toml:"branch"`
	// TrustProxy makes the server read the client IP from X-Forwarded-For.
	TrustProxy bool `toml:"trust_proxy"`
	// RedirectHost sends requests for any other host name (www, an IP) to the site's host.
	RedirectHost bool `toml:"redirect_host"`
}

type Comments struct {
	Enabled bool `toml:"enabled"`
	// Default is whether posts take comments when their frontmatter does not say.
	Default bool `toml:"default"`
	// ReplyByEmail shows a "reply by email" link (needs author.email).
	ReplyByEmail bool `toml:"reply_by_email"`
	MaxLinks     int  `toml:"max_links"`
	MaxLength    int  `toml:"max_length"`
	// PowBits is the proof-of-work difficulty for the comment form.
	PowBits int `toml:"pow_bits"`
}

type Newsletter struct {
	Enabled bool `toml:"enabled"`
	// From is the sender address, e.g. "Deeper Thinking <letters@deeperthinking.xyz>".
	From string `toml:"from"`
	// SMTPHost and SMTPPort point at the mail server. The password comes from SKIMDIT_SMTP_PASSWORD.
	SMTPHost     string `toml:"smtp_host"`
	SMTPPort     int    `toml:"smtp_port"`
	SMTPUser     string `toml:"smtp_user"`
	SMTPSecurity string `toml:"smtp_security"`
	// BounceIMAP is the IMAP host:port of the mailbox that receives bounces (password: SKIMDIT_IMAP_PASSWORD).
	BounceIMAP string `toml:"bounce_imap"`
	BounceUser string `toml:"bounce_user"`
	// Content is "full" or "excerpt".
	Content string `toml:"content"`
	// BatchSize and BatchPauseSeconds throttle sending.
	BatchSize         int `toml:"batch_size"`
	BatchPauseSeconds int `toml:"batch_pause_seconds"`
}

type Stats struct {
	Enabled bool `toml:"enabled"`
}

type Webmention struct {
	Send    bool `toml:"send"`
	Receive bool `toml:"receive"`
}

type Fediverse struct {
	Enabled bool `toml:"enabled"`
	// Handle is the user part of @handle@domain.
	Handle  string `toml:"handle"`
	Summary string `toml:"summary"`
}

type Crosspost struct {
	Mastodon MastodonTarget `toml:"mastodon"`
	Bluesky  BlueskyTarget  `toml:"bluesky"`
}

type MastodonTarget struct {
	Enabled bool `toml:"enabled"`
	// Instance is e.g. "https://mastodon.social". The token comes from SKIMDIT_MASTODON_TOKEN.
	Instance string `toml:"instance"`
}

type BlueskyTarget struct {
	Enabled bool `toml:"enabled"`
	// Handle is e.g. "name.bsky.social". The app password comes from SKIMDIT_BLUESKY_PASSWORD.
	Handle string `toml:"handle"`
	PDS    string `toml:"pds"`
}

type Plugin struct {
	// Path is the .wasm file, relative to the site.
	Path string `toml:"path"`
	// Config is passed to the plugin as-is.
	Config map[string]any `toml:"config"`
}

// Default returns a config with every default filled in.
func Default() Config {
	return Config{
		Site: Site{
			Language:     "en",
			Timezone:     "UTC",
			PostsPerPage: 10,
		},
		Permalinks: Permalinks{
			Post:   "/posts/:slug/",
			Page:   "/:slug/",
			Tag:    "/tags/:slug/",
			Series: "/series/:slug/",
		},
		Build: Build{
			Content: "content",
			Output:  "dist",
			Cache:   ".skimdit/cache",
		},
		Markdown: Markdown{
			RawHTML:        true,
			Typographer:    true,
			TOCMinHeadings: 3,
		},
		Images: Images{
			Widths:      []int{480, 800, 1200, 1600},
			Formats:     []string{"avif", "webp"},
			Quality:     70,
			RequireAlt:  true,
			MaxFileSize: 25 << 20,
		},
		Theme:      Theme{Name: "default"},
		Taxonomies: Taxonomies{TagPages: true, ArchiveMonths: true, ArchiveDays: false},
		Check:      Check{PageBudgetKB: 30, JSBudgetKB: 100, BuildBudgetSeconds: 0},
		Feeds:      Feeds{Atom: true, RSS: true, JSON: true, PerTag: true, PerSeries: true, Limit: 20, FullContent: true},
		Robots:     Robots{AI: "allow"},
		Social:     Social{Cards: true},
		Search:     Search{Enabled: true},
		Server: Server{
			Listen:       ":8080",
			Data:         ".skimdit/data",
			Branch:       "main",
			RedirectHost: true,
		},
		Comments: Comments{
			Enabled:      true,
			Default:      true,
			ReplyByEmail: true,
			MaxLinks:     2,
			MaxLength:    5000,
			PowBits:      18,
		},
		Newsletter: Newsletter{
			SMTPPort:          465,
			SMTPSecurity:      "tls",
			Content:           "full",
			BatchSize:         20,
			BatchPauseSeconds: 30,
		},
		Stats:      Stats{Enabled: true},
		Webmention: Webmention{Send: true, Receive: true},
		Crosspost:  Crosspost{Bluesky: BlueskyTarget{PDS: "https://bsky.social"}},
	}
}

// Load reads <root>/config.toml over the defaults and validates it.
func Load(root string) (*Config, error) {
	cfg := Default()
	path := filepath.Join(root, FileName)
	md, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s: not found (run `skimdit init` to create a site)", path)
		}
		var perr toml.ParseError
		if errors.As(err, &perr) {
			return nil, fmt.Errorf("%s:%d: %s", path, perr.Position.Line, perr.Message)
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, 0, len(und))
		for _, k := range und {
			// Plugin config tables are free-form.
			if len(k) > 0 && k[0] == "plugins" {
				continue
			}
			keys = append(keys, k.String())
		}
		if len(keys) > 0 {
			sort.Strings(keys)
			return nil, fmt.Errorf("%s: unknown key(s): %s", path, strings.Join(keys, ", "))
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cfg.Root = abs
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

	if strings.TrimSpace(c.Site.Title) == "" {
		add("site.title is required")
	}
	u, err := url.Parse(c.Site.URL)
	if c.Site.URL == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		add("site.url must be an absolute http(s) URL, e.g. \"https://example.com\"")
	} else {
		if u.RawQuery != "" || u.Fragment != "" {
			add("site.url must not have a query or fragment")
		}
		if c.Site.BasePath == "" {
			c.Site.BasePath = u.Path
		}
		u.Path = ""
		c.BaseURL = u
	}
	c.Site.BasePath = normalizeBasePath(c.Site.BasePath)

	loc, err := time.LoadLocation(c.Site.Timezone)
	if err != nil {
		add("site.timezone %q is not a known IANA timezone (e.g. \"America/Chicago\")", c.Site.Timezone)
	} else {
		c.Location = loc
	}
	if c.Site.PostsPerPage < 1 {
		add("site.posts_per_page must be at least 1")
	}
	for name, p := range map[string]string{
		"permalinks.post":   c.Permalinks.Post,
		"permalinks.page":   c.Permalinks.Page,
		"permalinks.tag":    c.Permalinks.Tag,
		"permalinks.series": c.Permalinks.Series,
	} {
		if err := validatePermalink(p); err != nil {
			add("%s: %v", name, err)
		}
	}
	for _, w := range c.Images.Widths {
		if w < 16 || w > 8192 {
			add("images.widths: %d is out of range (16-8192)", w)
		}
	}
	sort.Ints(c.Images.Widths)
	for _, f := range c.Images.Formats {
		if f != "avif" && f != "webp" {
			add("images.formats: %q is not supported (use \"avif\" and/or \"webp\")", f)
		}
	}
	if c.Images.Quality < 1 || c.Images.Quality > 100 {
		add("images.quality must be 1-100")
	}
	switch c.Robots.AI {
	case "allow", "block-training", "block-all":
	default:
		add("robots.ai must be \"allow\", \"block-training\" or \"block-all\"")
	}
	switch c.Newsletter.Content {
	case "full", "excerpt":
	default:
		add("newsletter.content must be \"full\" or \"excerpt\"")
	}
	switch c.Newsletter.SMTPSecurity {
	case "tls", "starttls", "none":
	default:
		add("newsletter.smtp_security must be \"tls\", \"starttls\" or \"none\"")
	}
	if c.Newsletter.Enabled && (c.Newsletter.From == "" || c.Newsletter.SMTPHost == "") {
		add("newsletter.enabled needs newsletter.from and newsletter.smtp_host")
	}
	if c.Fediverse.Enabled && !validHandle(c.Fediverse.Handle) {
		add("fediverse.handle must be letters, digits, '_' or '.', e.g. \"gerrod\"")
	}
	if c.Crosspost.Mastodon.Enabled && !strings.HasPrefix(c.Crosspost.Mastodon.Instance, "https://") {
		add("crosspost.mastodon.instance must be an https URL")
	}
	if c.Crosspost.Bluesky.Enabled && c.Crosspost.Bluesky.Handle == "" {
		add("crosspost.bluesky.handle is required when Bluesky cross-posting is on")
	}
	if c.Comments.ReplyByEmail && c.Author.Email == "" {
		c.Comments.ReplyByEmail = false
	}
	for i, p := range c.Plugins {
		if !strings.HasSuffix(p.Path, ".wasm") {
			add("plugins[%d].path must point at a .wasm file", i)
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func normalizeBasePath(p string) string {
	p = "/" + strings.Trim(p, "/") + "/"
	if p == "//" {
		return "/"
	}
	return p
}

func validatePermalink(p string) error {
	if !strings.HasPrefix(p, "/") {
		return errors.New("must start with /")
	}
	if !strings.Contains(p, ":slug") {
		return errors.New("must contain :slug")
	}
	if !strings.HasSuffix(p, "/") && !strings.HasSuffix(p, ".html") {
		return errors.New("must end with / or .html")
	}
	rest := p
	for _, tok := range []string{":slug", ":year", ":month", ":day"} {
		rest = strings.ReplaceAll(rest, tok, "")
	}
	if strings.Contains(rest, ":") {
		return errors.New("only :slug, :year, :month and :day are allowed")
	}
	return nil
}

func validHandle(h string) bool {
	if h == "" || len(h) > 64 {
		return false
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// Path resolves a site-relative path.
func (c *Config) Path(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(c.Root, rel)
}

// AbsURL turns a site path such as "/posts/x/" into a full URL. The path
// must already include the base path.
func (c *Config) AbsURL(path string) string {
	if c.BaseURL == nil {
		return path
	}
	return strings.TrimSuffix(c.BaseURL.String(), "/") + path
}

// Rel prefixes a root-relative path with the base path: "/x/" -> "/blog/x/".
func (c *Config) Rel(path string) string {
	if c.Site.BasePath == "/" {
		return path
	}
	return strings.TrimSuffix(c.Site.BasePath, "/") + path
}

// Domain is the site's host name.
func (c *Config) Domain() string {
	if c.BaseURL == nil {
		return ""
	}
	return c.BaseURL.Hostname()
}
