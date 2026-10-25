// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html/template"
	"math/bits"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/GTKottman/skimdit/internal/markdown"
	"github.com/GTKottman/skimdit/internal/site"
	"github.com/GTKottman/skimdit/internal/store"
)

// ---- proof of work ----

// powChallenge hands out a signed challenge. The comment form must find a
// nonce whose SHA-256 (with the challenge) starts with enough zero bits:
// cheap for one reader, expensive for a spam bot sending thousands.
func (s *Server) powChallenge(w http.ResponseWriter, r *http.Request) {
	ch := NewSigner(s.secret, "pow").Sign(randomToken(12), 15*time.Minute, s.now())
	writeJSON(w, http.StatusOK, map[string]any{"challenge": ch, "bits": s.cfg.Comments.PowBits})
}

func leadingZeroBits(b []byte) int {
	n := 0
	for _, x := range b {
		if x == 0 {
			n += 8
			continue
		}
		return n + bits.LeadingZeros8(x)
	}
	return n
}

// checkPow verifies "challenge:nonce" once.
func (s *Server) checkPow(v string) bool {
	i := strings.LastIndexByte(v, ':')
	if i < 0 {
		return false
	}
	ch := v[:i]
	if _, err := NewSigner(s.secret, "pow").Verify(ch, s.now()); err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(v))
	if leadingZeroBits(sum[:]) < s.cfg.Comments.PowBits {
		return false
	}
	s.powMu.Lock()
	defer s.powMu.Unlock()
	now := s.now()
	for k, t := range s.powUsed {
		if now.Sub(t) > 20*time.Minute {
			delete(s.powUsed, k)
		}
	}
	if _, used := s.powUsed[ch]; used {
		return false
	}
	s.powUsed[ch] = now
	return true
}

// ---- notices ----

// notice answers a form post with a small page in the site's own look.
func (s *Server) notice(w http.ResponseWriter, code int, title, text, back string) {
	css := ""
	if b := s.lastBuild(); b != nil {
		css = s.cfg.Rel(b.Theme.AssetDir() + "css/main.css")
	}
	s.securityHeadersNoReq(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	page := `<!doctype html><html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex"><title>{{.Title}}</title>{{if .CSS}}<link rel="stylesheet" href="{{.CSS}}">{{end}}</head>
<body><main id="main"><div class="wrap"><h1 class="page-title">{{.Title}}</h1><p>{{.Text}}</p>{{if .Back}}<p><a href="{{.Back}}">{{.BackText}}</a></p>{{end}}</div></main></body></html>`
	t := template.Must(template.New("n").Parse(page))
	backText := "Back to the site"
	if strings.Contains(back, "#") {
		backText = "Back to the post"
	}
	_ = t.Execute(w, map[string]string{"Lang": s.cfg.Site.Language, "Title": title, "Text": text, "CSS": css, "Back": back, "BackText": backText})
}

func (s *Server) securityHeadersNoReq(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", readerCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
}

// ---- posting a comment ----

func (s *Server) entryBySlug(slug string) *site.Entry {
	b := s.lastBuild()
	if b == nil {
		return nil
	}
	for _, e := range b.Entries {
		if e.Kind == "post" && e.Slug == slug && e.Status != "draft" {
			return e
		}
	}
	return nil
}

func (s *Server) postComment(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Comments
	ip := s.clientIP(r)
	if !cfg.Enabled {
		httpError(w, http.StatusNotFound, "comments are off")
		return
	}
	if !s.postLimit.allow("comment:"+ip, s.now()) {
		s.notice(w, http.StatusTooManyRequests, "Slow down", "You sent several comments in a short time. Wait a few minutes and try again.", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		httpError(w, http.StatusBadRequest, "bad form")
		return
	}
	e := s.entryBySlug(r.PostFormValue("post"))
	back := s.cfg.Rel("/")
	if e != nil {
		back = e.URL + "#responses"
	}
	fail := func(msg string) { s.notice(w, http.StatusBadRequest, "Your comment was not sent", msg, back) }
	if e == nil || !e.Comments {
		fail("Comments are closed on this post.")
		return
	}
	// The honeypot field is invisible to people; bots fill it in.
	if r.PostFormValue("website") != "" {
		s.notice(w, http.StatusOK, "Thanks!", "Your comment will appear once it is approved.", back)
		return
	}
	if !s.checkPow(r.PostFormValue("pow")) {
		fail("The spam check did not pass. Reload the post and try again (the form needs JavaScript).")
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	body := strings.TrimSpace(strings.ReplaceAll(r.PostFormValue("body"), "\r\n", "\n"))
	email := strings.TrimSpace(r.PostFormValue("email"))
	site := strings.TrimSpace(r.PostFormValue("url"))
	switch {
	case name == "" || len([]rune(name)) > 80:
		fail("Please give a name of up to 80 characters.")
		return
	case body == "":
		fail("The comment is empty.")
		return
	case len([]rune(body)) > cfg.MaxLength:
		fail(fmt.Sprintf("The comment is longer than %d characters.", cfg.MaxLength))
		return
	case strings.Count(strings.ToLower(body), "http") > cfg.MaxLinks:
		fail(fmt.Sprintf("Comments may contain at most %d links.", cfg.MaxLinks))
		return
	}
	if email != "" {
		if a, err := mail.ParseAddress(email); err != nil || a.Address != email || len(email) > 254 {
			fail("That email address does not look right.")
			return
		}
	}
	if site != "" {
		u, err := url.Parse(site)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(site) > 300 {
			fail("The website must be a full address starting with https://.")
			return
		}
	}
	ipHash := s.ipHash(ip)
	if s.store.RecentCommentsFrom(ipHash, s.now().Add(-24*time.Hour).Unix()) >= 20 {
		fail("Too many comments from your connection today.")
		return
	}
	c := &store.Comment{Post: e.Slug, Author: name, Email: email, URL: site, Body: body, HTML: markdown.RenderComment(body), Status: "pending", IPHash: ipHash}
	if pid, err := strconv.ParseInt(r.PostFormValue("parent"), 10, 64); err == nil && pid > 0 {
		if p, err := s.store.Comment(pid); err == nil && p.Post == e.Slug && p.Status == "approved" {
			c.ParentID = pid
		}
	}
	if r.PostFormValue("notify") == "1" && email != "" {
		c.Notify = 1
		c.NotifyToken = randomToken(24)
	}
	id, err := s.store.AddComment(c)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not save the comment")
		return
	}
	payload, _ := json.Marshal(map[string]int64{"comment": id})
	if c.Notify == 1 {
		_, _ = s.store.Enqueue("comment_confirm", fmt.Sprintf("comment_confirm:%d", id), string(payload), 0)
	}
	if s.cfg.Author.Email != "" && s.mailReady() {
		_, _ = s.store.Enqueue("comment_owner", fmt.Sprintf("comment_owner:%d", id), string(payload), 0)
	}
	msg := "Your comment will appear once it is approved."
	if c.Notify == 1 {
		msg += " To get an email when someone replies, click the link we just sent you."
	}
	s.notice(w, http.StatusOK, "Thanks!", msg, back)
}

func (s *Server) mailReady() bool {
	return s.testMailer != nil || (s.cfg.Newsletter.SMTPHost != "" && s.cfg.Newsletter.From != "")
}

// confirmCommentNotify turns on reply emails after the commenter clicks
// the link (double opt-in).
func (s *Server) confirmCommentNotify(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.CommentByToken(r.URL.Query().Get("t"))
	if err != nil {
		s.notice(w, http.StatusNotFound, "Link not valid", "This link is not valid any more.", s.cfg.Rel("/"))
		return
	}
	if r.Method == http.MethodPost {
		_ = s.store.SetCommentNotify(c.ID, 2)
		s.notice(w, http.StatusOK, "Done", "You will get an email when someone replies to your comment.", s.postURL(c.Post))
		return
	}
	s.confirmForm(w, "Email me about replies?", "Click the button to get an email when someone replies to your comment.", "Yes, email me")
}

func (s *Server) unsubscribeCommentNotify(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.CommentByToken(r.URL.Query().Get("t"))
	if err == nil && (r.Method == http.MethodPost) {
		_ = s.store.SetCommentNotify(c.ID, 0)
	}
	if r.Method == http.MethodPost {
		s.notice(w, http.StatusOK, "Done", "You will not get any more emails about this comment.", s.cfg.Rel("/"))
		return
	}
	s.confirmForm(w, "Stop reply emails?", "Click the button to stop emails about replies to your comment.", "Stop the emails")
}

// confirmForm asks for a button press, so link scanners that open every
// link in an email never confirm or unsubscribe by accident.
func (s *Server) confirmForm(w http.ResponseWriter, title, text, button string) {
	css := ""
	if b := s.lastBuild(); b != nil {
		css = s.cfg.Rel(b.Theme.AssetDir() + "css/main.css")
	}
	s.securityHeadersNoReq(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	t := template.Must(template.New("c").Parse(`<!doctype html><html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="robots" content="noindex"><title>{{.Title}}</title>{{if .CSS}}<link rel="stylesheet" href="{{.CSS}}">{{end}}</head>
<body><main id="main"><div class="wrap"><h1 class="page-title">{{.Title}}</h1><p>{{.Text}}</p><form method="post"><button type="submit">{{.Button}}</button></form></div></main></body></html>`))
	_ = t.Execute(w, map[string]string{"Lang": s.cfg.Site.Language, "Title": title, "Text": text, "Button": button, "CSS": css})
}

func (s *Server) postURL(slug string) string {
	if e := s.entryBySlug(slug); e != nil {
		return e.URL + "#responses"
	}
	return s.cfg.Rel("/")
}

// ---- mail jobs ----

func (s *Server) commentFromJob(j *store.Job) (*store.Comment, error) {
	var p struct {
		Comment int64 `json:"comment"`
	}
	if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
		return nil, err
	}
	return s.store.Comment(p.Comment)
}

func (s *Server) jobCommentConfirm(ctx context.Context, j *store.Job) (string, error) {
	c, err := s.commentFromJob(j)
	if err != nil || c.Notify != 1 {
		return "nothing to confirm", nil
	}
	link := s.cfg.AbsURL(s.cfg.Rel("/api/comments/confirm")) + "?t=" + url.QueryEscape(c.NotifyToken)
	m := &Mail{To: c.Email, Subject: "Confirm reply emails from " + s.cfg.Site.Title,
		Text: fmt.Sprintf("You commented on %s and asked for an email when someone replies.\n\nConfirm here:\n%s\n\nIf this was not you, ignore this email; nothing more will be sent.\n", s.cfg.Site.Title, link)}
	return "sent", s.mailer().Send(ctx, []*Mail{m})
}

func (s *Server) jobCommentOwner(ctx context.Context, j *store.Job) (string, error) {
	c, err := s.commentFromJob(j)
	if err != nil {
		return "comment gone", nil
	}
	admin := s.cfg.AbsURL(s.cfg.Rel("/admin/comments"))
	m := &Mail{To: s.cfg.Author.Email, Subject: fmt.Sprintf("New comment from %s waiting for approval", c.Author),
		Text: fmt.Sprintf("%s wrote on %q:\n\n%s\n\nApprove or remove it: %s\n", c.Author, c.Post, c.Body, admin)}
	return "sent", s.mailer().Send(ctx, []*Mail{m})
}

func (s *Server) jobCommentReply(ctx context.Context, j *store.Job) (string, error) {
	c, err := s.commentFromJob(j)
	if err != nil || c.ParentID == 0 || c.Status != "approved" {
		return "nothing to send", nil
	}
	parent, err := s.store.Comment(c.ParentID)
	if err != nil || parent.Notify != 2 || parent.Email == "" {
		return "no one to tell", nil
	}
	stop := s.cfg.AbsURL(s.cfg.Rel("/api/comments/unsubscribe")) + "?t=" + url.QueryEscape(parent.NotifyToken)
	link := s.cfg.AbsURL(s.postURL(c.Post))
	m := &Mail{To: parent.Email, Subject: c.Author + " replied to your comment", ListUnsubscribe: stop,
		Text: fmt.Sprintf("%s replied to your comment on %s:\n\n%s\n\nRead it: %s\n\nStop these emails: %s\n", c.Author, s.cfg.Site.Title, c.Body, link, stop)}
	return "sent", s.mailer().Send(ctx, []*Mail{m})
}

// ---- responses for the build ----

// responses gathers a post's approved comments, webmentions and fediverse
// replies for its page.
func (s *Server) responses(e *site.Entry) []*site.Response {
	var out []*site.Response
	comments, _ := s.store.ApprovedComments(e.Slug)
	byID := map[int64]*site.Response{}
	for _, c := range comments {
		r := &site.Response{ID: c.ID, Kind: "comment", Author: c.Author, AuthorURL: c.URL, HTML: template.HTML(c.HTML), Date: time.Unix(c.CreatedAt, 0).In(s.cfg.Location)}
		byID[c.ID] = r
		if parent := byID[c.ParentID]; c.ParentID != 0 && parent != nil {
			// One level of nesting keeps threads readable.
			for parent.ID != 0 && byID[parentOf(comments, parent.ID)] != nil {
				parent = byID[parentOf(comments, parent.ID)]
			}
			r.Kind = "reply"
			parent.Children = append(parent.Children, r)
			continue
		}
		out = append(out, r)
	}
	mentions, _ := s.store.ApprovedWebmentions(e.Slug)
	for _, m := range mentions {
		kind := m.Type
		if kind == "reply" {
			kind = "reply"
		}
		d := time.Unix(m.CreatedAt, 0)
		if m.Published > 0 {
			d = time.Unix(m.Published, 0)
		}
		out = append(out, &site.Response{ID: -m.ID, Kind: kind, Author: firstNonEmpty(m.Author, hostOf(m.Source)), AuthorURL: m.AuthorURL, URL: m.Source, HTML: template.HTML(m.HTML), Date: d.In(s.cfg.Location)})
	}
	out = append(out, s.fediverseReplies(e)...)
	return out
}

func parentOf(cs []*store.Comment, id int64) int64 {
	for _, c := range cs {
		if c.ID == id {
			return c.ParentID
		}
	}
	return 0
}

func hostOf(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	return p.Host
}
