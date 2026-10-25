// SPDX-License-Identifier: AGPL-3.0-or-later

package importer

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GTKottman/skimdit/internal/content"
	"github.com/GTKottman/skimdit/internal/site"
)

func pngBytes() []byte {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	return b.Bytes()
}

func imageServer(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".png") {
			w.Write(pngBytes())
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newSite(t *testing.T) (string, Options) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "config.toml"), []byte("[site]\ntitle = \"T\"\nurl = \"https://new.example\"\n"), 0o644)
	return root, Options{ContentDir: filepath.Join(root, "content"), Location: time.UTC, Download: true}
}

func read(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// builds checks the imported site really builds.
func builds(t *testing.T, root string) {
	t.Helper()
	res, err := site.Run(site.Options{Root: root, Out: filepath.Join(t.TempDir(), "dist"), Now: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		msg := ""
		if res != nil {
			msg = res.Diags.Error()
		}
		t.Fatalf("imported site does not build: %v\n%s", err, msg)
	}
}

func TestWordPress(t *testing.T) {
	img := imageServer(t)
	root, opts := newSite(t)
	wxrFile := filepath.Join(t.TempDir(), "wp.xml")
	os.WriteFile(wxrFile, []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:excerpt="http://wordpress.org/export/1.2/excerpt/" xmlns:wp="http://wordpress.org/export/1.2/">
<channel><link>https://old.example</link>
<item><title>Hello &amp; welcome</title><link>https://old.example/2019/05/hello-welcome/</link>
<content:encoded><![CDATA[First paragraph with <strong>bold</strong>.

Second line one
second line two

[caption id="x" align="aligncenter" width="4"]<img src="`+img.URL+`/wp-content/uploads/pic.png" alt="A pixel" /> A tiny picture[/caption]

[gallery ids="1,2"]]]></content:encoded>
<excerpt:encoded><![CDATA[Short <em>summary</em>]]></excerpt:encoded>
<wp:post_name>hello-welcome</wp:post_name><wp:post_type>post</wp:post_type><wp:status>publish</wp:status>
<wp:post_date_gmt>2019-05-03 12:30:00</wp:post_date_gmt>
<category domain="category" nicename="news"><![CDATA[News]]></category><category domain="post_tag" nicename="go"><![CDATA[Go]]></category>
<wp:comment><wp:comment_content>hi</wp:comment_content></wp:comment>
</item>
<item><title>Unfinished</title><link>https://old.example/?p=9</link><content:encoded><![CDATA[<p>WIP</p>]]></content:encoded>
<wp:post_name></wp:post_name><wp:post_type>post</wp:post_type><wp:status>draft</wp:status><wp:post_date_gmt>0000-00-00 00:00:00</wp:post_date_gmt><wp:post_date>2020-01-01 10:00:00</wp:post_date></item>
<item><title>About</title><link>https://old.example/about/</link><content:encoded><![CDATA[<p>About me.</p>]]></content:encoded>
<wp:post_name>about</wp:post_name><wp:post_type>page</wp:post_type><wp:status>publish</wp:status></item>
<item><title>pic</title><wp:post_type>attachment</wp:post_type><wp:status>inherit</wp:status></item>
</channel></rss>`), 0o644)
	rep, err := WordPress(wxrFile, opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Posts != 2 || rep.Pages != 1 || rep.Drafts != 1 || rep.Images != 1 {
		t.Fatalf("report: %+v", rep)
	}
	post := read(t, filepath.Join(opts.ContentDir, "posts/hello-welcome/index.md"))
	for _, want := range []string{"title: Hello & welcome", "date: \"2019-05-03T12:30:00+00:00\"", "tags: [Go, News]", "aliases: [/2019/05/hello-welcome/]",
		"excerpt: Short summary", "First paragraph with **bold**.\n\nSecond line one", "![A pixel](pic.png \"A tiny picture\")"} {
		if !strings.Contains(post, want) {
			t.Errorf("post missing %q:\n%s", want, post)
		}
	}
	if strings.Contains(post, "[gallery") || strings.Contains(post, "[caption") {
		t.Error("shortcodes left in the post")
	}
	if _, err := os.Stat(filepath.Join(opts.ContentDir, "posts/hello-welcome/pic.png")); err != nil {
		t.Error("image not downloaded")
	}
	if !strings.Contains(read(t, filepath.Join(opts.ContentDir, "posts/unfinished/index.md")), "status: draft") {
		t.Error("draft not marked")
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), "1 comments") {
		t.Errorf("comments not reported: %v", rep.Warnings)
	}
	builds(t, root)
}

func TestGhost(t *testing.T) {
	img := imageServer(t)
	root, opts := newSite(t)
	opts.BaseURL = img.URL
	f := filepath.Join(t.TempDir(), "ghost.json")
	os.WriteFile(f, []byte(`{"db":[{"data":{
		"posts":[{"id":"1","title":"Ghost post","slug":"ghost-post","html":"<p>Hi <a href=\"https://x.example\">there</a>.</p><figure><img src=\"__GHOST_URL__/content/images/a.png\" alt=\"Pic\"></figure>","type":"post","status":"published","published_at":"2021-02-03T04:05:06.000Z","custom_excerpt":"Ex","feature_image":"__GHOST_URL__/content/images/cover.png"},
		         {"id":"2","title":"Ghost page","slug":"info","html":"<p>Page</p>","type":"page","status":"published"}],
		"tags":[{"id":"t1","name":"Writing"},{"id":"t2","name":"#internal"}],
		"posts_tags":[{"post_id":"1","tag_id":"t1"},{"post_id":"1","tag_id":"t2"}],
		"posts_meta":[{"post_id":"1","feature_image_alt":"A cover"}]}}]}`), 0o644)
	rep, err := Ghost(f, opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Posts != 1 || rep.Pages != 1 || rep.Images != 2 {
		t.Fatalf("report: %+v %v", rep, rep.Warnings)
	}
	post := read(t, filepath.Join(opts.ContentDir, "posts/ghost-post/index.md"))
	for _, want := range []string{"tags: [Writing]", "aliases: [/ghost-post/]", "cover: cover.png", "cover_alt: A cover", "[there](https://x.example)", "![Pic](a.png)"} {
		if !strings.Contains(post, want) {
			t.Errorf("post missing %q:\n%s", want, post)
		}
	}
	builds(t, root)
}

func TestMarkdownFolder(t *testing.T) {
	root, opts := newSite(t)
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "posts/bundle"), 0o755)
	os.WriteFile(filepath.Join(src, "posts/bundle/index.md"), []byte("+++\ntitle = \"Hugo bundle\"\ndate = 2022-03-04T05:06:07Z\ntags = [\"go\"]\ncategories = [\"dev\"]\ndraft = false\naliases = [\"/old/bundle/\"]\nweight = 3\n+++\n\n![Logo](logo.png)\n\n{{< youtube abc >}}\n"), 0o644)
	os.WriteFile(filepath.Join(src, "posts/bundle/logo.png"), pngBytes(), 0o644)
	os.MkdirAll(filepath.Join(src, "_posts"), 0o755)
	os.WriteFile(filepath.Join(src, "_posts/2020-01-02-jekyll-thing.md"), []byte("---\ntitle: Jekyll thing\nlayout: post\npermalink: /blog/jekyll-thing.html\n---\nBody.\n"), 0o644)
	os.WriteFile(filepath.Join(src, "_index.md"), []byte("---\ntitle: Section\n---\n"), 0o644)
	rep, err := Markdown(src, opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Posts != 2 {
		t.Fatalf("report: %+v %v", rep, rep.Warnings)
	}
	b := read(t, filepath.Join(opts.ContentDir, "posts/bundle/index.md"))
	for _, want := range []string{"tags: [dev, go]", "aliases: [/old/bundle/]", "weight: 3"} {
		if !strings.Contains(b, want) {
			t.Errorf("bundle missing %q:\n%s", want, b)
		}
	}
	if _, err := os.Stat(filepath.Join(opts.ContentDir, "posts/bundle/logo.png")); err != nil {
		t.Error("bundle file not copied")
	}
	j := read(t, filepath.Join(opts.ContentDir, "posts/jekyll-thing/index.md"))
	if !strings.Contains(j, "2020-01-02") || !strings.Contains(j, "/blog/jekyll-thing.html") {
		t.Errorf("jekyll post:\n%s", j)
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), "shortcodes") {
		t.Error("Hugo shortcode not reported")
	}
	// The shortcode text stays as text, so it still builds.
	builds(t, root)
}

func TestSkimditRoundTrip(t *testing.T) {
	// Importing a Skimdit content folder yields the same frontmatter.
	_, opts := newSite(t)
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "posts/a"), 0o755)
	in := "---\ntitle: A\ndate: \"2026-01-02T03:04:05Z\"\ntags: [x]\n---\n\nBody.\n"
	os.WriteFile(filepath.Join(src, "posts/a/index.md"), []byte(in), 0o644)
	if _, err := Markdown(src, opts); err != nil {
		t.Fatal(err)
	}
	it, diags := content.Parse([]byte(read(t, filepath.Join(opts.ContentDir, "posts/a/index.md"))), "a", content.KindPost, "a", time.UTC)
	if diags.HasErrors() || it.Title != "A" || it.Date.Unix() != time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Unix() || len(it.Tags) != 1 {
		t.Fatalf("round trip: %+v %v", it, diags)
	}
}
