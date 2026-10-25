// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/GTKottman/skimdit/internal/site"
	"github.com/GTKottman/skimdit/internal/store"
	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

func (s *Server) newsletterRoutes(w http.ResponseWriter, r *http.Request, p string) bool {
	switch {
	case p == "/subscribe" && r.Method == http.MethodPost:
		s.subscribe(w, r)
	case p == "/subscribe/confirm":
		s.confirmSubscription(w, r)
	case p == "/unsubscribe":
		s.unsubscribe(w, r)
	default:
		return false
	}
	return true
}

// subscribe starts a double opt-in. The answer is the same whether or not
// the address is already subscribed, so nobody can probe the list.
func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	back := s.cfg.Rel("/")
	if !s.cfg.Newsletter.Enabled {
		httpError(w, http.StatusNotFound, "the newsletter is off")
		return
	}
	ip := s.clientIP(r)
	if !s.postLimit.allow("subscribe:"+ip, s.now()) {
		s.notice(w, http.StatusTooManyRequests, "Slow down", "Too many sign-ups from your connection. Try again in a few minutes.", back)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	email := strings.TrimSpace(r.FormValue("email"))
	source := truncate(r.FormValue("source"), 200)
	if strings.HasPrefix(source, "/") {
		back = source
	}
	done := func() {
		s.notice(w, http.StatusOK, "Check your inbox", "Almost done: click the link in the email we just sent to confirm. Nothing is sent until you do.", back)
	}
	if r.FormValue("website") != "" {
		done()
		return
	}
	a, err := mail.ParseAddress(email)
	if err != nil || a.Address != email || len(email) > 254 || !strings.Contains(email[strings.LastIndexByte(email, '@'):], ".") {
		s.notice(w, http.StatusBadRequest, "That didn't work", "Check the email address and try again.", back)
		return
	}
	sub, err := s.store.AddPendingSubscriber(strings.ToLower(email), randomToken(24), source)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not save")
		return
	}
	if sub.Status == "pending" {
		payload, _ := json.Marshal(map[string]int64{"subscriber": sub.ID})
		// At most one confirmation email per address per hour.
		_, _ = s.store.Enqueue("newsletter_confirm", fmt.Sprintf("newsletter_confirm:%d:%d", sub.ID, s.now().Unix()/3600), string(payload), 0)
	}
	done()
}

func (s *Server) confirmSubscription(w http.ResponseWriter, r *http.Request) {
	sub, err := s.store.SubscriberByToken(r.URL.Query().Get("t"))
	if err != nil || (sub.Status != "pending" && sub.Status != "active") {
		s.notice(w, http.StatusNotFound, "Link not valid", "This link is not valid any more. Sign up again from the site.", s.cfg.Rel("/"))
		return
	}
	if r.Method != http.MethodPost {
		s.confirmForm(w, "Confirm your subscription", "Click the button to get an email for each new post from "+s.cfg.Site.Title+".", "Confirm")
		return
	}
	if sub.Status == "pending" {
		_ = s.store.ConfirmSubscriber(sub.ID, s.ipHash(s.clientIP(r)))
	}
	s.notice(w, http.StatusOK, "You're subscribed", "Thanks for reading. Every email has a one-click unsubscribe link.", s.cfg.Rel("/"))
}

// unsubscribe works with a button (GET shows it) and with one-click
// unsubscribe from mail apps (RFC 8058 POST).
func (s *Server) unsubscribe(w http.ResponseWriter, r *http.Request) {
	sub, err := s.store.SubscriberByToken(r.URL.Query().Get("t"))
	if r.Method != http.MethodPost {
		s.confirmForm(w, "Unsubscribe?", "Click the button and you won't get any more emails from "+s.cfg.Site.Title+".", "Unsubscribe")
		return
	}
	if err == nil {
		_ = s.store.SetSubscriberStatus(sub.ID, "unsubscribed")
	}
	s.notice(w, http.StatusOK, "You're unsubscribed", "You won't get any more emails.", s.cfg.Rel("/"))
}

func (s *Server) subscriberFromJob(j *store.Job) (*store.Subscriber, error) {
	var p struct {
		Subscriber int64 `json:"subscriber"`
	}
	if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
		return nil, err
	}
	return s.store.Subscriber(p.Subscriber)
}

func (s *Server) jobNewsletterConfirm(ctx context.Context, j *store.Job) (string, error) {
	sub, err := s.subscriberFromJob(j)
	if err != nil || sub.Status != "pending" {
		return "nothing to confirm", nil
	}
	link := s.cfg.AbsURL(s.cfg.Rel("/api/subscribe/confirm")) + "?t=" + url.QueryEscape(sub.Token)
	m := &Mail{To: sub.Email, Subject: "Confirm your subscription to " + s.cfg.Site.Title,
		Text: fmt.Sprintf("Someone (hopefully you) asked to get new posts from %s by email.\n\nConfirm here:\n%s\n\nIf this wasn't you, ignore this email and nothing more will be sent.\n", s.cfg.Site.Title, link)}
	return "sent", s.mailer().Send(ctx, []*Mail{m})
}

// ---- sending posts ----

// jobNewsletter sends one batch of a post's newsletter and queues the next
// batch after a pause, so a big list never floods the mail server.
func (s *Server) jobNewsletter(ctx context.Context, j *store.Job) (string, error) {
	var p struct {
		Slug  string `json:"slug"`
		Batch int    `json:"batch"`
	}
	_ = json.Unmarshal([]byte(j.Payload), &p)
	e := s.entryBySlug(p.Slug)
	if e == nil {
		return "post gone", nil
	}
	if p.Batch == 0 {
		n, err := s.store.QueueSends(p.Slug)
		if err != nil {
			return "", err
		}
		if n == 0 {
			return "no subscribers", nil
		}
	}
	cfg := s.cfg.Newsletter
	sends, err := s.store.NextSends(p.Slug, max(1, cfg.BatchSize))
	if err != nil {
		return "", err
	}
	if len(sends) == 0 {
		return "all sent", nil
	}
	ok, failed := 0, 0
	for _, ps := range sends {
		m := s.postMail(e, ps.Subscriber)
		err := s.mailer().Send(ctx, []*Mail{m})
		if err != nil && (err == errNoMail || ctx.Err() != nil) {
			return "", err
		}
		_ = s.store.MarkSent(ps.ID, err)
		if err != nil {
			failed++
		} else {
			ok++
		}
	}
	next, _ := json.Marshal(map[string]any{"slug": p.Slug, "batch": p.Batch + 1})
	_, _ = s.store.Enqueue("newsletter", fmt.Sprintf("newsletter:%s:%d", p.Slug, p.Batch+1), string(next), s.now().Add(time.Duration(cfg.BatchPauseSeconds)*time.Second).Unix())
	return fmt.Sprintf("batch %d: %d sent, %d failed", p.Batch+1, ok, failed), nil
}

var mailTmpl = template.Must(template.New("mail").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title></head>
<body style="margin:0;padding:0;background:#f6f3ec;color:#1c1b18;">
<div style="max-width:640px;margin:0 auto;padding:32px 20px;font:18px/1.6 Georgia,'Times New Roman',serif;">
<p style="font:14px/1.4 -apple-system,Segoe UI,Roboto,Arial,sans-serif;color:#5c584f;margin:0 0 24px;"><a href="{{.SiteURL}}" style="color:#5c584f;">{{.Site}}</a></p>
<h1 style="font-size:30px;line-height:1.15;margin:0 0 8px;"><a href="{{.URL}}" style="color:#1c1b18;text-decoration:none;">{{.Title}}</a></h1>
<p style="font:14px/1.4 -apple-system,Segoe UI,Roboto,Arial,sans-serif;color:#5c584f;margin:0 0 24px;">{{.Date}}</p>
<div>{{.Body}}</div>
{{if .Excerpt}}<p><a href="{{.URL}}" style="color:#2f5aa0;">Read the whole post</a></p>{{end}}
<hr style="border:0;border-top:1px solid #d8d2c4;margin:32px 0 16px;">
<p style="font:13px/1.5 -apple-system,Segoe UI,Roboto,Arial,sans-serif;color:#5c584f;">You get this because you subscribed to {{.Site}}. <a href="{{.Unsubscribe}}" style="color:#5c584f;">Unsubscribe</a> with one click.</p>
</div></body></html>`))

// postMail builds a post's newsletter email for one subscriber.
func (s *Server) postMail(e *site.Entry, sub *store.Subscriber) *Mail {
	b := s.lastBuild()
	unsub := s.cfg.AbsURL(s.cfg.Rel("/api/unsubscribe")) + "?t=" + url.QueryEscape(sub.Token)
	full := s.cfg.Newsletter.Content == "full"
	body := template.HTML("<p>" + template.HTMLEscapeString(e.Excerpt) + "</p>")
	if full {
		body = template.HTML(b.FeedHTML(e))
	}
	var h bytes.Buffer
	_ = mailTmpl.Execute(&h, map[string]any{
		"Lang": e.Lang, "Title": e.Title, "Site": s.cfg.Site.Title, "SiteURL": s.cfg.AbsURL(s.cfg.Site.BasePath),
		"URL": e.Permalink, "Date": b.Locale.Date(e.Date), "Body": body, "Excerpt": !full, "Unsubscribe": unsub,
	})
	text, err := htmltomarkdown.ConvertString(string(body))
	if err != nil {
		text = e.Excerpt
	}
	plain := fmt.Sprintf("%s\n%s\n\n%s\n\n%s\n\n--\nYou get this because you subscribed to %s.\nUnsubscribe: %s\n", e.Title, e.Permalink, b.Locale.Date(e.Date), strings.TrimSpace(text), s.cfg.Site.Title, unsub)
	return &Mail{To: sub.Email, Subject: e.Title, Text: plain, HTML: h.String(), ListUnsubscribe: unsub, MessageID: "post-" + e.Slug + "-" + strconv.FormatInt(sub.ID, 10)}
}

// ---- admin ----

func (a *adminUI) subscribers(w http.ResponseWriter, r *http.Request) {
	s := a.s
	status := r.URL.Query().Get("status")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	subs, _ := s.store.Subscribers(status, q, 500)
	reports, _ := s.store.SendReports(20)
	titles := map[string]string{}
	if b := s.lastBuild(); b != nil {
		for _, e := range b.Entries {
			titles[e.Slug] = e.Title
		}
	}
	a.render(w, r, http.StatusOK, "subscribers", &adminPage{Title: "Subscribers", Nav: "subscribers", Data: map[string]any{
		"Enabled": s.cfg.Newsletter.Enabled, "Subs": subs, "Counts": s.store.SubscriberCounts(), "Status": status, "Query": q,
		"Reports": reports, "Titles": titles, "MailReady": s.mailReady(),
	}})
}

func (a *adminUI) subscribersAction(w http.ResponseWriter, r *http.Request) {
	s := a.s
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	switch r.FormValue("action") {
	case "delete":
		_ = s.store.DeleteSubscriber(id)
		a.redirect(w, r, "subscribers", "Deleted, with everything kept about them.")
	case "unsubscribe":
		_ = s.store.SetSubscriberStatus(id, "unsubscribed")
		a.redirect(w, r, "subscribers", "Unsubscribed.")
	case "test":
		b := s.lastBuild()
		if b == nil || len(b.Posts) == 0 || s.cfg.Author.Email == "" {
			a.redirect(w, r, "subscribers", "A test needs a published post and author.email in the config.")
			return
		}
		m := s.postMail(b.Posts[0], &store.Subscriber{Email: s.cfg.Author.Email, Token: "test"})
		m.Subject = "[Test] " + m.Subject
		if err := s.mailer().Send(r.Context(), []*Mail{m}); err != nil {
			a.redirect(w, r, "subscribers", "The test email failed: "+err.Error())
			return
		}
		a.redirect(w, r, "subscribers", "Sent the newest post to "+s.cfg.Author.Email+". Check that it arrived in the inbox, not spam.")
	default:
		a.redirect(w, r, "subscribers", "")
	}
}

// subscribersCSV exports the list (for backups, or to move elsewhere).
func (a *adminUI) subscribersCSV(w http.ResponseWriter, r *http.Request) {
	s := a.s
	subs, _ := s.store.Subscribers("", "", 1_000_000)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="subscribers-`+s.now().Format("2006-01-02")+`.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"email", "status", "subscribed_at", "consent_at", "source"})
	for _, x := range subs {
		consent := ""
		if x.ConsentAt > 0 {
			consent = time.Unix(x.ConsentAt, 0).UTC().Format(time.RFC3339)
		}
		_ = cw.Write([]string{x.Email, x.Status, time.Unix(x.CreatedAt, 0).UTC().Format(time.RFC3339), consent, x.ConsentSource})
	}
	cw.Flush()
}
