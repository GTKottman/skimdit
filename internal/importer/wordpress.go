// SPDX-License-Identifier: AGPL-3.0-or-later

package importer

import (
	"encoding/xml"
	"fmt"
	"html"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/GTKottman/skimdit/internal/content"
)

type wxr struct {
	Channel struct {
		Link  string    `xml:"link"`
		Items []wxrItem `xml:"item"`
	} `xml:"channel"`
}

type wxrItem struct {
	Title      string `xml:"title"`
	Link       string `xml:"link"`
	Content    string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Excerpt    string `xml:"http://wordpress.org/export/1.2/excerpt/ encoded"`
	PostName   string `xml:"http://wordpress.org/export/1.2/ post_name"`
	PostType   string `xml:"http://wordpress.org/export/1.2/ post_type"`
	Status     string `xml:"http://wordpress.org/export/1.2/ status"`
	DateGMT    string `xml:"http://wordpress.org/export/1.2/ post_date_gmt"`
	Date       string `xml:"http://wordpress.org/export/1.2/ post_date"`
	Categories []struct {
		Domain string `xml:"domain,attr"`
		Name   string `xml:",chardata"`
	} `xml:"category"`
	Comments []struct{} `xml:"http://wordpress.org/export/1.2/ comment"`
}

// WordPress imports a WXR export (Tools › Export in WordPress).
func WordPress(file string, opts Options) (*Report, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	d := xml.NewDecoder(f)
	d.Strict = false
	d.Entity = xml.HTMLEntity
	var doc wxr
	if err := d.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s is not a WordPress export: %w", file, err)
	}
	rep := &Report{}
	var posts []*Post
	comments := 0
	for _, it := range doc.Channel.Items {
		var kind content.Kind
		switch it.PostType {
		case "post":
			kind = content.KindPost
		case "page":
			kind = content.KindPage
		default:
			continue // attachments, menus, blocks...
		}
		if it.Status == "trash" || it.Status == "auto-draft" || it.Status == "inherit" {
			rep.Skipped++
			continue
		}
		comments += len(it.Comments)
		p := &Post{Kind: kind, From: firstNonEmpty(it.Title, it.PostName)}
		p.Fields.Title = firstNonEmpty(html.UnescapeString(it.Title), "Untitled")
		p.Fields.Slug = decodeSlug(it.PostName)
		if kind == content.KindPost {
			p.Fields.Date = dateString(wpDate(it.DateGMT, it.Date, opts.Location), opts.Location)
			if p.Fields.Date == "" {
				p.Fields.Date = dateString(time.Now(), opts.Location)
			}
		}
		switch it.Status {
		case "publish":
		case "future":
			p.Fields.Status = "scheduled"
		default:
			p.Fields.Status = "draft"
		}
		if ex := strings.TrimSpace(it.Excerpt); ex != "" {
			p.Fields.Excerpt = strings.TrimSpace(stripTags(html.UnescapeString(ex)))
		}
		var tags []string
		for _, c := range it.Categories {
			if (c.Domain == "post_tag" || c.Domain == "category") && c.Name != "Uncategorized" {
				tags = append(tags, html.UnescapeString(c.Name))
			}
		}
		p.Fields.Tags = uniqueSorted(tags)
		if a := aliasFrom(it.Link); a != "" {
			p.Fields.Aliases = []string{a}
		}
		p.HTML = wpContent(it.Content, rep, p.From)
		posts = append(posts, p)
	}
	if comments > 0 {
		rep.warn("the export holds %d comments; comments are not imported", comments)
	}
	return rep, write(posts, opts, rep)
}

func decodeSlug(s string) string {
	if dec, err := urlUnescape(s); err == nil {
		return content.Slugify(dec)
	}
	return content.Slugify(s)
}

func wpDate(gmt, local string, loc *time.Location) time.Time {
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", gmt, time.UTC); err == nil && !strings.HasPrefix(gmt, "0000") {
		return t
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04:05", local, loc); err == nil && !strings.HasPrefix(local, "0000") {
		return t
	}
	return time.Time{}
}

var (
	captionRe   = regexp.MustCompile(`(?s)\[caption[^\]]*\](.*?)\[/caption\]`)
	embedRe     = regexp.MustCompile(`(?s)\[embed[^\]]*\](.*?)\[/embed\]`)
	shortcodeRe = regexp.MustCompile(`\[/?[a-z_-]+(?:\s[^\]]*)?\]`)
	imgTagRe    = regexp.MustCompile(`(?s)^\s*(<a[^>]*>)?\s*(<img[^>]*>)\s*(</a>)?\s*`)
	tagRe       = regexp.MustCompile(`<[^>]+>`)
	youtubeRe   = regexp.MustCompile(`(?:youtube\.com/watch\?v=|youtu\.be/)([A-Za-z0-9_-]{6,20})`)
	blockTagRe  = regexp.MustCompile(`(?i)^<(p|div|h[1-6]|ul|ol|blockquote|pre|table|figure|img|hr)[\s>/]`)
	paraTagRe   = regexp.MustCompile(`(?i)<p[\s>]`)
	imgOnlyRe   = regexp.MustCompile(`(?s)<img[^>]*>`)
	titleAttrRe = regexp.MustCompile(`\s+title="[^"]*"`)
)

// wpContent turns WordPress post content into clean HTML: shortcodes
// resolved or removed, and paragraphs added the way WordPress shows them.
func wpContent(c string, rep *Report, from string) string {
	// A caption becomes the image's title, which Skimdit shows as its caption.
	c = captionRe.ReplaceAllStringFunc(c, func(m string) string {
		inner := captionRe.FindStringSubmatch(m)[1]
		lead := imgTagRe.FindString(inner)
		caption := strings.TrimSpace(stripTags(strings.TrimPrefix(inner, lead)))
		img := imgOnlyRe.FindString(lead)
		if img == "" {
			return inner
		}
		img = titleAttrRe.ReplaceAllString(img, "")
		img = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(img), ">"), "/") + ` title="` + html.EscapeString(caption) + `">`
		return "\n\n" + img + "\n\n"
	})
	c = embedRe.ReplaceAllStringFunc(c, func(m string) string {
		u := strings.TrimSpace(embedRe.FindStringSubmatch(m)[1])
		return `<p><a href="` + html.EscapeString(u) + `">` + html.EscapeString(u) + `</a></p>`
	})
	if codes := shortcodeRe.FindAllString(c, -1); len(codes) > 0 {
		rep.warn("%s: removed %d WordPress shortcode(s) such as %s", from, len(codes), codes[0])
		c = shortcodeRe.ReplaceAllString(c, "")
	}
	if !paraTagRe.MatchString(c) {
		c = autop(c)
	}
	return c
}

// autop wraps blank-line-separated text in paragraphs (WordPress's wpautop).
func autop(s string) string {
	var b strings.Builder
	for _, para := range regexp.MustCompile(`\n\s*\n`).Split(strings.ReplaceAll(s, "\r\n", "\n"), -1) {
		para = strings.TrimSpace(para)
		switch {
		case para == "":
		case blockTagRe.MatchString(para):
			b.WriteString(para + "\n")
		default:
			b.WriteString("<p>" + strings.ReplaceAll(para, "\n", "<br>\n") + "</p>\n")
		}
	}
	return b.String()
}

func stripTags(s string) string { return tagRe.ReplaceAllString(s, "") }
