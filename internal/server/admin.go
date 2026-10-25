// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GTKottman/skimdit/internal/content"
	"github.com/GTKottman/skimdit/internal/store"
	"github.com/pquerna/otp/totp"
)

//go:embed admin
var adminFS embed.FS

func getenv(k string) string { return os.Getenv(k) }

type adminUI struct {
	s     *Server
	pages map[string]*template.Template
	asset string // URL prefix of admin assets
}

// adminPage is what every admin template receives.
type adminPage struct {
	Title    string
	Nav      string
	Site     string
	SiteURL  string
	Base     string
	Assets   string
	CSRF     string
	Flash    string
	Error    string
	Data     any
	LoggedIn bool
}

func newAdminUI(s *Server) (*adminUI, error) {
	a := &adminUI{s: s, pages: map[string]*template.Template{}, asset: s.cfg.Rel("/admin/assets/")}
	funcs := template.FuncMap{
		"ago":  func(unix int64) string { return ago(s.now(), unix) },
		"when": func(unix int64) string { return time.Unix(unix, 0).In(s.cfg.Location).Format("Jan 2, 2006 15:04") },
		"date": func(t time.Time) string { return t.In(s.cfg.Location).Format("Jan 2, 2006") },
		"json": func(v any) (template.JS, error) {
			b, err := json.Marshal(v)
			return template.JS(b), err
		},
		"join":  strings.Join,
		"deref": func(b *bool) bool { return b != nil && *b },
		// safe marks HTML that was sanitized before it was stored.
		"safe": func(h string) template.HTML { return template.HTML(h) },
		"add":  func(a, b int) int { return a + b },
	}
	layout, err := fs.ReadFile(adminFS, "admin/templates/layout.html")
	if err != nil {
		return nil, err
	}
	names, _ := fs.Glob(adminFS, "admin/templates/*.html")
	for _, n := range names {
		name := strings.TrimSuffix(filepath.Base(n), ".html")
		if name == "layout" {
			continue
		}
		src, _ := fs.ReadFile(adminFS, n)
		t, err := template.New("layout").Funcs(funcs).Parse(string(layout))
		if err != nil {
			return nil, err
		}
		if _, err := t.Parse(string(src)); err != nil {
			return nil, fmt.Errorf("admin template %s: %w", name, err)
		}
		a.pages[name] = t
	}
	return a, nil
}

func ago(now time.Time, unix int64) string {
	if unix == 0 {
		return "never"
	}
	d := now.Sub(time.Unix(unix, 0))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
	return time.Unix(unix, 0).Format("Jan 2, 2006")
}

func (a *adminUI) render(w http.ResponseWriter, r *http.Request, code int, name string, p *adminPage) {
	s := a.s
	p.Site = s.cfg.Site.Title
	p.SiteURL = s.cfg.Rel("/")
	p.Base = s.cfg.Rel("/admin/")
	p.Assets = a.asset
	if se, ok := r.Context().Value(sessionKey).(*store.Session); ok && se != nil {
		p.CSRF = se.CSRF
		p.LoggedIn = true
	}
	if f := r.URL.Query().Get("flash"); f != "" && p.Flash == "" {
		p.Flash = truncate(f, 200)
	}
	var buf bytes.Buffer
	if err := a.pages[name].ExecuteTemplate(&buf, "layout", p); err != nil {
		s.log.Printf("admin template %s: %v", name, err)
		httpError(w, http.StatusInternalServerError, "template error")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(buf.Bytes())
}

func (a *adminUI) redirect(w http.ResponseWriter, r *http.Request, to, flash string) {
	u := a.s.cfg.Rel("/admin/" + strings.TrimPrefix(to, "/"))
	if flash != "" {
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + "flash=" + template.URLQueryEscaper(flash)
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

// ServeHTTP routes /admin/*.
func (a *adminUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s := a.s
	p := strings.TrimPrefix(r.URL.Path, s.cfg.Rel("/admin"))
	p = "/" + strings.TrimPrefix(p, "/")

	// Public admin routes.
	switch {
	case strings.HasPrefix(p, "/assets/"):
		a.serveAsset(w, r, strings.TrimPrefix(p, "/assets/"))
		return
	case p == "/setup":
		a.setup(w, r)
		return
	case p == "/login":
		a.login(w, r)
		return
	case p == "/login/passkey/begin" && r.Method == http.MethodPost:
		s.passkeyLoginBegin(w, r)
		return
	case p == "/login/passkey/finish" && r.Method == http.MethodPost:
		s.passkeyLoginFinish(w, r)
		return
	}
	if s.setupToken != "" {
		a.render(w, r, http.StatusOK, "message", &adminPage{Title: "Finish setup", Data: map[string]string{
			"Text": "This site has no account yet. Open the setup link printed in the server log when it started.",
		}})
		return
	}
	se := s.session(r)
	if se == nil {
		if r.Method == http.MethodGet {
			http.Redirect(w, r, s.cfg.Rel("/admin/login")+"?next="+template.URLQueryEscaper(r.URL.Path), http.StatusSeeOther)
			return
		}
		httpError(w, http.StatusUnauthorized, "sign in first")
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), sessionKey, se))
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !s.checkCSRF(r, se) {
		httpError(w, http.StatusForbidden, "this form expired; reload the page and try again")
		return
	}
	switch {
	case p == "/":
		a.dashboard(w, r)
	case p == "/logout" && r.Method == http.MethodPost:
		s.endSession(w, r)
		http.Redirect(w, r, s.cfg.Rel("/admin/login"), http.StatusSeeOther)
	case p == "/new" && r.Method == http.MethodPost:
		a.newDraft(w, r)
	case p == "/edit" && r.Method == http.MethodPost:
		a.editPublished(w, r)
	case strings.HasPrefix(p, "/drafts/"):
		a.draftRoutes(w, r, strings.TrimPrefix(p, "/drafts/"))
	case p == "/unpublish" && r.Method == http.MethodPost:
		a.unpublish(w, r)
	case p == "/settings":
		a.settings(w, r)
	case strings.HasPrefix(p, "/settings/"):
		a.settingsAction(w, r, strings.TrimPrefix(p, "/settings/"), se)
	case p == "/activity":
		a.activity(w, r)
	case p == "/rebuild" && r.Method == http.MethodPost:
		if err := s.rebuild(); err != nil {
			a.redirect(w, r, "", "Build failed: see the top of the dashboard.")
			return
		}
		a.redirect(w, r, "", "Rebuilt the site.")
	default:
		if a.featureRoutes(w, r, p) {
			return
		}
		a.render(w, r, http.StatusNotFound, "message", &adminPage{Title: "Not found", Data: map[string]string{"Text": "There is no admin page here."}})
	}
}

func (a *adminUI) serveAsset(w http.ResponseWriter, r *http.Request, name string) {
	b, err := fs.ReadFile(adminFS, "admin/assets/"+filepathClean(name))
	if err != nil {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", contentType(name))
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

func filepathClean(p string) string {
	p = filepath.ToSlash(filepath.Clean("/" + p))
	return strings.TrimPrefix(p, "/")
}

// ---- setup and login ----

func (a *adminUI) setup(w http.ResponseWriter, r *http.Request) {
	s := a.s
	token := r.FormValue("token")
	page := &adminPage{Title: "Create your account", Data: map[string]string{"Token": token}}
	if s.setupToken == "" {
		a.redirect(w, r, "login", "")
		return
	}
	if r.Method == http.MethodPost {
		if !s.sameOrigin(r) {
			httpError(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		if err := s.completeSetup(token, r.FormValue("password"), r.FormValue("confirm")); err != nil {
			page.Error = err.Error()
			a.render(w, r, http.StatusBadRequest, "setup", page)
			return
		}
		if err := s.startSession(w, r); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		a.redirect(w, r, "settings", "Account created. Add a passkey or a code app now, so a password alone is never enough.")
		return
	}
	a.render(w, r, http.StatusOK, "setup", page)
}

func (a *adminUI) login(w http.ResponseWriter, r *http.Request) {
	s := a.s
	if s.setupToken != "" {
		a.render(w, r, http.StatusOK, "message", &adminPage{Title: "Finish setup", Data: map[string]string{"Text": "Open the setup link printed in the server log."}})
		return
	}
	c, _ := s.store.Credentials()
	keys, _ := s.store.Passkeys()
	data := map[string]any{"TOTP": c != nil && c.TOTPSecret != "", "Passkeys": len(keys) > 0, "Next": safeNext(r.FormValue("next"), s.cfg.Rel("/admin/"))}
	page := &adminPage{Title: "Sign in", Data: data}
	if r.Method == http.MethodPost {
		if !s.sameOrigin(r) {
			httpError(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		if err := s.passwordLogin(r, r.FormValue("password"), r.FormValue("code")); err != nil {
			page.Error = err.Error()
			a.render(w, r, http.StatusUnauthorized, "login", page)
			return
		}
		if err := s.startSession(w, r); err != nil {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		http.Redirect(w, r, data["Next"].(string), http.StatusSeeOther)
		return
	}
	if s.session(r) != nil {
		http.Redirect(w, r, data["Next"].(string), http.StatusSeeOther)
		return
	}
	a.render(w, r, http.StatusOK, "login", page)
}

// safeNext only allows redirects back into the admin.
func safeNext(next, fallback string) string {
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && strings.Contains(next, "/admin/") && !strings.ContainsAny(next, "\\\r\n") {
		return next
	}
	return fallback
}

// ---- dashboard ----

type postRow struct {
	Title, URL, Source, Status, Kind string
	Date                             time.Time
	DraftID                          int64
}

type draftRow struct {
	ID        int64
	Title     string
	Kind      string
	Editing   string
	UpdatedAt int64
	PublishAt int64
}

func (a *adminUI) dashboard(w http.ResponseWriter, r *http.Request) {
	s := a.s
	drafts, _ := s.store.Drafts()
	var drows []draftRow
	open := map[string]int64{}
	for _, d := range drafts {
		f, _ := s.draftFields(d)
		title := f.Title
		if title == "" {
			title = "Untitled"
		}
		drows = append(drows, draftRow{ID: d.ID, Title: title, Kind: d.Kind, Editing: d.Source, UpdatedAt: d.UpdatedAt, PublishAt: d.PublishAt})
		if d.Source != "" {
			open[d.Source] = d.ID
		}
	}
	var posts []postRow
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if b := s.lastBuild(); b != nil {
		for _, e := range b.Entries {
			if q != "" && !strings.Contains(strings.ToLower(e.Title), q) {
				continue
			}
			st := "Published"
			if e.Unlisted {
				st = "Unlisted"
			}
			posts = append(posts, postRow{Title: e.Title, URL: e.URL, Source: e.Source, Status: st, Kind: e.Kind, Date: e.Date, DraftID: open[e.Source]})
		}
	}
	sort.SliceStable(posts, func(i, j int) bool {
		if posts[i].Kind != posts[j].Kind {
			return posts[i].Kind == "post"
		}
		return posts[i].Date.After(posts[j].Date)
	})
	s.bmu.RLock()
	buildErr, buildAt := s.buildErr, s.buildAt
	s.bmu.RUnlock()
	a.render(w, r, http.StatusOK, "dashboard", &adminPage{Title: "Posts", Nav: "posts", Data: map[string]any{
		"Drafts": drows, "Posts": posts, "BuildError": buildErr, "BuiltAgo": ago(s.now(), buildAt.Unix()), "Query": q,
		"Git": s.git.isRepo(), "Head": s.git.Head(), "Pending": a.pendingCounts(),
	}})
}

func (a *adminUI) newDraft(w http.ResponseWriter, r *http.Request) {
	kind := r.FormValue("kind")
	if kind != "page" {
		kind = "post"
	}
	d, err := a.s.NewDraft(kind, strings.TrimSpace(r.FormValue("title")))
	if err != nil {
		a.redirect(w, r, "", "Could not create the draft: "+err.Error())
		return
	}
	a.redirect(w, r, fmt.Sprintf("drafts/%d", d.ID), "")
}

func (a *adminUI) editPublished(w http.ResponseWriter, r *http.Request) {
	d, err := a.s.EditDraft(r.FormValue("source"))
	if err != nil {
		a.redirect(w, r, "", "Could not open the post: "+err.Error())
		return
	}
	a.redirect(w, r, fmt.Sprintf("drafts/%d", d.ID), "")
}

func (a *adminUI) unpublish(w http.ResponseWriter, r *http.Request) {
	d, err := a.s.Unpublish(r.FormValue("source"))
	if err != nil && d == nil {
		a.redirect(w, r, "", "Could not unpublish: "+err.Error())
		return
	}
	msg := "Unpublished. Its address now answers \"gone\", and the text is back in your drafts."
	if err != nil {
		msg = err.Error()
	}
	a.redirect(w, r, fmt.Sprintf("drafts/%d", d.ID), msg)
}

// ---- drafts ----

func (a *adminUI) draftRoutes(w http.ResponseWriter, r *http.Request, rest string) {
	s := a.s
	idStr, action, _ := strings.Cut(rest, "/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		httpError(w, http.StatusNotFound, "no such draft")
		return
	}
	d, err := s.store.Draft(id)
	if err != nil {
		a.render(w, r, http.StatusNotFound, "message", &adminPage{Title: "Draft not found", Data: map[string]string{"Text": "This draft was published or deleted."}})
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		a.editor(w, r, d)
	case action == "save" && r.Method == http.MethodPost:
		a.save(w, r, d)
	case action == "preview":
		a.preview(w, r, d)
	case action == "publish" && r.Method == http.MethodPost:
		a.publish(w, r, d)
	case action == "schedule" && r.Method == http.MethodPost:
		a.schedule(w, r, d)
	case action == "delete" && r.Method == http.MethodPost:
		_ = os.RemoveAll(s.uploadDir(d.ID))
		_ = s.store.DeleteDraft(d.ID)
		a.redirect(w, r, "", "Draft deleted.")
	case action == "upload" && r.Method == http.MethodPost:
		a.upload(w, r, d)
	case strings.HasPrefix(action, "files/"):
		a.draftFile(w, r, d, strings.TrimPrefix(action, "files/"))
	case action == "revisions":
		revs, _ := s.store.Revisions(d.ID)
		type rv struct {
			ID      int64
			At      int64
			Words   int
			Preview string
		}
		var out []rv
		for _, x := range revs {
			out = append(out, rv{ID: x.ID, At: x.CreatedAt, Words: len(strings.Fields(x.Body)), Preview: truncate(strings.Join(strings.Fields(x.Body), " "), 140)})
		}
		writeJSON(w, http.StatusOK, out)
	case action == "restore" && r.Method == http.MethodPost:
		rid, _ := strconv.ParseInt(r.FormValue("revision"), 10, 64)
		rev, err := s.store.Revision(rid)
		if err != nil || rev.DraftID != d.ID {
			a.redirect(w, r, fmt.Sprintf("drafts/%d", d.ID), "That version does not exist.")
			return
		}
		_ = s.store.SaveDraft(d.ID, d.Fields, d.Body, true) // keep the current text as a version too
		_ = s.store.SaveDraft(d.ID, rev.Fields, rev.Body, true)
		a.redirect(w, r, fmt.Sprintf("drafts/%d", d.ID), "Restored the version from "+ago(s.now(), rev.CreatedAt)+".")
	default:
		httpError(w, http.StatusNotFound, "not found")
	}
}

func (a *adminUI) editor(w http.ResponseWriter, r *http.Request, d *store.Draft) {
	s := a.s
	f, _ := s.draftFields(d)
	files := []string{}
	if entries, err := os.ReadDir(s.uploadDir(d.ID)); err == nil {
		for _, e := range entries {
			files = append(files, e.Name())
		}
	}
	// Files already published next to the post can be referenced too.
	if d.Source != "" {
		if entries, err := os.ReadDir(filepath.Dir(filepath.Join(s.root, filepath.FromSlash(d.Source)))); err == nil && strings.HasSuffix(d.Source, "/index.md") {
			for _, e := range entries {
				if !e.IsDir() && e.Name() != "index.md" {
					files = append(files, e.Name())
				}
			}
		}
	}
	scheduled := ""
	if d.PublishAt > 0 {
		scheduled = time.Unix(d.PublishAt, 0).In(s.cfg.Location).Format("2006-01-02T15:04")
	}
	a.render(w, r, http.StatusOK, "editor", &adminPage{Title: "Edit: " + f.Title, Nav: "posts", Data: map[string]any{
		"Draft": d, "Fields": f, "Tags": strings.Join(f.Tags, ", "), "Aliases": strings.Join(f.Aliases, "\n"),
		"Files": files, "Scheduled": scheduled, "Timezone": s.cfg.Site.Timezone, "IsPage": d.Kind == "page",
		"Editing": d.Source,
	}})
}

// formFields reads the editor form into Fields.
func formFields(r *http.Request) Fields {
	split := func(s, sep string) []string {
		var out []string
		for _, p := range strings.Split(s, sep) {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	f := Fields{
		Title:       strings.TrimSpace(r.FormValue("title")),
		Slug:        strings.TrimSpace(r.FormValue("slug")),
		Date:        strings.TrimSpace(r.FormValue("date")),
		Updated:     strings.TrimSpace(r.FormValue("updated")),
		Tags:        split(r.FormValue("tags"), ","),
		Series:      strings.TrimSpace(r.FormValue("series")),
		Excerpt:     strings.TrimSpace(r.FormValue("excerpt")),
		Description: strings.TrimSpace(r.FormValue("description")),
		Cover:       strings.TrimSpace(r.FormValue("cover")),
		CoverAlt:    strings.TrimSpace(r.FormValue("cover_alt")),
		SocialImage: strings.TrimSpace(r.FormValue("social_image")),
		Aliases:     split(r.FormValue("aliases"), "\n"),
		Canonical:   strings.TrimSpace(r.FormValue("canonical")),
	}
	f.SeriesOrder, _ = strconv.Atoi(r.FormValue("series_order"))
	if r.FormValue("unlisted") == "1" {
		f.Status = "unlisted"
	}
	if v := r.FormValue("comments"); v == "off" {
		no := false
		f.Comments = &no
	}
	if v := r.FormValue("toc"); v == "on" || v == "off" {
		b := v == "on"
		f.TOC = &b
	}
	// The editor turns datetime-local values into dates with the site zone.
	for _, p := range []*string{&f.Date, &f.Updated} {
		if len(*p) == len("2006-01-02T15:04") {
			*p += ":00"
		}
	}
	return f
}

func (a *adminUI) save(w http.ResponseWriter, r *http.Request, d *store.Draft) {
	s := a.s
	f := formFields(r)
	old, _ := s.draftFields(d)
	f.Extra, f.Lang = old.Extra, old.Lang
	if f.Slug == "" && d.Source == "" {
		f.Slug = content.Slugify(f.Title)
	}
	b, _ := json.Marshal(f)
	if err := s.store.SaveDraft(d.ID, string(b), r.FormValue("body"), r.FormValue("force") == "1"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Report problems while writing, without blocking the save.
	text, _ := FileText(f, r.FormValue("body"))
	var problems []string
	if _, diags := content.Parse([]byte(text), "draft", content.Kind(d.Kind), firstNonEmpty(f.Slug, "draft"), s.cfg.Location); len(diags) > 0 {
		for _, dg := range diags {
			problems = append(problems, dg.Message)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": s.now().In(s.cfg.Location).Format("15:04:05"), "problems": problems})
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (a *adminUI) preview(w http.ResponseWriter, r *http.Request, d *store.Draft) {
	s := a.s
	b := s.lastBuild()
	if b == nil {
		httpError(w, http.StatusServiceUnavailable, "the site has not built yet")
		return
	}
	f, _ := s.draftFields(d)
	if f.Slug == "" {
		f.Slug = firstNonEmpty(content.Slugify(f.Title), "draft")
	}
	text, _ := FileText(f, d.Body)
	srcDir := s.uploadDir(d.ID)
	html, err := b.Preview([]byte(text), content.Kind(d.Kind), f.Slug, d.Source, srcDir, s.cfg.Rel(fmt.Sprintf("/admin/drafts/%d/files/", d.ID)))
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><link rel="stylesheet" href="%s"><div class="preview-error"><h1>This draft does not build yet</h1><pre>%s</pre></div>`,
			a.asset+"admin.css", template.HTMLEscapeString(err.Error()))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(html)
}

func (a *adminUI) publish(w http.ResponseWriter, r *http.Request, d *store.Draft) {
	s := a.s
	if d.PublishAt > 0 {
		_ = s.store.ScheduleDraft(d.ID, 0)
		d.PublishAt = 0
	}
	res, err := s.publishDraft(d)
	if err != nil && res == nil {
		a.render(w, r, http.StatusUnprocessableEntity, "message", &adminPage{Title: "Not published", Nav: "posts", Error: err.Error(), Data: map[string]string{
			"Text": "Nothing changed on the site. Fix the problem in the draft and publish again.", "Back": s.cfg.Rel(fmt.Sprintf("/admin/drafts/%d", d.ID)),
		}})
		return
	}
	msg := "Published: " + res.URL
	if err != nil {
		msg = err.Error()
	}
	a.redirect(w, r, "", msg)
}

func (a *adminUI) schedule(w http.ResponseWriter, r *http.Request, d *store.Draft) {
	s := a.s
	v := strings.TrimSpace(r.FormValue("publish_at"))
	if v == "" {
		_ = s.store.ScheduleDraft(d.ID, 0)
		a.redirect(w, r, fmt.Sprintf("drafts/%d", d.ID), "No longer scheduled.")
		return
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", v, s.cfg.Location)
	if err != nil {
		a.redirect(w, r, fmt.Sprintf("drafts/%d", d.ID), "That date is not valid.")
		return
	}
	if !t.After(s.now()) {
		a.redirect(w, r, fmt.Sprintf("drafts/%d", d.ID), "Pick a time in the future, or publish now.")
		return
	}
	if d.Source != "" {
		a.redirect(w, r, fmt.Sprintf("drafts/%d", d.ID), "Edits to published posts go out when you publish them; scheduling is for new posts.")
		return
	}
	_ = s.store.ScheduleDraft(d.ID, t.Unix())
	a.redirect(w, r, fmt.Sprintf("drafts/%d", d.ID), "Scheduled for "+t.Format("Jan 2, 2006 at 15:04 (MST)")+". It stays private until then.")
}

func (a *adminUI) upload(w http.ResponseWriter, r *http.Request, d *store.Draft) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxUpload+1<<20)
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no file, or the file is over 25 MB"})
		return
	}
	defer file.Close()
	name, err := a.s.saveUpload(d.ID, hdr.Filename, file)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

func (a *adminUI) draftFile(w http.ResponseWriter, r *http.Request, d *store.Draft, name string) {
	s := a.s
	name = filepath.Base(name)
	file := filepath.Join(s.uploadDir(d.ID), name)
	if _, err := os.Stat(file); err != nil && d.Source != "" {
		file = filepath.Join(filepath.Dir(filepath.Join(s.root, filepath.FromSlash(d.Source))), name)
	}
	if _, ok := uploadTypes[strings.ToLower(filepath.Ext(name))]; !ok {
		httpError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	http.ServeFile(w, r, file)
}

// ---- settings ----

func (a *adminUI) settings(w http.ResponseWriter, r *http.Request) {
	s := a.s
	c, _ := s.store.Credentials()
	keys, _ := s.store.Passkeys()
	sessions, _ := s.store.Sessions()
	data := map[string]any{"TOTP": c != nil && c.TOTPSecret != "", "Passkeys": keys, "Sessions": sessions}
	if r.URL.Query().Get("totp") == "new" && c != nil && c.TOTPSecret == "" {
		key, img, err := s.newTOTP()
		if err == nil {
			data["NewTOTP"] = map[string]string{"Secret": key.Secret(), "QR": img, "Signed": NewSigner(s.secret, "totp").Sign(key.Secret(), 15*time.Minute, s.now())}
		}
	}
	a.render(w, r, http.StatusOK, "settings", &adminPage{Title: "Settings", Nav: "settings", Data: data})
}

func (a *adminUI) settingsAction(w http.ResponseWriter, r *http.Request, action string, se *store.Session) {
	s := a.s
	switch action {
	case "password":
		if r.Method != http.MethodPost {
			break
		}
		if err := s.passwordLogin(r, r.FormValue("current"), r.FormValue("code")); err != nil {
			a.redirect(w, r, "settings", "Password not changed: "+err.Error())
			return
		}
		if r.FormValue("password") != r.FormValue("confirm") {
			a.redirect(w, r, "settings", "Password not changed: the two new passwords differ.")
			return
		}
		hash, err := HashPassword(r.FormValue("password"))
		if err != nil {
			a.redirect(w, r, "settings", "Password not changed: "+err.Error())
			return
		}
		_ = s.store.SetPassword(hash)
		_ = s.store.DeleteSessions()
		_ = s.startSession(w, r)
		a.redirect(w, r, "settings", "Password changed. Every other browser was signed out.")
		return
	case "totp/enable":
		secret, err := NewSigner(s.secret, "totp").Verify(r.FormValue("signed"), s.now())
		if err != nil || !totp.Validate(strings.ReplaceAll(r.FormValue("code"), " ", ""), secret) {
			a.redirect(w, r, "settings?totp=new", "That code did not match. Try the current code from your app.")
			return
		}
		_ = s.store.SetTOTP(secret)
		a.redirect(w, r, "settings", "Code app turned on. You will need a code with your password from now on.")
		return
	case "totp/disable":
		if err := s.passwordLogin(r, r.FormValue("current"), r.FormValue("code")); err != nil {
			a.redirect(w, r, "settings", "Not turned off: "+err.Error())
			return
		}
		_ = s.store.SetTOTP("")
		a.redirect(w, r, "settings", "Code app turned off.")
		return
	case "passkeys/begin":
		s.passkeyBegin(w, r, se)
		return
	case "passkeys/finish":
		s.passkeyFinish(w, r, se)
		return
	case "passkeys/delete":
		keys, _ := s.store.Passkeys()
		for _, k := range keys {
			if fmt.Sprintf("%x", k.ID) == r.FormValue("id") {
				_ = s.store.DeletePasskey(k.ID)
			}
		}
		a.redirect(w, r, "settings", "Passkey removed.")
		return
	case "sessions/revoke":
		_ = s.store.DeleteSessions()
		_ = s.startSession(w, r)
		a.redirect(w, r, "settings", "Signed out everywhere else.")
		return
	}
	httpError(w, http.StatusNotFound, "not found")
}

// ---- activity ----

func (a *adminUI) activity(w http.ResponseWriter, r *http.Request) {
	s := a.s
	if r.Method == http.MethodPost {
		id, _ := strconv.ParseInt(r.FormValue("retry"), 10, 64)
		_ = s.store.RequeueFailed(id)
		a.redirect(w, r, "activity", "Queued again.")
		return
	}
	jobs, _ := s.store.RecentJobs(100)
	a.render(w, r, http.StatusOK, "activity", &adminPage{Title: "Activity", Nav: "activity", Data: map[string]any{"Jobs": jobs}})
}

var errNoBuild = errors.New("no build yet")
