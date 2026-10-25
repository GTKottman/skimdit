// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/GTKottman/skimdit/internal/store"
)

// statsCounter counts page views and feed fetches in memory and writes
// daily totals to the database once a minute. No cookie is set, no IP or
// user agent is stored, nothing identifies a visitor.
type statsCounter struct {
	s      *Server
	mu     sync.Mutex
	counts map[store.StatKey]int
}

func newStatsCounter(s *Server) *statsCounter {
	return &statsCounter{s: s, counts: map[store.StatKey]int{}}
}

var botWords = []string{"bot", "crawl", "spider", "slurp", "preview", "headless", "curl", "wget", "python", "go-http",
	"java/", "httpclient", "okhttp", "axios", "node-fetch", "scrapy", "lighthouse", "pingdom", "uptime", "monitor", "facebookexternalhit", "embedly"}

func isBot(ua string) bool {
	if ua == "" {
		return true
	}
	ua = strings.ToLower(ua)
	for _, w := range botWords {
		if strings.Contains(ua, w) {
			return true
		}
	}
	return false
}

func isFeed(rel string) bool {
	return rel == "feed.xml" || rel == "rss.xml" || rel == "feed.json" || strings.HasSuffix(rel, "/feed.xml")
}

// count records one request for a built file (rel is its output path).
func (c *statsCounter) count(r *http.Request, rel string, _ os.FileInfo) {
	if c == nil || !c.s.cfg.Stats.Enabled || r.Method != http.MethodGet {
		return
	}
	kind := ""
	switch {
	case isFeed(rel):
		kind = "feed" // feed readers are counted even though they are robots
	case strings.HasSuffix(rel, ".html") && rel != "404.html":
		if isBot(r.UserAgent()) || r.Header.Get("Sec-Purpose") != "" || r.Header.Get("Purpose") == "prefetch" {
			return
		}
		kind = "page"
	default:
		return
	}
	path := c.s.cfg.Rel("/" + strings.TrimSuffix(strings.TrimSuffix(rel, "index.html"), "/"))
	if strings.HasSuffix(rel, "index.html") && path != "/" && !strings.HasSuffix(path, "/") {
		path += "/"
	}
	ref := ""
	if kind == "page" {
		if u, err := url.Parse(r.Referer()); err == nil && u.Host != "" && !strings.EqualFold(u.Hostname(), c.s.cfg.Domain()) {
			ref = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		}
	}
	k := store.StatKey{Day: c.s.now().UTC().Format("2006-01-02"), Path: path, Referrer: ref, Kind: kind}
	c.mu.Lock()
	if len(c.counts) < 50_000 {
		c.counts[k]++
	}
	c.mu.Unlock()
}

func (c *statsCounter) flush() {
	c.mu.Lock()
	counts := c.counts
	c.counts = map[store.StatKey]int{}
	c.mu.Unlock()
	if err := c.s.store.AddStats(counts); err != nil {
		c.s.log.Printf("stats: %v", err)
	}
}

func (c *statsCounter) loop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.flush()
		}
	}
}

// ---- admin ----

type statRow struct {
	Label string
	N     int
	Pct   int
}

func bars(cs []store.Count) []statRow {
	maxN := 0
	for _, c := range cs {
		maxN = max(maxN, c.N)
	}
	out := make([]statRow, len(cs))
	for i, c := range cs {
		pct := 0
		if maxN > 0 {
			pct = c.N * 100 / maxN
		}
		out[i] = statRow{c.Label, c.N, pct}
	}
	return out
}

func (a *adminUI) statsPage(w http.ResponseWriter, r *http.Request) {
	s := a.s
	a.s.stats.flush()
	days := 30
	if r.URL.Query().Get("range") == "7" {
		days = 7
	}
	since := s.now().UTC().AddDate(0, 0, -days+1).Format("2006-01-02")
	// Fill in days with no views so the chart has no gaps.
	daily := map[string]int{}
	for _, c := range s.store.DailyViews(since, "page") {
		daily[c.Label] = c.N
	}
	var series []store.Count
	total := 0
	for i := days - 1; i >= 0; i-- {
		d := s.now().UTC().AddDate(0, 0, -i).Format("2006-01-02")
		series = append(series, store.Count{Label: d, N: daily[d]})
		total += daily[d]
	}
	feeds := 0
	for _, c := range s.store.DailyViews(since, "feed") {
		feeds += c.N
	}
	a.render(w, r, http.StatusOK, "stats", &adminPage{Title: "Stats", Nav: "stats", Data: map[string]any{
		"Enabled": s.cfg.Stats.Enabled, "Days": days, "Total": total, "Feeds": feeds,
		"Daily": bars(series), "Pages": bars(s.store.TopPaths(since, "page", 20)),
		"Referrers": bars(s.store.TopReferrers(since, 15)), "FeedPaths": bars(s.store.TopPaths(since, "feed", 10)),
	}})
}
