// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/GTKottman/skimdit/internal/markdown"
	"github.com/GTKottman/skimdit/internal/site"
	"github.com/GTKottman/skimdit/internal/store"
)

// Cross-posting announces a new post on Mastodon and Bluesky once, and
// keeps the resulting links (shown on the post as "also on").

func (s *Server) saveCrosspost(slug, network, link string) error {
	_, err := s.store.DB.Exec(`INSERT INTO crossposts (post, network, url, created_at) VALUES (?, ?, ?, ?) ON CONFLICT(post, network) DO UPDATE SET url = excluded.url`, slug, network, link, s.store.Unix())
	return err
}

func (s *Server) crosspostURL(slug, network string) string {
	var u string
	_ = s.store.DB.QueryRow(`SELECT url FROM crossposts WHERE post = ? AND network = ?`, slug, network).Scan(&u)
	return u
}

// syndication lists where a post was cross-posted.
func (s *Server) syndication(e *site.Entry) []site.Link {
	rows, err := s.store.DB.Query(`SELECT network, url FROM crossposts WHERE post = ? ORDER BY network`, e.Slug)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []site.Link
	for rows.Next() {
		var n, u string
		if rows.Scan(&n, &u) == nil {
			out = append(out, site.Link{Name: map[string]string{"mastodon": "Mastodon", "bluesky": "Bluesky"}[n], URL: u})
		}
	}
	return out
}

// announcement is the short text: title, excerpt, link, trimmed to fit.
func announcement(e *site.Entry, limit int) string {
	link := e.Permalink
	head := e.Title
	room := limit - utf8.RuneCountInString(head) - utf8.RuneCountInString(link) - 4
	text := head
	if room > 20 && e.Excerpt != "" {
		text += "\n\n" + markdown.Truncate(e.Excerpt, room)
	}
	return text + "\n\n" + link
}

func (s *Server) slugPost(j *store.Job) *site.Entry {
	var p struct {
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal([]byte(j.Payload), &p)
	e := s.entryBySlug(p.Slug)
	if e == nil || e.Unlisted {
		return nil
	}
	return e
}

func (s *Server) jobMastodon(ctx context.Context, j *store.Job) (string, error) {
	e := s.slugPost(j)
	if e == nil {
		return "post gone or unlisted", nil
	}
	if u := s.crosspostURL(e.Slug, "mastodon"); u != "" {
		return u, nil
	}
	token := getenv("SKIMDIT_MASTODON_TOKEN")
	if token == "" {
		return "", errors.New("SKIMDIT_MASTODON_TOKEN is not set")
	}
	body, _ := json.Marshal(map[string]string{"status": announcement(e, 500), "visibility": "public", "language": e.Lang})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(s.cfg.Crosspost.Mastodon.Instance, "/")+"/api/v1/statuses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	// Mastodon refuses a second post with the same key for an hour.
	req.Header.Set("Idempotency-Key", "skimdit-"+e.Slug)
	req.Header.Set("User-Agent", s.userAgent())
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := readLimited(resp.Body, 1<<20)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("mastodon answered %d: %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var out struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.URL != "" {
		_ = s.saveCrosspost(e.Slug, "mastodon", out.URL)
		s.requestRebuild()
	}
	return out.URL, nil
}

func (s *Server) jobBluesky(ctx context.Context, j *store.Job) (string, error) {
	e := s.slugPost(j)
	if e == nil {
		return "post gone or unlisted", nil
	}
	if u := s.crosspostURL(e.Slug, "bluesky"); u != "" {
		return u, nil
	}
	cfg := s.cfg.Crosspost.Bluesky
	pass := getenv("SKIMDIT_BLUESKY_PASSWORD")
	if pass == "" {
		return "", errors.New("SKIMDIT_BLUESKY_PASSWORD is not set")
	}
	pds := strings.TrimSuffix(cfg.PDS, "/")
	call := func(method, token string, in, out any) error {
		b, _ := json.Marshal(in)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, pds+"/xrpc/"+method, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", s.userAgent())
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		raw, _ := readLimited(resp.Body, 1<<20)
		if resp.StatusCode >= 300 {
			return fmt.Errorf("bluesky %s answered %d: %s", method, resp.StatusCode, truncate(string(raw), 200))
		}
		return json.Unmarshal(raw, out)
	}
	var sess struct {
		AccessJwt string `json:"accessJwt"`
		DID       string `json:"did"`
		Handle    string `json:"handle"`
	}
	if err := call("com.atproto.server.createSession", "", map[string]string{"identifier": cfg.Handle, "password": pass}, &sess); err != nil {
		return "", err
	}
	// Bluesky posts are limited to 300 characters; the link becomes a
	// clickable facet (byte offsets) and a link card.
	text := announcement(e, 300)
	start := strings.LastIndex(text, e.Permalink)
	record := map[string]any{
		"$type":     "app.bsky.feed.post",
		"text":      text,
		"createdAt": s.now().UTC().Format(time.RFC3339),
		"langs":     []string{e.Lang},
		"facets": []any{map[string]any{
			"index":    map[string]int{"byteStart": start, "byteEnd": start + len(e.Permalink)},
			"features": []any{map[string]string{"$type": "app.bsky.richtext.facet#link", "uri": e.Permalink}},
		}},
		"embed": map[string]any{"$type": "app.bsky.embed.external", "external": map[string]string{"uri": e.Permalink, "title": e.Title, "description": markdown.Truncate(e.Excerpt, 280)}},
	}
	var created struct {
		URI string `json:"uri"`
	}
	if err := call("com.atproto.repo.createRecord", sess.AccessJwt, map[string]any{"repo": sess.DID, "collection": "app.bsky.feed.post", "record": record}, &created); err != nil {
		return "", err
	}
	// at://did/app.bsky.feed.post/rkey -> https://bsky.app/profile/handle/post/rkey
	rkey := created.URI[strings.LastIndexByte(created.URI, '/')+1:]
	link := "https://bsky.app/profile/" + firstNonEmpty(sess.Handle, cfg.Handle) + "/post/" + rkey
	_ = s.saveCrosspost(e.Slug, "bluesky", link)
	s.requestRebuild()
	return link, nil
}
