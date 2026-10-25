// SPDX-License-Identifier: AGPL-3.0-or-later

package markdown

import (
	"strings"
	"testing"
)

func render(t *testing.T, src string, hooks Hooks) *Doc {
	t.Helper()
	d, err := New(Options{RawHTML: true, Typographer: true}).Render([]byte(src), hooks)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func has(t *testing.T, html string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(html, w) {
			t.Errorf("output missing %q\n---\n%s", w, html)
		}
	}
}

func hasNot(t *testing.T, html string, bad ...string) {
	t.Helper()
	for _, b := range bad {
		if strings.Contains(html, b) {
			t.Errorf("output must not contain %q\n---\n%s", b, html)
		}
	}
}

func TestBasics(t *testing.T) {
	d := render(t, "# Hello World\n\nSome *text* with a [link](https://example.com).\n\n## Why Go?\n\n## Why Go?\n\n| a | b |\n|:--|--:|\n| 1 | 2 |\n\n- [x] done\n- [ ] todo\n\n~~old~~\n", Hooks{})
	has(t, d.HTML, `<h1 id="hello-world">`, `<h2 id="why-go">`, `<h2 id="why-go-2">`, `<a href="https://example.com">link</a>`,
		`align="left"`, `type="checkbox"`, `<del>old</del>`, `class="heading-anchor"`)
	hasNot(t, d.HTML, "nofollow", "skmd")
	if len(d.Headings) != 3 || d.Headings[1].ID != "why-go" {
		t.Errorf("headings = %+v", d.Headings)
	}
	if d.Excerpt != "Some text with a link." {
		t.Errorf("excerpt = %q", d.Excerpt)
	}
}

func TestMoreMarker(t *testing.T) {
	d := render(t, "First para.\n\nSecond para.\n\n<!--more-->\n\nAfter.\n", Hooks{})
	if d.Excerpt != "First para. Second para." {
		t.Errorf("excerpt = %q", d.Excerpt)
	}
}

func TestSanitizer(t *testing.T) {
	d := render(t, "<script>alert(1)</script>\n\n<img src=x onerror=alert(1) alt=a>\n\n[x](javascript:alert(1))\n\n<a href=\"#\" onclick=\"x()\">c</a>\n\n<iframe src=\"https://evil\"></iframe>\n\n<div style=\"color:red\">s</div>\n", Hooks{})
	hasNot(t, d.HTML, "<script", "onerror", "javascript:", "onclick", "<iframe", "style=")
}

func TestMath(t *testing.T) {
	d := render(t, "Inline $a_b * c_d$ and money $5 and $10.\n\n$$\n\\frac{1}{2}\n$$\n\n```math\nx^2\n```\n", Hooks{})
	if !d.HasMath {
		t.Fatal("HasMath false")
	}
	has(t, d.HTML, `class="math math-inline"`, `class="math math-display"`, "<math", "$5 and $10")
	// Underscores inside math must not become emphasis.
	hasNot(t, d.HTML, "<em>b", "skmd")
}

func TestMathError(t *testing.T) {
	d := render(t, "line one\n\n$\\frac{1}$\n", Hooks{})
	if len(d.Diags) != 1 || d.Diags[0].Warning || d.Diags[0].Line != 3 {
		t.Fatalf("diags = %+v", d.Diags)
	}
}

func TestCode(t *testing.T) {
	d := render(t, "```go {title=\"main.go\" hl=\"2\"}\npackage main\nfunc main() {}\n```\n\n```\nplain <b>\n```\n", Hooks{})
	has(t, d.HTML, `<figure class="code" data-lang="go">`, `<span class="code-title">main.go</span>`, `class="chroma"`, "&lt;b&gt;", `class="line hl"`)
	if !d.HasCode {
		t.Error("HasCode false")
	}
}

func TestDirectives(t *testing.T) {
	src := ":::callout{type=warning title=\"Careful\"}\nThis **matters**.\n\n<script>x</script>\n:::\n\n::youtube{id=dQw4w9WgXcQ title=\"A song\"}\n\n:::quote{url=\"https://example.com/p/1\" author=\"Ada\"}\nShort quote.\n:::\n"
	d := render(t, src, Hooks{})
	has(t, d.HTML, `<aside class="callout callout-warning" role="note">`, `<p class="callout-title">Careful</p>`, "<strong>matters</strong>",
		`data-embed="youtube"`, `https://www.youtube.com/watch?v=dQw4w9WgXcQ`, `<blockquote cite="https://example.com/p/1">`)
	hasNot(t, d.HTML, "<script", "skmd", "<p><aside", "</aside></p>")
	if !d.HasEmbed {
		t.Error("HasEmbed false")
	}
	bad := render(t, "::nope{}\n\n::youtube{id=x}\n", Hooks{})
	if len(bad.Diags) != 2 {
		t.Errorf("diags = %+v", bad.Diags)
	}
}

func TestNestedDirectives(t *testing.T) {
	d := render(t, "::::callout{type=note}\nOuter\n\n:::callout{type=tip}\nInner\n:::\n::::\n", Hooks{})
	has(t, d.HTML, `callout-note`, `callout-tip`)
	if strings.Index(d.HTML, "callout-note") > strings.Index(d.HTML, "callout-tip") {
		t.Error("nesting order wrong")
	}
	hasNot(t, d.HTML, "skmd")
}

func TestImages(t *testing.T) {
	hooks := Hooks{
		RequireAlt: true,
		Image: func(src string) (*Image, error) {
			return &Image{Src: "/media/" + src, Width: 800, Height: 600, Sources: []ImageSource{{Type: "image/avif", Srcset: "/media/a.avif 800w"}}}, nil
		},
	}
	d := render(t, "![A cat](cat.jpg \"My cat\")\n\nText ![inline](i.png) here.\n\n![](noalt.jpg)\n\n![x](https://remote/x.png)\n", hooks)
	has(t, d.HTML, "<figure><picture>", `type="image/avif"`, `width="800" height="600"`, "<figcaption>My cat</figcaption>", `alt="inline"`, `loading="lazy"`)
	hasNot(t, d.HTML, "<p><figure>")
	errs := 0
	for _, dg := range d.Diags {
		if !dg.Warning {
			errs++
		}
	}
	if errs != 2 {
		t.Errorf("want 2 errors (no alt, remote), got %+v", d.Diags)
	}
}

func TestLinkHook(t *testing.T) {
	d := render(t, "[a](post:hello) [b](file.pdf)\n", Hooks{Link: func(dest string) (string, error) {
		if dest == "post:hello" {
			return "/posts/hello/", nil
		}
		return "/files/" + dest, nil
	}})
	has(t, d.HTML, `href="/posts/hello/"`, `href="/files/file.pdf"`)
	if len(d.Links) != 2 {
		t.Errorf("links = %v", d.Links)
	}
}

func TestFootnotes(t *testing.T) {
	d := render(t, "Claim.[^1]\n\n[^1]: Source.\n", Hooks{})
	has(t, d.HTML, `role="doc-noteref"`, `<section class="footnotes" role="doc-endnotes">`, `id="fn:1"`)
}

func TestTypographerExcerpt(t *testing.T) {
	d := render(t, "It's \"quoted\" -- yes...\n", Hooks{})
	if strings.Contains(d.Excerpt, "&") {
		t.Errorf("excerpt has entities: %q", d.Excerpt)
	}
}

func TestHTMLPost(t *testing.T) {
	d := New(Options{}).RenderHTML([]byte(`<h2 id="a">A</h2><p>Hello there friend.</p><script>x</script>`))
	hasNot(t, d.HTML, "<script")
	if d.Excerpt != "Hello there friend." || len(d.Headings) != 1 || d.Words != 4 {
		t.Errorf("doc = %+v", d)
	}
}

func FuzzRender(f *testing.F) {
	for _, s := range []string{"# a", "$x$", ":::callout\nx\n:::", "<script>", "![a](b)", "```go\nx\n```", "[^1]\n\n[^1]: x", "::youtube{id=\"abcdefg\" title=x}"} {
		f.Add(s)
	}
	r := New(Options{RawHTML: true, Typographer: true})
	f.Fuzz(func(t *testing.T, src string) {
		d, err := r.Render([]byte(src), Hooks{})
		if err != nil {
			return
		}
		low := strings.ToLower(d.HTML)
		for _, bad := range []string{"<script", "javascript:", "onerror=", "onload=", "<iframe"} {
			if strings.Contains(low, bad) && !strings.Contains(strings.ToLower(src), "skmd") {
				t.Fatalf("unsafe output %q for input %q", d.HTML, src)
			}
		}
	})
}
