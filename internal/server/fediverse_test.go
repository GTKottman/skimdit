// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// fakeFedi is a remote fediverse server with one user, alice, plus fake
// Mastodon and Bluesky APIs.
type fakeFedi struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	mu       sync.Mutex
	inbox    []map[string]any
	inboxSig []string
	statuses []map[string]any
	headers  []http.Header
	records  []map[string]any
}

func newFakeFedi(t *testing.T) *fakeFedi {
	f := &fakeFedi{}
	f.key, _ = rsa.GenerateKey(rand.Reader, 2048)
	pubDER, _ := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/users/alice":
			w.Header().Set("Content-Type", apContentType)
			json.NewEncoder(w).Encode(map[string]any{
				"id": f.actor(), "type": "Person", "preferredUsername": "alice", "name": "Alice", "url": f.srv.URL + "/@alice",
				"inbox": f.srv.URL + "/users/alice/inbox", "endpoints": map[string]string{"sharedInbox": f.srv.URL + "/inbox"},
				"publicKey": map[string]string{"id": f.actor() + "#main-key", "owner": f.actor(), "publicKeyPem": pubPEM},
			})
		case "/inbox", "/users/alice/inbox":
			var m map[string]any
			json.NewDecoder(r.Body).Decode(&m)
			f.inbox = append(f.inbox, m)
			f.inboxSig = append(f.inboxSig, r.Header.Get("Signature"))
			w.WriteHeader(http.StatusAccepted)
		case "/api/v1/statuses":
			var m map[string]any
			json.NewDecoder(r.Body).Decode(&m)
			f.statuses = append(f.statuses, m)
			f.headers = append(f.headers, r.Header.Clone())
			json.NewEncoder(w).Encode(map[string]string{"url": f.srv.URL + "/@me/111"})
		case "/xrpc/com.atproto.server.createSession":
			json.NewEncoder(w).Encode(map[string]string{"accessJwt": "jwt", "did": "did:plc:abc", "handle": "me.bsky.social"})
		case "/xrpc/com.atproto.repo.createRecord":
			if r.Header.Get("Authorization") != "Bearer jwt" {
				w.WriteHeader(401)
				return
			}
			var m map[string]any
			json.NewDecoder(r.Body).Decode(&m)
			f.records = append(f.records, m)
			json.NewEncoder(w).Encode(map[string]string{"uri": "at://did:plc:abc/app.bsky.feed.post/3kxyz", "cid": "x"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeFedi) actor() string { return f.srv.URL + "/users/alice" }

// send posts a signed activity to our inbox as alice.
func (f *fakeFedi) send(t *testing.T, h *harness, act map[string]any, key *rsa.PrivateKey) int {
	t.Helper()
	body, _ := json.Marshal(act)
	req, _ := http.NewRequest("POST", h.http.URL+"/ap/inbox", bytes.NewReader(body))
	req.Header.Set("Content-Type", apContentType)
	req.Header.Set("Date", h.clock.now().UTC().Format(http.TimeFormat))
	req.Header.Set("Digest", digestHeader(body))
	headers := []string{"(request-target)", "host", "date", "digest"}
	req.Host = strings.TrimPrefix(h.http.URL, "http://")
	sum := sha256.Sum256([]byte(signingString(req, headers)))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	req.Header.Set("Signature", fmt.Sprintf(`keyId="%s#main-key",algorithm="rsa-sha256",headers="%s",signature="%s"`, f.actor(), strings.Join(headers, " "), base64.StdEncoding.EncodeToString(sig)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

func TestFediverse(t *testing.T) {
	f := newFakeFedi(t)
	cfg := "[comments]\nenabled = false\n[fediverse]\nenabled = true\nhandle = \"blog\"\n" +
		"[crosspost.mastodon]\nenabled = true\ninstance = \"" + f.srv.URL + "\"\n" +
		"[crosspost.bluesky]\nenabled = true\nhandle = \"me.bsky.social\"\npds = \"" + f.srv.URL + "\"\n"
	t.Setenv("SKIMDIT_MASTODON_TOKEN", "tok")
	t.Setenv("SKIMDIT_BLUESKY_PASSWORD", "app-pass")
	h := newHarnessWith(t, cfg, f.srv.Client())
	h.setup()
	ctx := context.Background()

	// Discovery.
	resp, body := h.req("GET", "/.well-known/webfinger?resource=acct:blog@blog.example", nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(body, `"href":"https://blog.example/ap/actor"`) {
		t.Fatalf("webfinger: %d %s", resp.StatusCode, body)
	}
	if _, body := h.req("GET", "/ap/actor", nil, nil); !strings.Contains(body, "BEGIN PUBLIC KEY") || !strings.Contains(body, `"preferredUsername":"blog"`) {
		t.Fatalf("actor: %s", body)
	}

	// A forged signature is refused; a real follow is accepted and answered.
	follow := map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "id": f.actor() + "#follow-1", "type": "Follow", "actor": f.actor(), "object": "https://blog.example/ap/actor"}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	if code := f.send(t, h, follow, other); code != http.StatusUnauthorized {
		t.Fatalf("forged follow: %d", code)
	}
	if code := f.send(t, h, follow, f.key); code != http.StatusAccepted {
		t.Fatalf("follow: %d", code)
	}
	h.srv.RunJobs(ctx)
	if len(f.inbox) != 1 || f.inbox[0]["type"] != "Accept" || f.inboxSig[0] == "" {
		t.Fatalf("accept not delivered: %+v", f.inbox)
	}

	// A new post goes to the follower (once, signed), Mastodon and Bluesky.
	d := h.newDraft("Hello fediverse", "A post for **everyone**.\n")
	h.form(d + "/publish")
	for i := 0; i < 3; i++ {
		h.srv.RunJobs(ctx)
	}
	var create map[string]any
	for _, m := range f.inbox {
		if m["type"] == "Create" {
			create = m
		}
	}
	if create == nil {
		t.Fatalf("post not delivered: %+v", f.inbox)
	}
	obj := create["object"].(map[string]any)
	if obj["name"] != "Hello fediverse" || !strings.Contains(obj["content"].(string), "<strong>everyone</strong>") || obj["url"] != "https://blog.example/posts/hello-fediverse/" {
		t.Fatalf("article: %+v", obj)
	}
	if len(f.statuses) != 1 || !strings.Contains(f.statuses[0]["status"].(string), "https://blog.example/posts/hello-fediverse/") ||
		f.headers[0].Get("Authorization") != "Bearer tok" || f.headers[0].Get("Idempotency-Key") == "" {
		t.Fatalf("mastodon: %+v %+v", f.statuses, f.headers)
	}
	if len(f.records) != 1 {
		t.Fatalf("bluesky records: %d", len(f.records))
	}
	rec := f.records[0]["record"].(map[string]any)
	text := rec["text"].(string)
	idx := rec["facets"].([]any)[0].(map[string]any)["index"].(map[string]any)
	start, end := int(idx["byteStart"].(float64)), int(idx["byteEnd"].(float64))
	if text[start:end] != "https://blog.example/posts/hello-fediverse/" || utf8.RuneCountInString(text) > 300 {
		t.Fatalf("bluesky facet points at %q", text[start:end])
	}
	h.srv.rebuild()
	if _, page := h.req("GET", "/posts/hello-fediverse/", nil, nil); !strings.Contains(page, "u-syndication") || !strings.Contains(page, "https://bsky.app/profile/me.bsky.social/post/3kxyz") {
		t.Fatal("syndication links not on the post")
	}
	// Retries never post twice.
	h.srv.store.Enqueue("mastodon", "mastodon:again", `{"slug":"hello-fediverse"}`, 0)
	h.srv.RunJobs(ctx)
	if len(f.statuses) != 1 {
		t.Error("cross-posted twice")
	}

	// A reply and a like wait for moderation; a delete removes the reply.
	note := map[string]any{"id": f.srv.URL + "/notes/1", "type": "Note", "content": "<p>Nice post! <script>x</script></p>", "inReplyTo": "https://blog.example/ap/posts/hello-fediverse", "published": "2026-10-25T13:00:00Z"}
	f.send(t, h, map[string]any{"id": f.srv.URL + "/notes/1/activity", "type": "Create", "actor": f.actor(), "object": note}, f.key)
	f.send(t, h, map[string]any{"id": f.srv.URL + "/likes/1", "type": "Like", "actor": f.actor(), "object": "https://blog.example/ap/posts/hello-fediverse"}, f.key)
	pending, _ := h.srv.store.Webmentions("pending", 10)
	if len(pending) != 2 {
		t.Fatalf("pending: %+v", pending)
	}
	for _, w := range pending {
		if w.Type == "reply" && (strings.Contains(w.HTML, "<script") || !strings.Contains(w.HTML, "Nice post!") || w.Author != "Alice") {
			t.Fatalf("reply: %+v", w)
		}
		h.form("/admin/comments/action", "kind", "mention", "id", fmt.Sprint(w.ID), "action", "approve")
	}
	h.srv.rebuild()
	if _, page := h.req("GET", "/posts/hello-fediverse/", nil, nil); !strings.Contains(page, "Nice post!") || !strings.Contains(page, "Alice") {
		t.Fatal("approved fediverse reply not on the page")
	}
	f.send(t, h, map[string]any{"id": f.srv.URL + "/notes/1#delete", "type": "Delete", "actor": f.actor(), "object": map[string]string{"id": f.srv.URL + "/notes/1", "type": "Tombstone"}}, f.key)
	if gone, _ := h.srv.store.Webmentions("deleted", 10); len(gone) != 1 {
		t.Fatalf("deleted reply still shown: %+v", gone)
	}

	// Unfollow.
	f.send(t, h, map[string]any{"id": f.actor() + "#undo-1", "type": "Undo", "actor": f.actor(), "object": follow}, f.key)
	if n, _ := h.srv.followerInboxes(); len(n) != 0 {
		t.Fatalf("follower kept after Undo: %v", n)
	}
}

func TestFediverseOffByDefault(t *testing.T) {
	h := newHarness(t)
	if resp, _ := h.req("GET", "/.well-known/webfinger?resource=acct:x@blog.example", nil, nil); resp.StatusCode != 404 {
		t.Errorf("webfinger answered while off: %d", resp.StatusCode)
	}
	if resp, _ := h.req("GET", "/ap/actor", nil, nil); resp.StatusCode != 404 {
		t.Errorf("actor answered while off: %d", resp.StatusCode)
	}
}
