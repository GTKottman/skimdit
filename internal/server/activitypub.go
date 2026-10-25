// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/GTKottman/skimdit/internal/markdown"
	"github.com/GTKottman/skimdit/internal/site"
	"github.com/GTKottman/skimdit/internal/store"
)

// The site is one ActivityPub actor (@handle@domain). Fediverse users can
// follow it; new posts are delivered to followers as Articles; replies,
// likes and boosts come back into the moderation queue as mentions.

const (
	apContentType = "application/activity+json"
	asPublic      = "https://www.w3.org/ns/activitystreams#Public"
)

func (s *Server) apOn() bool { return s.cfg.Fediverse.Enabled }

func (s *Server) apURL(p string) string { return s.cfg.AbsURL(s.cfg.Rel("/ap" + p)) }
func (s *Server) actorID() string       { return s.apURL("/actor") }
func (s *Server) objectID(slug string) string {
	return s.apURL("/posts/" + slug)
}

func writeAP(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", apContentType+"; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(v)
}

// ---- discovery ----

func (s *Server) webfinger(w http.ResponseWriter, r *http.Request) {
	if !s.apOn() {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	want := "acct:" + s.cfg.Fediverse.Handle + "@" + s.cfg.Domain()
	res := r.URL.Query().Get("resource")
	if !strings.EqualFold(res, want) && res != s.actorID() {
		httpError(w, http.StatusNotFound, "no such account")
		return
	}
	w.Header().Set("Content-Type", "application/jrd+json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"subject": want,
		"aliases": []string{s.actorID(), s.cfg.AbsURL(s.cfg.Site.BasePath)},
		"links": []map[string]string{
			{"rel": "self", "type": apContentType, "href": s.actorID()},
			{"rel": "http://webfinger.net/rel/profile-page", "type": "text/html", "href": s.cfg.AbsURL(s.cfg.Site.BasePath)},
		},
	})
}

func (s *Server) nodeinfoIndex(w http.ResponseWriter, r *http.Request) {
	if !s.apOn() {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": []map[string]string{{"rel": "http://nodeinfo.diaspora.software/ns/schema/2.1", "href": s.apURL("/nodeinfo")}}})
}

// ---- actor, outbox, objects ----

func (s *Server) activityPubHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.apOn() {
			httpError(w, http.StatusNotFound, "not found")
			return
		}
		p := strings.TrimPrefix(r.URL.Path, s.cfg.Rel("/ap"))
		switch {
		case p == "/actor" && r.Method == http.MethodGet:
			s.apActor(w, r)
		case p == "/inbox" && r.Method == http.MethodPost:
			s.apInbox(w, r)
		case p == "/outbox" && r.Method == http.MethodGet:
			s.apOutbox(w, r)
		case p == "/followers" && r.Method == http.MethodGet:
			var n int
			_ = s.store.DB.QueryRow(`SELECT COUNT(*) FROM ap_followers`).Scan(&n)
			writeAP(w, map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "id": s.apURL("/followers"), "type": "OrderedCollection", "totalItems": n})
		case p == "/nodeinfo":
			b := s.lastBuild()
			posts := 0
			if b != nil {
				posts = len(b.Posts)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"version": "2.1", "software": map[string]string{"name": "skimdit", "version": versionString(), "repository": "https://github.com/GTKottman/skimdit"},
				"protocols": []string{"activitypub"}, "services": map[string]any{"inbound": []string{}, "outbound": []string{"atom1.0", "rss2.0"}},
				"openRegistrations": false, "usage": map[string]any{"users": map[string]int{"total": 1}, "localPosts": posts}, "metadata": map[string]string{},
			})
		case strings.HasPrefix(p, "/posts/") && r.Method == http.MethodGet:
			e := s.entryBySlug(strings.TrimPrefix(p, "/posts/"))
			if e == nil || e.Unlisted {
				httpError(w, http.StatusNotFound, "not found")
				return
			}
			art := s.article(e)
			art["@context"] = "https://www.w3.org/ns/activitystreams"
			writeAP(w, art)
		default:
			httpError(w, http.StatusNotFound, "not found")
		}
	})
}

func (s *Server) apActor(w http.ResponseWriter, r *http.Request) {
	_, pub, err := s.apKey()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "key unavailable")
		return
	}
	cfg := s.cfg
	summary := cfg.Fediverse.Summary
	if summary == "" {
		summary = cfg.Site.Description
	}
	actor := map[string]any{
		"@context":                  []any{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"},
		"id":                        s.actorID(),
		"type":                      "Person",
		"preferredUsername":         cfg.Fediverse.Handle,
		"name":                      cfg.Site.Title,
		"summary":                   "<p>" + escapeHTML(summary) + "</p>",
		"url":                       cfg.AbsURL(cfg.Site.BasePath),
		"inbox":                     s.apURL("/inbox"),
		"outbox":                    s.apURL("/outbox"),
		"followers":                 s.apURL("/followers"),
		"manuallyApprovesFollowers": false,
		"discoverable":              true,
		"endpoints":                 map[string]string{"sharedInbox": s.apURL("/inbox")},
		"publicKey":                 map[string]string{"id": s.actorID() + "#main-key", "owner": s.actorID(), "publicKeyPem": pub},
	}
	writeAP(w, actor)
}

func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// article is a post as an ActivityStreams Article.
func (s *Server) article(e *site.Entry) map[string]any {
	b := s.lastBuild()
	var tags []map[string]string
	for _, t := range e.Tags {
		tags = append(tags, map[string]string{"type": "Hashtag", "name": "#" + strings.ReplaceAll(t.Name, " ", ""), "href": s.cfg.AbsURL(t.URL)})
	}
	art := map[string]any{
		"id":           s.objectID(e.Slug),
		"type":         "Article",
		"name":         e.Title,
		"summary":      escapeHTML(e.Excerpt),
		"content":      b.FeedHTML(e),
		"url":          e.Permalink,
		"attributedTo": s.actorID(),
		"published":    e.Date.UTC().Format(time.RFC3339),
		"to":           []string{asPublic},
		"cc":           []string{s.apURL("/followers")},
		"tag":          tags,
	}
	if !e.Updated.IsZero() {
		art["updated"] = e.Updated.UTC().Format(time.RFC3339)
	}
	return art
}

func (s *Server) createActivity(e *site.Entry) map[string]any {
	return map[string]any{
		"@context":  "https://www.w3.org/ns/activitystreams",
		"id":        s.objectID(e.Slug) + "#create",
		"type":      "Create",
		"actor":     s.actorID(),
		"published": e.Date.UTC().Format(time.RFC3339),
		"to":        []string{asPublic},
		"cc":        []string{s.apURL("/followers")},
		"object":    s.article(e),
	}
}

func (s *Server) apOutbox(w http.ResponseWriter, r *http.Request) {
	b := s.lastBuild()
	var items []any
	if b != nil {
		for i, e := range b.Posts {
			if i >= 20 {
				break
			}
			items = append(items, s.createActivity(e))
		}
	}
	total := 0
	if b != nil {
		total = len(b.Posts)
	}
	writeAP(w, map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "id": s.apURL("/outbox"), "type": "OrderedCollection", "totalItems": total, "orderedItems": items})
}

// ---- remote actors ----

type remoteActor struct {
	ID     string
	Inbox  string
	Shared string
	Name   string
	URL    string
	Key    *rsa.PublicKey
	At     time.Time
}

var (
	actorCache   = map[string]*remoteActor{}
	actorCacheMu sync.Mutex
)

// fetchActor reads a remote actor (signed GET, so "authorized fetch"
// servers answer).
func (s *Server) fetchActor(ctx context.Context, id string) (*remoteActor, error) {
	id = strings.SplitN(id, "#", 2)[0]
	actorCacheMu.Lock()
	if a, ok := actorCache[id]; ok && time.Since(a.At) < 6*time.Hour {
		actorCacheMu.Unlock()
		return a, nil
	}
	actorCacheMu.Unlock()
	u, err := url.Parse(id)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("actor id is not a URL")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	req.Header.Set("Accept", apContentType+`, application/ld+json; profile="https://www.w3.org/ns/activitystreams"`)
	req.Header.Set("User-Agent", s.userAgent())
	if err := s.signRequest(req, nil); err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, &statusError{url: id, code: resp.StatusCode}
	}
	body, err := readLimited(resp.Body, 1<<20)
	if err != nil {
		return nil, err
	}
	var raw struct {
		ID        string `json:"id"`
		Inbox     string `json:"inbox"`
		Name      string `json:"name"`
		Preferred string `json:"preferredUsername"`
		URL       any    `json:"url"`
		Endpoints struct {
			SharedInbox string `json:"sharedInbox"`
		} `json:"endpoints"`
		PublicKey struct {
			ID    string `json:"id"`
			Owner string `json:"owner"`
			PEM   string `json:"publicKeyPem"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("actor is not JSON: %w", err)
	}
	if raw.ID != id || raw.PublicKey.Owner != "" && raw.PublicKey.Owner != raw.ID {
		return nil, errors.New("actor document does not match its id")
	}
	key, err := parsePublicKey(raw.PublicKey.PEM)
	if err != nil {
		return nil, err
	}
	a := &remoteActor{ID: raw.ID, Inbox: raw.Inbox, Shared: raw.Endpoints.SharedInbox, Name: firstNonEmpty(raw.Name, raw.Preferred), Key: key, At: time.Now()}
	if s, ok := raw.URL.(string); ok {
		a.URL = s
	}
	actorCacheMu.Lock()
	actorCache[id] = a
	actorCacheMu.Unlock()
	return a, nil
}

func (s *Server) actorKey(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	a, err := s.fetchActor(ctx, keyID)
	if err != nil {
		return nil, fmt.Errorf("could not fetch the signing key: %w", err)
	}
	return a.Key, nil
}

func (s *Server) forgetActorKey(keyID string) {
	actorCacheMu.Lock()
	delete(actorCache, strings.SplitN(keyID, "#", 2)[0])
	actorCacheMu.Unlock()
}

// ---- inbox ----

type activity struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Actor  string          `json:"actor"`
	Object json.RawMessage `json:"object"`
}

type apObject struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	URL       any    `json:"url"`
	InReplyTo string `json:"inReplyTo"`
	Content   string `json:"content"`
	Published string `json:"published"`
	Object    string `json:"object"`
}

func (s *Server) apInbox(w http.ResponseWriter, r *http.Request) {
	if !s.apiLimit.allow("ap:"+s.clientIP(r), s.now()) {
		httpError(w, http.StatusTooManyRequests, "slow down")
		return
	}
	body, err := readLimited(http.MaxBytesReader(w, r.Body, 1<<20), 1<<20)
	if err != nil {
		httpError(w, http.StatusRequestEntityTooLarge, "too large")
		return
	}
	var act activity
	if err := json.Unmarshal(body, &act); err != nil || act.Type == "" || act.Actor == "" {
		httpError(w, http.StatusBadRequest, "not an activity")
		return
	}
	keyID, err := s.verifyRequest(r, body)
	if err != nil {
		// Deletes of accounts we never knew arrive in floods; ignore them quietly.
		if act.Type == "Delete" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		httpError(w, http.StatusUnauthorized, "signature: "+err.Error())
		return
	}
	if strings.SplitN(keyID, "#", 2)[0] != act.Actor {
		httpError(w, http.StatusUnauthorized, "the signing key does not belong to the actor")
		return
	}
	id := act.ID
	if id == "" {
		h := sha256.Sum256(body)
		id = "urn:sha256:" + hex.EncodeToString(h[:])
	}
	if _, err := s.store.DB.Exec(`INSERT OR IGNORE INTO ap_activities (id, direction, type, actor, data, created_at) VALUES (?, 'in', ?, ?, ?, ?)`, id, act.Type, act.Actor, string(body), s.store.Unix()); err != nil {
		httpError(w, http.StatusInternalServerError, "could not store")
		return
	}
	if err := s.handleActivity(r.Context(), &act); err != nil {
		s.log.Printf("activitypub: %s from %s: %v", act.Type, act.Actor, err)
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleActivity(ctx context.Context, act *activity) error {
	var obj apObject
	var objID string
	if err := json.Unmarshal(act.Object, &objID); err != nil {
		_ = json.Unmarshal(act.Object, &obj)
		objID = obj.ID
	}
	switch act.Type {
	case "Follow":
		if objID != s.actorID() {
			return nil
		}
		a, err := s.fetchActor(ctx, act.Actor)
		if err != nil {
			return err
		}
		if _, err := s.store.DB.Exec(`INSERT INTO ap_followers (actor, inbox, shared_inbox, follow_id, created_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(actor) DO UPDATE SET inbox = excluded.inbox, shared_inbox = excluded.shared_inbox, follow_id = excluded.follow_id`,
			a.ID, a.Inbox, a.Shared, act.ID, s.store.Unix()); err != nil {
			return err
		}
		accept := map[string]any{
			"@context": "https://www.w3.org/ns/activitystreams", "id": s.actorID() + "#accept-" + randomToken(8),
			"type": "Accept", "actor": s.actorID(), "object": json.RawMessage(mustJSON(act)),
		}
		payload, _ := json.Marshal(map[string]any{"inbox": a.Inbox, "activity": accept})
		_, err = s.store.Enqueue("ap_send", "ap_send:accept:"+act.ID, string(payload), 0)
		return err
	case "Undo":
		var inner activity
		if json.Unmarshal(act.Object, &inner) == nil && inner.Type == "Follow" {
			_, err := s.store.DB.Exec(`DELETE FROM ap_followers WHERE actor = ?`, act.Actor)
			return err
		}
		if json.Unmarshal(act.Object, &inner) == nil && (inner.Type == "Like" || inner.Type == "Announce") {
			_, err := s.store.DB.Exec(`UPDATE webmentions SET status = 'deleted' WHERE source = ?`, inner.ID)
			return err
		}
	case "Create", "Update":
		if obj.InReplyTo == "" {
			return nil
		}
		e := s.entryForObject(obj.InReplyTo)
		if e == nil {
			return nil
		}
		a, err := s.fetchActor(ctx, act.Actor)
		if err != nil {
			return err
		}
		return s.saveFediverseResponse(obj.ID, e, "reply", a, obj.Content, obj.Published, urlOf(obj.URL, obj.ID))
	case "Like", "Announce":
		e := s.entryForObject(objID)
		if e == nil {
			return nil
		}
		a, err := s.fetchActor(ctx, act.Actor)
		if err != nil {
			return err
		}
		kind := map[string]string{"Like": "like", "Announce": "repost"}[act.Type]
		return s.saveFediverseResponse(act.ID, e, kind, a, "", "", act.ID)
	case "Delete":
		if objID == act.Actor {
			_, err := s.store.DB.Exec(`DELETE FROM ap_followers WHERE actor = ?`, act.Actor)
			return err
		}
		res, err := s.store.DB.Exec(`UPDATE webmentions SET status = 'deleted' WHERE source = ? AND status != 'deleted'`, objID)
		if n, _ := res.RowsAffected(); n > 0 {
			s.requestRebuild()
		}
		return err
	}
	return nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func urlOf(v any, fallback string) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		for _, y := range x {
			if s, ok := y.(string); ok {
				return s
			}
		}
	}
	return fallback
}

// entryForObject maps an object id or permalink to one of our posts.
func (s *Server) entryForObject(id string) *site.Entry {
	if strings.HasPrefix(id, s.apURL("/posts/")) {
		return s.entryBySlug(strings.TrimPrefix(id, s.apURL("/posts/")))
	}
	u, err := url.Parse(id)
	if err != nil || !strings.EqualFold(u.Hostname(), s.cfg.Domain()) {
		return nil
	}
	return s.entryByURLPath(u.Path)
}

// saveFediverseResponse stores a reply, like or boost as a mention that
// waits for moderation like any other.
func (s *Server) saveFediverseResponse(source string, e *site.Entry, kind string, a *remoteActor, content, published, link string) error {
	id, err := s.store.UpsertWebmention(source, e.Permalink, e.Slug)
	if err != nil {
		return err
	}
	wm, err := s.store.Webmention(id)
	if err != nil {
		return err
	}
	wm.Type = kind
	wm.Author = truncate(firstNonEmpty(a.Name, hostOf(a.ID)), 80)
	wm.AuthorURL = firstNonEmpty(a.URL, a.ID)
	if content != "" {
		wm.HTML = markdown.CommentPolicy().Sanitize(content)
	}
	if t, err := time.Parse(time.RFC3339, published); err == nil {
		wm.Published = t.Unix()
	}
	if wm.Status != "approved" && wm.Status != "spam" {
		wm.Status = "pending"
	}
	_ = link // the object id doubles as its address on Mastodon and most servers
	return s.store.SaveWebmention(wm)
}

// fediverseReplies are already in the webmentions table.
func (s *Server) fediverseReplies(e *site.Entry) []*site.Response { return nil }

// ---- delivery ----

// jobAPDeliver sends a new post to every follower's inbox, one job per
// inbox so each retries on its own.
func (s *Server) jobAPDeliver(ctx context.Context, j *store.Job) (string, error) {
	var p struct {
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal([]byte(j.Payload), &p)
	e := s.entryBySlug(p.Slug)
	if e == nil || e.Unlisted {
		return "post gone or unlisted", nil
	}
	inboxes, err := s.followerInboxes()
	if err != nil {
		return "", err
	}
	act := s.createActivity(e)
	for _, inbox := range inboxes {
		payload, _ := json.Marshal(map[string]any{"inbox": inbox, "activity": act})
		_, _ = s.store.Enqueue("ap_send", "ap_send:"+p.Slug+":"+inbox, string(payload), 0)
	}
	return fmt.Sprintf("%d inbox(es)", len(inboxes)), nil
}

// followerInboxes lists distinct inboxes, preferring shared ones.
func (s *Server) followerInboxes() ([]string, error) {
	rows, err := s.store.DB.Query(`SELECT DISTINCT CASE WHEN shared_inbox != '' THEN shared_inbox ELSE inbox END FROM ap_followers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var x string
		if rows.Scan(&x) == nil {
			out = append(out, x)
		}
	}
	return out, rows.Err()
}

func (s *Server) jobAPSend(ctx context.Context, j *store.Job) (string, error) {
	var p struct {
		Inbox    string          `json:"inbox"`
		Activity json.RawMessage `json:"activity"`
	}
	if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
		return "", err
	}
	if err := s.signedPost(bytes.NewReader(p.Activity), p.Inbox); err != nil {
		var se *statusError
		if errors.As(err, &se) && se.code == http.StatusGone {
			_, _ = s.store.DB.Exec(`DELETE FROM ap_followers WHERE inbox = ? OR shared_inbox = ?`, p.Inbox, p.Inbox)
			return "inbox gone; follower removed", nil
		}
		return "", err
	}
	return "delivered", nil
}

// announceDelete tells followers a post was removed.
func (s *Server) announceDelete(slug string) {
	if !s.apOn() {
		return
	}
	inboxes, _ := s.followerInboxes()
	del := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams", "id": s.objectID(slug) + "#delete", "type": "Delete",
		"actor": s.actorID(), "to": []string{asPublic}, "object": map[string]string{"id": s.objectID(slug), "type": "Tombstone"},
	}
	for _, inbox := range inboxes {
		payload, _ := json.Marshal(map[string]any{"inbox": inbox, "activity": del})
		_, _ = s.store.Enqueue("ap_send", "ap_send:delete:"+slug+":"+inbox, string(payload), 0)
	}
}
