// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const newsletterOn = "[comments]\nenabled = false\n[newsletter]\nenabled = true\nfrom = \"Test <letters@blog.example>\"\nsmtp_host = \"mail.example\"\nbatch_size = 2\nbatch_pause_seconds = 30\n"

func linkIn(text string) (string, string) {
	i := strings.Index(text, "https://blog.example")
	u, _ := url.Parse(strings.Fields(text[i:])[0])
	return u.Path, u.RawQuery
}

func TestNewsletterDoubleOptInAndSend(t *testing.T) {
	h := newHarnessWith(t, newsletterOn, nil)
	h.setup()
	mailer := h.srv.testMailer.(*recordingMailer)
	ctx := context.Background()

	for _, addr := range []string{"one@example.org", "two@example.org", "three@example.org"} {
		resp, body := h.req("POST", "/api/subscribe", url.Values{"email": {addr}, "source": {"/posts/existing/"}}, nil)
		if resp.StatusCode != 200 || !strings.Contains(body, "Check your inbox") {
			t.Fatalf("subscribe %s: %d", addr, resp.StatusCode)
		}
		h.clock.add(11 * time.Minute)
	}
	if resp, _ := h.req("POST", "/api/subscribe", url.Values{"email": {"not an email"}}, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad address accepted: %d", resp.StatusCode)
	}
	h.srv.RunJobs(ctx)
	// Confirm two of the three; the third never clicks.
	for _, addr := range []string{"one@example.org", "two@example.org"} {
		m := mailer.find(addr, "Confirm your subscription")
		if m == nil {
			t.Fatalf("no confirmation mail for %s", addr)
		}
		p, q := linkIn(m.Text)
		if r, body := h.req("GET", p+"?"+q, nil, nil); r.StatusCode != 200 || !strings.Contains(body, "<form") {
			t.Fatalf("confirm page: %d", r.StatusCode)
		}
		h.req("POST", p+"?"+q, url.Values{}, nil)
	}
	counts := h.srv.store.SubscriberCounts()
	if counts["active"] != 2 || counts["pending"] != 1 {
		t.Fatalf("counts: %v", counts)
	}
	// Signing up again as an active subscriber gives the same answer and no mail.
	before := len(mailer.sent)
	h.clock.add(11 * time.Minute)
	if _, body := h.req("POST", "/api/subscribe", url.Values{"email": {"one@example.org"}}, nil); !strings.Contains(body, "Check your inbox") {
		t.Error("an existing address is told apart from a new one")
	}
	h.srv.RunJobs(ctx)
	if len(mailer.sent) != before {
		t.Error("existing subscriber got a confirmation mail")
	}

	// Publishing sends the post to active subscribers only, in batches.
	d := h.newDraft("Fresh news", "The *body* of the post.\n")
	h.form(d + "/publish")
	h.srv.RunJobs(ctx) // announce, newsletter batch 1
	h.srv.RunJobs(ctx)
	got := func() []*Mail {
		var out []*Mail
		for _, m := range mailer.sent {
			if m.Subject == "Fresh news" {
				out = append(out, m)
			}
		}
		return out
	}
	if n := len(got()); n != 2 {
		t.Fatalf("sent %d post emails, want 2 (batch of 2)", n)
	}
	m := got()[0]
	if m.ListUnsubscribe == "" || !strings.Contains(m.HTML, "<em>body</em>") || !strings.Contains(m.Text, "Unsubscribe:") || strings.Contains(m.HTML, "heading-anchor") {
		t.Fatalf("post email: %+v", m)
	}
	// The next batch waits for its pause; nothing is sent twice.
	h.clock.add(time.Minute)
	h.srv.RunJobs(ctx)
	if n := len(got()); n != 2 {
		t.Errorf("sent %d after the last batch, want still 2", n)
	}
	// One-click unsubscribe (what mail apps do with List-Unsubscribe-Post).
	u, _ := url.Parse(m.ListUnsubscribe)
	h.req("POST", u.Path+"?"+u.RawQuery, url.Values{"List-Unsubscribe": {"One-Click"}}, nil)
	if c := h.srv.store.SubscriberCounts(); c["active"] != 1 || c["unsubscribed"] != 1 {
		t.Fatalf("after unsubscribe: %v", c)
	}
	// CSV export.
	if resp, body := h.req("GET", "/admin/subscribers.csv", nil, nil); resp.StatusCode != 200 || !strings.Contains(body, "one@example.org") || !strings.HasPrefix(body, "email,status") {
		t.Fatalf("csv: %d %s", resp.StatusCode, body)
	}
}

func TestParseBounce(t *testing.T) {
	hard := "Content-Type: message/delivery-status\r\n\r\nReporting-MTA: dns; mx.example\r\n\r\nFinal-Recipient: rfc822; Gone@Example.org\r\nAction: failed\r\nStatus: 5.1.1\r\n"
	soft := "Final-Recipient: rfc822;<full@example.org>\r\nAction: delayed\r\nStatus: 4.2.2\r\n"
	ok := "Final-Recipient: rfc822; fine@example.org\r\nAction: delivered\r\nStatus: 2.0.0\r\n"
	abuse := "Content-Type: message/feedback-report\r\n\r\nFeedback-Type: abuse\r\nOriginal-Rcpt-To: <angry@example.org>\r\n"
	if b := parseBounce([]byte(hard)); b == nil || !b.Hard || b.Email != "gone@example.org" {
		t.Errorf("hard: %+v", b)
	}
	if b := parseBounce([]byte(soft)); b == nil || b.Hard || b.Email != "full@example.org" {
		t.Errorf("soft: %+v", b)
	}
	if b := parseBounce([]byte(ok)); b != nil {
		t.Errorf("delivered parsed as bounce: %+v", b)
	}
	if b := parseBounce([]byte(abuse)); b == nil || !b.Complaint || b.Email != "angry@example.org" {
		t.Errorf("complaint: %+v", b)
	}
	if b := parseBounce([]byte("Subject: hello\r\n\r\nJust a reply.")); b != nil {
		t.Errorf("plain mail parsed as bounce: %+v", b)
	}
}

func TestBouncesTurnOffSubscribers(t *testing.T) {
	h := newHarnessWith(t, newsletterOn, nil)
	st := h.srv.store
	for _, e := range []string{"a@x.org", "b@x.org", "c@x.org"} {
		sub, _ := st.AddPendingSubscriber(e, "tok-"+e, "/")
		st.ConfirmSubscriber(sub.ID, "")
	}
	h.srv.applyBounce(&Bounce{Email: "a@x.org", Hard: true})
	for i := 0; i < 3; i++ {
		h.srv.applyBounce(&Bounce{Email: "b@x.org"})
	}
	h.srv.applyBounce(&Bounce{Email: "c@x.org", Complaint: true})
	if h.srv.ourBounce([]byte("Final-Recipient: rfc822; a@x.org\r\nStatus: 5.1.1\r\nMessage-ID: <abc@elsewhere.example>")) {
		t.Error("a bounce of someone else's mail was accepted")
	}
	if !h.srv.ourBounce([]byte("Final-Recipient: rfc822; a@x.org\r\nStatus: 5.1.1\r\n\r\nMessage-ID: <post-x-1@blog.example>")) {
		t.Error("a bounce of our own mail was refused")
	}
	c := st.SubscriberCounts()
	if c["bounced"] != 2 || c["complained"] != 1 || c["active"] != 0 {
		t.Fatalf("counts: %v", c)
	}
}

func TestStatsCountWithoutTracking(t *testing.T) {
	h := newHarness(t)
	h.setup()
	browser := map[string]string{"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0", "Referer": "https://news.example.com/item?id=1"}
	for i := 0; i < 3; i++ {
		h.req("GET", "/posts/existing/", nil, browser)
	}
	h.req("GET", "/posts/existing/", nil, map[string]string{"User-Agent": "Googlebot/2.1"})
	h.req("GET", "/assets/x.css", nil, browser)
	h.req("GET", "/feed.xml", nil, map[string]string{"User-Agent": "NetNewsWire"})
	resp, _ := h.req("GET", "/posts/existing/", nil, browser)
	for _, c := range resp.Cookies() {
		t.Errorf("reader got a cookie: %s", c.Name)
	}
	h.srv.stats.flush()
	day := h.clock.now().UTC().Format("2006-01-02")
	pages := h.srv.store.TopPaths(day, "page", 10)
	if len(pages) != 1 || pages[0].Label != "/posts/existing/" || pages[0].N != 4 {
		t.Fatalf("pages: %+v", pages)
	}
	if refs := h.srv.store.TopReferrers(day, 10); len(refs) != 1 || refs[0].Label != "news.example.com" {
		t.Fatalf("referrers: %+v", refs)
	}
	if feeds := h.srv.store.TopPaths(day, "feed", 10); len(feeds) != 1 || feeds[0].N != 1 {
		t.Fatalf("feeds: %+v", feeds)
	}
	var n int
	h.srv.store.DB.QueryRow(`SELECT COUNT(*) FROM stats_daily WHERE path LIKE '%Firefox%' OR referrer LIKE '%item%'`).Scan(&n)
	if n != 0 {
		t.Error("stats stored more than a domain and a path")
	}
	if resp, body := h.req("GET", "/admin/stats", nil, nil); resp.StatusCode != 200 || !strings.Contains(body, "news.example.com") {
		t.Fatalf("stats page: %d", resp.StatusCode)
	}
}
