// SPDX-License-Identifier: AGPL-3.0-or-later

package markdown

import (
	"regexp"
	"strings"
	"sync"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

var (
	contentOnce sync.Once
	contentPol  *bluemonday.Policy
	commentOnce sync.Once
	commentPol  *bluemonday.Policy
)

var (
	classRe = regexp.MustCompile(`^[a-zA-Z0-9 _-]{1,200}$`)
	idRe    = regexp.MustCompile(`^[\p{L}\p{N}:_.-]{1,200}$`)
)

// contentPolicy is the allow-list for the site author's own Markdown and
// raw HTML. It never allows scripts, event handlers, styles, iframes or
// forms; engine features that need richer markup are inserted after it runs.
func contentPolicy() *bluemonday.Policy {
	contentOnce.Do(func() {
		p := bluemonday.NewPolicy()
		p.AllowStandardURLs()
		p.AllowURLSchemes("http", "https", "mailto", "tel")
		p.AllowRelativeURLs(true)
		p.RequireParseableURLs(true)
		p.AllowElements("p", "br", "hr", "h1", "h2", "h3", "h4", "h5", "h6",
			"blockquote", "pre", "code", "kbd", "samp", "var", "em", "strong", "b", "i", "u",
			"s", "del", "ins", "mark", "small", "sub", "sup", "abbr", "cite", "q", "dfn", "time",
			"ul", "ol", "li", "dl", "dt", "dd", "table", "thead", "tbody", "tfoot", "tr", "th", "td",
			"caption", "colgroup", "col", "figure", "figcaption", "details", "summary", "aside",
			"section", "div", "span", "wbr", "ruby", "rt", "rp", "bdi", "bdo")
		p.AllowAttrs("href").OnElements("a")
		p.AllowAttrs("title").Globally()
		p.AllowAttrs("lang", "dir").Globally()
		p.AllowAttrs("class").Matching(classRe).Globally()
		p.AllowAttrs("id").Matching(idRe).Globally()
		p.AllowAttrs("role").Matching(regexp.MustCompile(`^doc-[a-z]+$|^note$`)).Globally()
		p.AllowAttrs("aria-hidden").Matching(regexp.MustCompile(`^true$`)).OnElements("a", "span")
		p.AllowAttrs("aria-label").Matching(regexp.MustCompile(`^[\p{L}\p{N} .,:;'()-]{1,100}$`)).OnElements("section", "nav", "aside", "figure")
		p.AllowAttrs("tabindex").Matching(regexp.MustCompile(`^-1$`)).OnElements("a")
		p.AllowAttrs("align").Matching(regexp.MustCompile(`^(left|right|center)$`)).OnElements("th", "td")
		p.AllowAttrs("colspan", "rowspan").Matching(bluemonday.Integer).OnElements("th", "td")
		p.AllowAttrs("start").Matching(bluemonday.Integer).OnElements("ol")
		p.AllowAttrs("reversed").OnElements("ol")
		p.AllowAttrs("open").OnElements("details")
		p.AllowAttrs("cite").OnElements("blockquote", "q", "del", "ins")
		p.AllowAttrs("datetime").OnElements("time", "del", "ins")
		p.AllowAttrs("type").Matching(regexp.MustCompile(`^checkbox$`)).OnElements("input")
		p.AllowAttrs("checked", "disabled").OnElements("input")
		p.AllowAttrs("src", "alt", "width", "height", "loading", "decoding").OnElements("img")
		p.AllowAttrs("loading").Matching(regexp.MustCompile(`^(lazy|eager)$`)).OnElements("img")
		p.AllowNoAttrs().OnElements("input")
		// Authors keep their own links as written: no forced nofollow.
		p.RequireNoFollowOnLinks(false)
		contentPol = p
	})
	return contentPol
}

// CommentPolicy is the strict allow-list for text written by readers:
// comments, webmentions and fediverse replies. Links get nofollow ugc.
func CommentPolicy() *bluemonday.Policy {
	commentOnce.Do(func() {
		p := bluemonday.NewPolicy()
		p.AllowStandardURLs()
		p.AllowURLSchemes("http", "https")
		p.RequireParseableURLs(true)
		p.AllowElements("p", "br", "em", "strong", "code", "pre", "blockquote", "ul", "ol", "li", "del")
		p.AllowAttrs("href").OnElements("a")
		p.RequireNoFollowOnLinks(true)
		p.AddTargetBlankToFullyQualifiedLinks(false)
		p.AllowRelativeURLs(false)
		commentPol = p
	})
	return commentPol
}

// htmlFacts extracts the word count, a plain-text excerpt and headings from HTML.
func htmlFacts(s string) (words int, excerpt string, headings []Heading) {
	doc, err := html.Parse(strings.NewReader("<body>" + s + "</body>"))
	if err != nil {
		return 0, "", nil
	}
	var firstPara string
	var walk func(n *html.Node, inPara bool)
	var para strings.Builder
	walk = func(n *html.Node, inPara bool) {
		if n.Type == html.TextNode {
			words += countWords([]byte(n.Data))
			if inPara {
				para.WriteString(n.Data)
			}
		}
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p":
				if firstPara == "" {
					para.Reset()
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c, true)
					}
					firstPara = strings.TrimSpace(para.String())
					return
				}
			case "h2", "h3", "h4":
				id := ""
				for _, a := range n.Attr {
					if a.Key == "id" {
						id = a.Val
					}
				}
				if id != "" {
					headings = append(headings, Heading{Level: int(n.Data[1] - '0'), ID: id, Text: strings.TrimSpace(textContent(n))})
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inPara)
		}
	}
	walk(doc, false)
	return words, Truncate(strings.Join(strings.Fields(firstPara), " "), 300), headings
}

func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textContent(c))
	}
	return b.String()
}
