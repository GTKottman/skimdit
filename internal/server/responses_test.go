// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const commentsOn = "[comments]\nenabled = true\npow_bits = 8\n"

// solvePow does what the comment form's script does.
func (h *harness) solvePow() string {
	h.t.Helper()
	_, body := h.req("GET", "/api/pow", nil, nil)
	var c struct {
		Challenge string `json:"challenge"`
		Bits      int    `json:"bits"`
	}
	json.Unmarshal([]byte(body), &c)
	for n := 0; ; n++ {
		v := fmt.Sprintf("%s:%d", c.Challenge, n)
		sum := sha256.Sum256([]byte(v))
		if leadingZeroBits(sum[:]) >= c.Bits {
			return v
		}
	}
}

func (h *harness) comment(fields ...string) (*http.Response, string) {
	v := url.Values{"post": {"existing"}, "name": {"Reader"}, "body": {"Great post, *thanks*!"}, "pow": {h.solvePow()}}
	for i := 0; i+1 < len(fields); i += 2 {
		v.Set(fields[i], fields[i+1])
	}
	return h.req("POST", "/api/comments", v, nil)
}

func TestCommentsFlow(t *testing.T) {
	h := newHarnessWith(t, commentsOn, nil)
	h.setup()
	mailer := h.srv.testMailer.(*recordingMailer)

	// Spam defences.
	if resp, _ := h.req("POST", "/api/comments", url.Values{"post": {"existing"}, "name": {"Bot"}, "body": {"hi"}, "pow": {"fake:1"}}, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad proof-of-work accepted: %d", resp.StatusCode)
	}
	pow := h.solvePow()
	h.req("POST", "/api/comments", url.Values{"post": {"existing"}, "name": {"A"}, "body": {"one"}, "pow": {pow}}, nil)
	if resp, _ := h.req("POST", "/api/comments", url.Values{"post": {"existing"}, "name": {"A"}, "body": {"two"}, "pow": {pow}}, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("reused proof-of-work accepted: %d", resp.StatusCode)
	}
	h.comment("website", "http://spam.example") // honeypot: silently dropped
	if resp, _ := h.comment("body", "http://a http://b http://c"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("too many links accepted: %d", resp.StatusCode)
	}
	all, _ := h.srv.store.Comments("", 50)
	if len(all) != 1 || all[0].Body != "one" {
		t.Fatalf("stored comments: %+v", all)
	}
	h.srv.store.SetCommentStatus(all[0].ID, "deleted")

	// A real comment with reply notifications (double opt-in).
	h.clock.add(11 * time.Minute) // past the per-address limit window
	resp, body := h.comment("email", "reader@example.org", "notify", "1", "body", "Great post, *thanks*! <script>x</script>")
	if resp.StatusCode != 200 || !strings.Contains(body, "approved") {
		t.Fatalf("comment: %d %s", resp.StatusCode, body)
	}
	h.srv.RunJobs(context.Background())
	confirm := mailer.find("reader@example.org", "Confirm reply emails")
	owner := mailer.find("ada@blog.example", "waiting for approval")
	if confirm == nil || owner == nil {
		t.Fatalf("mails: %+v", mailer.sent)
	}
	// Not on the page until approved.
	if _, page := h.req("GET", "/posts/existing/", nil, nil); strings.Contains(page, "Great post") {
		t.Fatal("pending comment is public")
	}
	pending, _ := h.srv.store.Comments("pending", 10)
	if len(pending) != 1 || strings.Contains(pending[0].HTML, "<script") || !strings.Contains(pending[0].HTML, "<em>thanks</em>") {
		t.Fatalf("pending: %+v", pending)
	}
	c := pending[0]
	// Confirm reply emails: GET shows a button, POST confirms.
	link := confirm.Text[strings.Index(confirm.Text, "https://"):]
	link = strings.Fields(link)[0]
	u, _ := url.Parse(link)
	if r, _ := h.req("GET", u.Path+"?"+u.RawQuery, nil, nil); r.StatusCode != 200 {
		t.Fatalf("confirm page: %d", r.StatusCode)
	}
	h.req("POST", u.Path+"?"+u.RawQuery, url.Values{}, nil)
	if got, _ := h.srv.store.Comment(c.ID); got.Notify != 2 {
		t.Fatalf("notify = %d after confirming", got.Notify)
	}
	// Approve: it appears on the post after the rebuild.
	h.form("/admin/comments/action", "kind", "comment", "id", fmt.Sprint(c.ID), "action", "approve")
	if err := h.srv.rebuild(); err != nil {
		t.Fatal(err)
	}
	if _, page := h.req("GET", "/posts/existing/", nil, nil); !strings.Contains(page, "<em>thanks</em>") || !strings.Contains(page, "Reader") {
		t.Fatal("approved comment not on the page")
	}
	// The owner replies; the reader gets an email with an unsubscribe header.
	h.form("/admin/comments/action", "kind", "comment", "id", fmt.Sprint(c.ID), "action", "reply", "body", "Thank you!")
	h.srv.RunJobs(context.Background())
	reply := mailer.find("reader@example.org", "replied to your comment")
	if reply == nil || reply.ListUnsubscribe == "" {
		t.Fatalf("reply mail: %+v", reply)
	}
	h.srv.rebuild()
	if _, page := h.req("GET", "/posts/existing/", nil, nil); !strings.Contains(page, "Thank you!") || !strings.Contains(page, "nested") {
		t.Fatal("owner reply not threaded on the page")
	}
}

func TestWebmentionsInAndOut(t *testing.T) {
	var targetHits, endpointHits int
	var received url.Values
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/reply":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<html><body><div class="h-entry"><a class="p-author h-card" href="https://bob.example">Bob</a>
				<a class="u-in-reply-to" href="https://blog.example/posts/existing/">in reply</a>
				<div class="e-content">I disagree, <b>politely</b>. <script>bad()</script></div>
				<time class="dt-published" datetime="2026-10-24T10:00:00Z">yesterday</time></div></body></html>`)
		case "/nolink":
			fmt.Fprint(w, `<html><body>nothing here</body></html>`)
		case "/article":
			targetHits++
			w.Header().Set("Link", `</wm-endpoint>; rel="webmention"`)
			fmt.Fprint(w, `<html><body>An article.</body></html>`)
		case "/wm-endpoint":
			endpointHits++
			r.ParseForm()
			received = r.PostForm
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer ext.Close()
	h := newHarnessWith(t, "[comments]\nenabled = false\n", ext.Client())
	h.setup()
	ctx := context.Background()

	// Receiving: bad targets are refused, good ones verified then moderated.
	if r, _ := h.req("POST", "/webmention", url.Values{"source": {ext.URL + "/reply"}, "target": {"https://other.example/x"}}, nil); r.StatusCode != http.StatusBadRequest {
		t.Errorf("foreign target accepted: %d", r.StatusCode)
	}
	if r, _ := h.req("POST", "/webmention", url.Values{"source": {ext.URL + "/reply"}, "target": {"https://blog.example/posts/existing/"}}, nil); r.StatusCode != http.StatusAccepted {
		t.Fatalf("webmention: %d", r.StatusCode)
	}
	h.req("POST", "/webmention", url.Values{"source": {ext.URL + "/nolink"}, "target": {"https://blog.example/posts/existing/"}}, nil)
	h.srv.RunJobs(ctx)
	pending, _ := h.srv.store.Webmentions("pending", 10)
	invalid, _ := h.srv.store.Webmentions("invalid", 10)
	if len(pending) != 1 || len(invalid) != 1 {
		t.Fatalf("pending %d invalid %d", len(pending), len(invalid))
	}
	wm := pending[0]
	if wm.Type != "reply" || wm.Author != "Bob" || strings.Contains(wm.HTML, "<script") || !strings.Contains(wm.HTML, "<b>politely</b>") {
		t.Fatalf("parsed webmention: %+v", wm)
	}
	h.form("/admin/comments/action", "kind", "mention", "id", fmt.Sprint(wm.ID), "action", "approve")
	h.srv.rebuild()
	if _, page := h.req("GET", "/posts/existing/", nil, nil); !strings.Contains(page, "politely") || !strings.Contains(page, "Bob") {
		t.Fatal("approved webmention not on the page")
	}

	// Sending: a new post linking out notifies the linked page's endpoint once.
	d := h.newDraft("Linking out", "See [this article]("+ext.URL+"/article) and [an internal one](/posts/existing/).\n")
	h.form(d + "/publish")
	h.srv.RunJobs(ctx) // announce -> webmention_send
	h.srv.RunJobs(ctx)
	if endpointHits != 1 || received.Get("source") != "https://blog.example/posts/linking-out/" || received.Get("target") != ext.URL+"/article" {
		t.Fatalf("sent webmention: hits %d form %v", endpointHits, received)
	}
	h.srv.requestRebuild()
	h.srv.RunJobs(ctx)
	if endpointHits != 1 {
		t.Error("webmention sent twice")
	}
}

func TestSafeClientRefusesPrivateAddresses(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()
	_, err := safeHTTPClient().Get(ts.URL)
	if err == nil || !strings.Contains(err.Error(), "private or local") {
		t.Fatalf("loopback fetch allowed: %v", err)
	}
}
