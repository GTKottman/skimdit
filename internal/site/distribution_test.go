// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeedsAreValidAndAbsolute(t *testing.T) {
	res := buildFixture(t, filepath.Join(t.TempDir(), "dist"))
	files := readTree(t, res.Out)
	var atom struct {
		ID      string `xml:"id"`
		Entries []struct {
			ID      string `xml:"id"`
			Title   string `xml:"title"`
			Content string `xml:"content"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(files["feed.xml"], &atom); err != nil {
		t.Fatal(err)
	}
	if len(atom.Entries) != 3 {
		t.Fatalf("atom entries = %d, want 3 (alpha, beta, zeta)", len(atom.Entries))
	}
	for _, e := range atom.Entries {
		if !strings.HasPrefix(e.ID, "tag:fixture.example,2026-") {
			t.Errorf("entry id %q is not a stable tag URI", e.ID)
		}
		if strings.Contains(e.Content, `src="/`) || strings.Contains(e.Content, `href="/`) || strings.Contains(e.Content, "heading-anchor") {
			t.Errorf("feed content for %q has relative URLs or anchors", e.Title)
		}
	}
	var rss struct {
		Items []struct{ GUID string `xml:"guid"` } `xml:"channel>item"`
	}
	if err := xml.Unmarshal(files["rss.xml"], &rss); err != nil || len(rss.Items) != 3 {
		t.Fatalf("rss: %v %d", err, len(rss.Items))
	}
	var jf struct {
		Version string `json:"version"`
		Items   []struct {
			ID    string `json:"id"`
			Image string `json:"image"`
		} `json:"items"`
	}
	if err := json.Unmarshal(files["feed.json"], &jf); err != nil || jf.Version != "https://jsonfeed.org/version/1.1" || len(jf.Items) != 3 {
		t.Fatalf("json feed: %v %+v", err, jf)
	}
	if _, ok := files["tags/go/feed.xml"]; !ok {
		t.Error("per-tag feed missing")
	}
	if _, ok := files["series/intro/feed.xml"]; !ok {
		t.Error("per-series feed missing")
	}
	if !strings.Contains(string(files["feed.xml"]), `<?xml-stylesheet href="/feed.xsl"`) {
		t.Error("feed stylesheet missing")
	}
}

func TestSitemapRobotsSearch(t *testing.T) {
	res := buildFixture(t, filepath.Join(t.TempDir(), "dist"))
	files := readTree(t, res.Out)
	sm := string(files["sitemap.xml"])
	for _, want := range []string{"https://fixture.example/posts/alpha/", "https://fixture.example/about/", "https://fixture.example/tags/go/"} {
		if !strings.Contains(sm, want) {
			t.Errorf("sitemap missing %s", want)
		}
	}
	if strings.Contains(sm, "epsilon") {
		t.Error("sitemap lists the unlisted post")
	}
	robots := string(files["robots.txt"])
	if !strings.Contains(robots, "Sitemap: https://fixture.example/sitemap.xml") || strings.Contains(robots, "GPTBot") || strings.Contains(robots, "Disallow: /\n") {
		t.Errorf("robots.txt with ai=allow:\n%s", robots)
	}
	var manifest struct {
		Chunks []string `json:"chunks"`
		Count  int      `json:"count"`
	}
	if err := json.Unmarshal(files["search-index.json"], &manifest); err != nil || manifest.Count != 4 || len(manifest.Chunks) != 1 {
		t.Fatalf("search manifest: %v %+v", err, manifest)
	}
	if !strings.Contains(string(files["search/index-0.json"]), `"t":"alpha the first post"`) {
		t.Error("search index missing normalized title")
	}
}

func TestRobotsPolicies(t *testing.T) {
	for policy, want := range map[string][]string{
		"block-training": {"User-agent: GPTBot", "User-agent: ClaudeBot"},
		"block-all":      {"User-agent: GPTBot", "User-agent: PerplexityBot"},
	} {
		root := copyFixture(t, t.TempDir())
		cfg := filepath.Join(root, "config.toml")
		b, _ := os.ReadFile(cfg)
		os.WriteFile(cfg, append(b, []byte("\n[robots]\nai = \""+policy+"\"\n")...), 0o644)
		out := filepath.Join(t.TempDir(), "dist")
		if _, err := Run(Options{Root: root, Out: out, Now: fixtureNow}); err != nil {
			t.Fatal(err)
		}
		r, _ := os.ReadFile(filepath.Join(out, "robots.txt"))
		for _, w := range want {
			if !strings.Contains(string(r), w) {
				t.Errorf("%s: robots.txt missing %q", policy, w)
			}
		}
		// Ordinary search engines are never blocked by accident.
		if !strings.Contains(string(r), "User-agent: *\nDisallow: /admin/") {
			t.Errorf("%s: default group changed:\n%s", policy, r)
		}
	}
}

func TestStagingBlocksEverything(t *testing.T) {
	t.Setenv("SKIMDIT_STAGING", "1")
	res := buildFixture(t, filepath.Join(t.TempDir(), "dist"))
	files := readTree(t, res.Out)
	if !strings.Contains(string(files["robots.txt"]), "Disallow: /\n") {
		t.Error("staging robots.txt must disallow everything")
	}
	if !strings.Contains(string(files["index.html"]), `content="noindex"`) {
		t.Error("staging pages must be noindex")
	}
}

func TestSocialCardsAndJSONLD(t *testing.T) {
	res := buildFixture(t, filepath.Join(t.TempDir(), "dist"))
	files := readTree(t, res.Out)
	alpha := string(files["posts/alpha/index.html"])
	i := strings.Index(alpha, `<meta property="og:image" content="https://fixture.example/social/alpha-`)
	if i < 0 {
		t.Fatal("alpha has no generated social card")
	}
	found := false
	for name, data := range files {
		if strings.HasPrefix(name, "social/alpha-") && strings.HasSuffix(name, ".png") {
			found = len(data) > 1000 && string(data[1:4]) == "PNG"
		}
	}
	if !found {
		t.Error("social card PNG missing or not a PNG")
	}
	// Every post gets a card; only social_image replaces it.
	if !strings.Contains(string(files["posts/beta/index.html"]), `og:image" content="https://fixture.example/social/beta-`) {
		t.Error("beta should share its generated card")
	}
	start := strings.Index(alpha, `<script type="application/ld+json">`)
	end := strings.Index(alpha[start:], "</script>")
	var ld map[string]any
	if err := json.Unmarshal([]byte(alpha[start+len(`<script type="application/ld+json">`):start+end]), &ld); err != nil {
		t.Fatalf("JSON-LD: %v", err)
	}
	if ld["@context"] != "https://schema.org" || !strings.Contains(alpha, `"@type":"BlogPosting"`) {
		t.Errorf("JSON-LD = %v", ld)
	}
}

func TestLedgerProtectsPublishedURLs(t *testing.T) {
	root := copyFixture(t, t.TempDir())
	out := filepath.Join(t.TempDir(), "dist")
	// First production build records every URL.
	if _, err := Run(Options{Root: root, Out: out, Now: fixtureNow, RecordURLs: true}); err != nil {
		t.Fatal(err)
	}
	ledger, _ := os.ReadFile(filepath.Join(root, LedgerFile))
	if !strings.Contains(string(ledger), "/posts/alpha/\tcontent/posts/alpha/index.md") {
		t.Fatalf("ledger:\n%s", ledger)
	}
	// Rename alpha's slug: check (no recording) fails, build records a redirect.
	p := filepath.Join(root, "content/posts/alpha/index.md")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), "date: 2026-10-01", "date: 2026-10-01\nslug: alpha-renamed", 1)), 0o644)
	bp := filepath.Join(root, "content/posts/beta/index.md")
	bb, _ := os.ReadFile(bp)
	os.WriteFile(bp, []byte(strings.ReplaceAll(string(bb), "post:alpha", "post:alpha-renamed")), 0o644)
	if res, err := Run(Options{Root: root, Out: out, Now: fixtureNow}); err == nil || !strings.Contains(res.Diags.Error(), "run `skimdit build` to record the redirect") {
		t.Fatalf("want ledger error, got %v", err)
	}
	if _, err := Run(Options{Root: root, Out: out, Now: fixtureNow, RecordURLs: true}); err != nil {
		t.Fatal(err)
	}
	red, _ := os.ReadFile(filepath.Join(root, RedirectsFile))
	if !strings.Contains(string(red), `"/posts/alpha/" = "/posts/alpha-renamed/"`) {
		t.Fatalf("redirects.toml:\n%s", red)
	}
	page, _ := os.ReadFile(filepath.Join(out, "posts/alpha/index.html"))
	if !strings.Contains(string(page), "url=/posts/alpha-renamed/") {
		t.Error("old URL does not redirect")
	}
	// Deleting a published post fails until a redirect or "gone" is added.
	os.RemoveAll(filepath.Join(root, "content/posts/zeta"))
	if res, err := Run(Options{Root: root, Out: out, Now: fixtureNow}); err == nil || !strings.Contains(res.Diags.Error(), "would now be a dead link") {
		t.Fatalf("want dead-link error, got %v", err)
	}
	f, _ := os.OpenFile(filepath.Join(root, RedirectsFile), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("\"/posts/zeta/\" = \"gone\"\n")
	f.Close()
	if res, err := Run(Options{Root: root, Out: out, Now: fixtureNow}); err != nil {
		t.Fatalf("%v %v", err, res.Diags)
	}
	r, _ := os.ReadFile(filepath.Join(out, "_redirects"))
	if !strings.Contains(string(r), "/posts/zeta/ /posts/zeta/ 410") || !strings.Contains(string(r), "/old-beta/ /posts/beta/ 301") {
		t.Errorf("_redirects:\n%s", r)
	}
}
