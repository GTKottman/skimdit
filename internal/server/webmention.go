// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/GTKottman/skimdit/internal/markdown"
	"github.com/GTKottman/skimdit/internal/site"
	"github.com/GTKottman/skimdit/internal/store"
	"golang.org/x/net/html"
	"willnorris.com/go/microformats"
)

// ---- receiving ----

// receiveWebmention accepts source/target, checks the target is one of our
// posts, and queues verification. Nothing shows until verified and approved.
func (s *Server) receiveWebmention(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Webmention.Receive {
		httpError(w, http.StatusNotFound, "webmentions are off")
		return
	}
	if !s.apiLimit.allow("wm:"+s.clientIP(r), s.now()) {
		httpError(w, http.StatusTooManyRequests, "too many webmentions; slow down")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	source, target := strings.TrimSpace(r.FormValue("source")), strings.TrimSpace(r.FormValue("target"))
	su, err1 := url.Parse(source)
	tu, err2 := url.Parse(target)
	switch {
	case err1 != nil || err2 != nil || len(source) > 2000 || len(target) > 2000:
		httpError(w, http.StatusBadRequest, "source and target must be URLs")
		return
	case su.Scheme != "https" && su.Scheme != "http":
		httpError(w, http.StatusBadRequest, "source must be an http(s) URL")
		return
	case !strings.EqualFold(tu.Hostname(), s.cfg.Domain()):
		httpError(w, http.StatusBadRequest, "target is not on this site")
		return
	case source == target:
		httpError(w, http.StatusBadRequest, "source and target are the same")
		return
	}
	e := s.entryByURLPath(tu.Path)
	if e == nil {
		httpError(w, http.StatusBadRequest, "target is not a post on this site")
		return
	}
	id, err := s.store.UpsertWebmention(source, target, e.Slug)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not save")
		return
	}
	payload, _ := json.Marshal(map[string]int64{"webmention": id})
	// Re-sends re-verify (an edit or deletion of the source), at most once a minute.
	_, _ = s.store.Enqueue("webmention_verify", fmt.Sprintf("webmention_verify:%d:%d", id, s.now().Unix()/60), string(payload), 0)
	httpError(w, http.StatusAccepted, "accepted; it will be checked and shown after approval")
}

// entryByURLPath finds a published post or page by its URL path.
func (s *Server) entryByURLPath(p string) *site.Entry {
	b := s.lastBuild()
	if b == nil {
		return nil
	}
	if !strings.HasSuffix(p, "/") && !strings.HasSuffix(p, ".html") {
		p += "/"
	}
	for _, e := range b.Entries {
		if e.URL == p && e.Status != "draft" {
			return e
		}
	}
	return nil
}

// verifyWebmention fetches the source and checks it really links to us,
// then reads its microformats for author, kind and text.
func (s *Server) jobWebmentionVerify(ctx context.Context, j *store.Job) (string, error) {
	var p struct {
		Webmention int64 `json:"webmention"`
	}
	if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
		return "", err
	}
	wm, err := s.store.Webmention(p.Webmention)
	if err != nil {
		return "gone", nil
	}
	body, hdr, final, err := s.fetch(ctx, wm.Source, "text/html", 1<<20)
	if err != nil {
		var se *statusError
		if errors.As(err, &se) && (se.code == http.StatusGone || se.code == http.StatusNotFound) {
			// The source was deleted: so is the mention.
			wm.Status = "deleted"
			return "source removed", s.store.SaveWebmention(wm)
		}
		return "", err
	}
	_ = hdr
	base, _ := url.Parse(final)
	if !linksTo(body, base, wm.Target) {
		if wm.Status == "approved" {
			wm.Status = "deleted"
		} else {
			wm.Status = "invalid"
		}
		return "no link to the target", s.store.SaveWebmention(wm)
	}
	readMicroformats(wm, body, base)
	switch wm.Status {
	case "approved", "spam":
		// Keep the owner's decision.
	default:
		wm.Status = "pending"
	}
	if err := s.store.SaveWebmention(wm); err != nil {
		return "", err
	}
	if wm.Status == "approved" {
		s.requestRebuild()
	}
	return "verified: " + wm.Type, nil
}

// linksTo reports whether HTML contains a link or embed of target.
func linksTo(body []byte, base *url.URL, target string) bool {
	want := normalizeURL(target)
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return false
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		for _, a := range tok.Attr {
			if a.Key != "href" && a.Key != "src" {
				continue
			}
			u, err := base.Parse(strings.TrimSpace(a.Val))
			if err == nil && normalizeURL(u.String()) == want {
				return true
			}
		}
	}
}

func normalizeURL(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	p.Fragment = ""
	p.Host = strings.ToLower(p.Host)
	p.Scheme = "https"
	p.Path = strings.TrimSuffix(p.Path, "/")
	return p.String()
}

func readMicroformats(wm *store.Webmention, body []byte, base *url.URL) {
	wm.Type = "mention"
	wm.Author, wm.AuthorURL, wm.HTML, wm.Published = "", "", "", 0
	data := microformats.Parse(bytes.NewReader(body), base)
	var entry *microformats.Microformat
	for _, it := range data.Items {
		if hasType(it, "h-entry") {
			entry = it
			break
		}
	}
	if entry == nil {
		return
	}
	target := normalizeURL(wm.Target)
	for prop, kind := range map[string]string{"in-reply-to": "reply", "like-of": "like", "repost-of": "repost", "bookmark-of": "mention"} {
		for _, v := range entry.Properties[prop] {
			if refersTo(v, target) {
				wm.Type = kind
			}
		}
	}
	if authors := entry.Properties["author"]; len(authors) > 0 {
		switch a := authors[0].(type) {
		case *microformats.Microformat:
			wm.Author = firstString(a.Properties["name"])
			wm.AuthorURL = firstString(a.Properties["url"])
		case string:
			wm.Author = a
		}
	}
	if wm.Author == "" {
		wm.Author = base.Host
	}
	if !strings.HasPrefix(wm.AuthorURL, "https://") && !strings.HasPrefix(wm.AuthorURL, "http://") {
		wm.AuthorURL = ""
	}
	wm.Author = truncate(wm.Author, 80)
	if c := entry.Properties["content"]; len(c) > 0 && (wm.Type == "reply" || wm.Type == "mention") {
		if m, ok := c[0].(map[string]string); ok {
			text := strings.TrimSpace(m["value"])
			if len([]rune(text)) > 600 {
				text = markdown.Truncate(text, 600)
				wm.HTML = "<p>" + html.EscapeString(text) + "</p>"
			} else if m["html"] != "" {
				wm.HTML = markdown.CommentPolicy().Sanitize(m["html"])
			} else {
				wm.HTML = "<p>" + html.EscapeString(text) + "</p>"
			}
		} else if str, ok := c[0].(string); ok {
			wm.HTML = "<p>" + html.EscapeString(markdown.Truncate(str, 600)) + "</p>"
		}
	}
	if pub := firstString(entry.Properties["published"]); pub != "" {
		if t, err := time.Parse(time.RFC3339, pub); err == nil {
			wm.Published = t.Unix()
		}
	}
}

func hasType(m *microformats.Microformat, t string) bool {
	for _, x := range m.Type {
		if x == t {
			return true
		}
	}
	return false
}

func refersTo(v any, target string) bool {
	switch x := v.(type) {
	case string:
		return normalizeURL(x) == target
	case *microformats.Microformat:
		for _, u := range x.Properties["url"] {
			if s, ok := u.(string); ok && normalizeURL(s) == target {
				return true
			}
		}
	}
	return false
}

func firstString(vs []any) string {
	for _, v := range vs {
		switch x := v.(type) {
		case string:
			return x
		case map[string]string:
			return x["value"]
		}
	}
	return ""
}

// ---- sending ----

var linkHeaderRe = regexp.MustCompile(`<([^>]*)>\s*;([^,]*)`)

// discoverEndpoint finds a page's webmention endpoint (Link header first,
// then <link> or <a> with rel=webmention).
func (s *Server) discoverEndpoint(ctx context.Context, target string) (string, error) {
	body, hdr, final, err := s.fetch(ctx, target, "text/html", 1<<20)
	if err != nil {
		return "", err
	}
	base, _ := url.Parse(final)
	for _, h := range hdr.Values("Link") {
		for _, m := range linkHeaderRe.FindAllStringSubmatch(h, -1) {
			if relHas(m[2], "webmention") {
				return resolveEndpoint(base, m[1])
			}
		}
	}
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return "", nil
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		tok := z.Token()
		if tok.Data != "link" && tok.Data != "a" {
			continue
		}
		var rel, href string
		hasHref := false
		for _, a := range tok.Attr {
			switch a.Key {
			case "rel":
				rel = a.Val
			case "href":
				href, hasHref = a.Val, true
			}
		}
		if hasHref && relHas(rel, "webmention") {
			return resolveEndpoint(base, href)
		}
	}
}

func relHas(rel, want string) bool {
	rel = strings.ReplaceAll(strings.ReplaceAll(rel, `"`, ""), "rel=", "")
	for _, r := range strings.Fields(rel) {
		if strings.EqualFold(r, want) {
			return true
		}
	}
	return false
}

func resolveEndpoint(base *url.URL, ref string) (string, error) {
	u, err := base.Parse(ref)
	if err != nil {
		return "", err
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", errors.New("endpoint is not http(s)")
	}
	return u.String(), nil
}

// jobWebmentionSend notifies every site a new post links to. It is best
// effort: sites without an endpoint are skipped.
func (s *Server) jobWebmentionSend(ctx context.Context, j *store.Job) (string, error) {
	var p struct {
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal([]byte(j.Payload), &p)
	e := s.entryBySlug(p.Slug)
	if e == nil {
		return "post gone", nil
	}
	b := s.lastBuild()
	targets := externalLinks(b.FeedHTML(e), s.cfg.Domain())
	sent, skipped, failed := 0, 0, 0
	for _, t := range targets {
		ep, err := s.discoverEndpoint(ctx, t)
		if err != nil {
			failed++
			continue
		}
		if ep == "" {
			skipped++
			continue
		}
		form := url.Values{"source": {e.Permalink}, "target": {t}}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ep, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", s.userAgent())
		resp, err := s.client.Do(req)
		if err != nil {
			failed++
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			sent++
		} else {
			failed++
		}
	}
	return fmt.Sprintf("%d sent, %d without an endpoint, %d failed", sent, skipped, failed), nil
}

// externalLinks lists the distinct http(s) links in HTML that leave the site.
func externalLinks(h, domain string) []string {
	seen := map[string]bool{}
	var out []string
	z := html.NewTokenizer(strings.NewReader(h))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return out
		}
		if tt != html.StartTagToken {
			continue
		}
		tok := z.Token()
		if tok.Data != "a" {
			continue
		}
		for _, a := range tok.Attr {
			if a.Key != "href" {
				continue
			}
			u, err := url.Parse(a.Val)
			if err != nil || (u.Scheme != "https" && u.Scheme != "http") || strings.EqualFold(u.Hostname(), domain) {
				continue
			}
			u.Fragment = ""
			if k := u.String(); !seen[k] && len(out) < 30 {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
}

// statusError is an HTTP error answer from a fetched URL.
type statusError struct {
	url  string
	code int
}

func (e *statusError) Error() string { return fmt.Sprintf("%s answered %d", e.url, e.code) }
