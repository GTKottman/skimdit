// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const testSecret = "test-secret-test-secret-test-secret-0123456789"

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type harness struct {
	t     *testing.T
	root  string
	srv   *Server
	http  *httptest.Server
	cl    *http.Client
	clock *clock
	csrf  string
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	write("config.toml", "[site]\ntitle = \"Test\"\nurl = \"https://blog.example\"\ntimezone = \"UTC\"\n[author]\nname = \"Ada\"\n[comments]\nenabled = false\n[server]\ndata = \".data\"\n")
	write(".gitignore", "/.data/\n/dist/\n/.skimdit/\n")
	write("content/posts/existing/index.md", "---\ntitle: Existing post\ndate: 2026-01-01\n---\nAlready here.\n")
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "init")

	c := &clock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	srv, err := New(Options{Root: root, Secret: testSecret, Now: c.now, NoWorkers: true, Log: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &harness{t: t, root: root, srv: srv, http: hs, cl: cl, clock: c}
}

func (h *harness) req(method, path string, form url.Values, hdr map[string]string) (*http.Response, string) {
	h.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r, _ := http.NewRequest(method, h.http.URL+path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	r.Header.Set("Origin", h.http.URL)
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	resp, err := h.cl.Do(r)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (h *harness) form(path string, kv ...string) (*http.Response, string) {
	v := url.Values{"csrf": {h.csrf}}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return h.req("POST", path, v, nil)
}

var csrfRe = regexp.MustCompile(`data-csrf="([^"]+)"`)

func (h *harness) setup() {
	h.t.Helper()
	u := h.srv.SetupURL()
	if u == "" {
		h.t.Fatal("no setup URL")
	}
	token := strings.SplitN(u, "token=", 2)[1]
	resp, _ := h.req("POST", "/admin/setup", url.Values{"token": {token}, "password": {"correct horse battery"}, "confirm": {"correct horse battery"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		h.t.Fatalf("setup: %d", resp.StatusCode)
	}
	if h.srv.SetupURL() != "" {
		h.t.Fatal("setup token still valid after setup")
	}
	h.refreshCSRF()
}

func (h *harness) refreshCSRF() {
	_, body := h.req("GET", "/admin/", nil, nil)
	m := csrfRe.FindStringSubmatch(body)
	if m == nil {
		h.t.Fatalf("no csrf token in dashboard:\n%s", body)
	}
	h.csrf = m[1]
}

func (h *harness) newDraft(title, text string) string {
	h.t.Helper()
	resp, _ := h.form("/admin/new", "kind", "post")
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/admin/drafts/") {
		h.t.Fatalf("new draft: %d %s", resp.StatusCode, loc)
	}
	resp, body := h.req("POST", loc+"/save", url.Values{"title": {title}, "body": {text}, "date": {"2026-10-25T09:00"}}, map[string]string{"X-CSRF-Token": h.csrf})
	if resp.StatusCode != 200 || !strings.Contains(body, `"saved"`) {
		h.t.Fatalf("save: %d %s", resp.StatusCode, body)
	}
	return loc
}

func TestSetupLoginAndCSRF(t *testing.T) {
	h := newHarness(t)
	// Before setup, the admin only says how to finish setup.
	if _, body := h.req("GET", "/admin/", nil, nil); !strings.Contains(body, "setup link") {
		t.Fatal("admin reachable before setup")
	}
	// A wrong token does nothing.
	if resp, _ := h.req("POST", "/admin/setup", url.Values{"token": {"nope"}, "password": {"correct horse battery"}, "confirm": {"correct horse battery"}}, nil); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad token: %d", resp.StatusCode)
	}
	h.setup()
	// No CSRF token: refused.
	if resp, _ := h.req("POST", "/admin/new", url.Values{"kind": {"post"}}, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing csrf: %d", resp.StatusCode)
	}
	// Cross-site origin: refused even with the token.
	if resp, _ := h.req("POST", "/admin/new", url.Values{"kind": {"post"}, "csrf": {h.csrf}}, map[string]string{"Origin": "https://evil.example"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site: %d", resp.StatusCode)
	}
	// Sign out, then sign in with the password.
	h.form("/admin/logout")
	if resp, _ := h.req("GET", "/admin/", nil, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("still signed in after logout: %d", resp.StatusCode)
	}
	if resp, _ := h.req("POST", "/admin/login", url.Values{"password": {"correct horse battery"}}, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	if resp, body := h.req("GET", "/admin/", nil, nil); resp.StatusCode != 200 || !strings.Contains(body, "Existing post") {
		t.Fatalf("dashboard after login: %d", resp.StatusCode)
	}
	if resp, _ := h.req("GET", "/admin/", nil, nil); resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("admin security headers missing")
	}
}

func TestLockout(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.form("/admin/logout")
	for i := 0; i < ipFailLimit; i++ {
		h.req("POST", "/admin/login", url.Values{"password": {"wrong password!!"}}, nil)
	}
	resp, body := h.req("POST", "/admin/login", url.Values{"password": {"correct horse battery"}}, nil)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "Too many failed attempts") {
		t.Fatalf("not locked out: %d", resp.StatusCode)
	}
	h.clock.add(16 * time.Minute)
	if resp, _ := h.req("POST", "/admin/login", url.Values{"password": {"correct horse battery"}}, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("lockout did not expire: %d", resp.StatusCode)
	}
}

func jpegWithGPS(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(1, 1, color.Black)
	var b bytes.Buffer
	jpeg.Encode(&b, img, nil)
	payload := append([]byte("Exif\x00\x00MM\x00\x2a\x00\x00\x00\x08\x00\x00"), []byte("GPSSECRET")...)
	seg := append([]byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)}, payload...)
	return append(append([]byte{0xFF, 0xD8}, seg...), b.Bytes()[2:]...)
}

func (h *harness) upload(draft, name string, data []byte) map[string]string {
	h.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	fw.Write(data)
	mw.Close()
	r, _ := http.NewRequest("POST", h.http.URL+draft+"/upload", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("X-CSRF-Token", h.csrf)
	r.Header.Set("Origin", h.http.URL)
	resp, err := h.cl.Do(r)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]string
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func TestWritePublishAndServe(t *testing.T) {
	h := newHarness(t)
	h.setup()
	draft := h.newDraft("Hello from the editor", "First words.\n")

	// Upload: EXIF is stripped; a scripted SVG is refused.
	up := h.upload(draft, "Photo.JPG", jpegWithGPS(t))
	if up["name"] == "" {
		t.Fatalf("upload: %v", up)
	}
	id := strings.TrimPrefix(draft, "/admin/drafts/")
	stored, _ := os.ReadFile(filepath.Join(h.root, ".data", "uploads", id, up["name"]))
	if bytes.Contains(stored, []byte("GPSSECRET")) {
		t.Error("uploaded JPEG still carries EXIF")
	}
	if bad := h.upload(draft, "x.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)); bad["error"] == "" {
		t.Error("scripted SVG accepted")
	}
	if bad := h.upload(draft, "x.png", []byte("not a png")); bad["error"] == "" {
		t.Error("fake PNG accepted")
	}
	// Save the text with the image and check the preview.
	text := "First words.\n\n![A test photo](" + up["name"] + ")\n"
	h.req("POST", draft+"/save", url.Values{"title": {"Hello from the editor"}, "body": {text}, "date": {"2026-10-25T09:00"}, "tags": {"news, test"}}, map[string]string{"X-CSRF-Token": h.csrf})
	if resp, body := h.req("GET", draft+"/preview", nil, nil); resp.StatusCode != 200 || !strings.Contains(body, "Hello from the editor") || !strings.Contains(body, "/files/"+up["name"]) {
		t.Fatalf("preview: %d", resp.StatusCode)
	}

	// Publish: file written, committed, served.
	resp, _ := h.form(draft + "/publish")
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "Published") {
		t.Fatalf("publish: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	file := filepath.Join(h.root, "content/posts/hello-from-the-editor/index.md")
	b, err := os.ReadFile(file)
	if err != nil || !strings.Contains(string(b), "title: Hello from the editor") || !strings.Contains(string(b), "tags: [news, test]") {
		t.Fatalf("published file: %v\n%s", err, b)
	}
	if _, err := os.Stat(filepath.Join(h.root, "content/posts/hello-from-the-editor", up["name"])); err != nil {
		t.Error("uploaded image not moved into the post folder")
	}
	if log := git(t, h.root, "log", "--oneline"); !strings.Contains(log, "Publish: Hello from the editor") {
		t.Fatalf("not committed:\n%s", log)
	}
	if status := git(t, h.root, "status", "--porcelain"); strings.TrimSpace(status) != "" {
		t.Errorf("uncommitted changes after publish:\n%s", status)
	}
	page, body := h.req("GET", "/posts/hello-from-the-editor/", nil, map[string]string{"Accept-Encoding": "br"})
	if page.StatusCode != 200 || page.Header.Get("Content-Encoding") != "br" || !strings.Contains(page.Header.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatalf("served page: %d %v", page.StatusCode, page.Header)
	}
	_ = body
	etag := page.Header.Get("ETag")
	if r2, _ := h.req("GET", "/posts/hello-from-the-editor/", nil, map[string]string{"If-None-Match": etag}); r2.StatusCode != http.StatusNotModified {
		t.Errorf("etag: %d", r2.StatusCode)
	}
	if r3, _ := h.req("GET", "/posts/hello-from-the-editor", nil, nil); r3.StatusCode != http.StatusMovedPermanently {
		t.Errorf("missing slash: %d", r3.StatusCode)
	}
	if r4, b4 := h.req("GET", "/no/such/page/", nil, nil); r4.StatusCode != 404 || !strings.Contains(b4, "Page not found") {
		t.Errorf("404: %d", r4.StatusCode)
	}

	// A broken draft never reaches the site or git.
	bad := h.newDraft("Broken one", "Links to [nothing](post:does-not-exist).\n")
	commits := git(t, h.root, "rev-list", "--count", "HEAD")
	if resp, body := h.form(bad + "/publish"); resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(body, "does-not-exist") {
		t.Fatalf("broken publish: %d", resp.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(h.root, "content/posts/broken-one")); err == nil {
		t.Error("broken post left on disk")
	}
	if git(t, h.root, "rev-list", "--count", "HEAD") != commits {
		t.Error("broken post was committed")
	}
	if resp, _ := h.req("GET", "/posts/hello-from-the-editor/", nil, nil); resp.StatusCode != 200 {
		t.Error("site broken after a failed publish")
	}

	// Unpublish: the address answers 410, the text returns as a draft.
	resp, _ = h.form("/admin/unpublish", "source", "content/posts/hello-from-the-editor/index.md")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("unpublish: %d", resp.StatusCode)
	}
	if r, _ := h.req("GET", "/posts/hello-from-the-editor/", nil, nil); r.StatusCode != http.StatusGone {
		t.Errorf("unpublished URL: %d", r.StatusCode)
	}
	drafts, _ := h.srv.store.Drafts()
	found := false
	for _, d := range drafts {
		if strings.Contains(d.Body, "First words.") && d.Source == "" {
			found = true
		}
	}
	if !found {
		t.Error("unpublished text did not come back as a draft")
	}
	if log := git(t, h.root, "log", "--oneline"); !strings.Contains(log, "Unpublish: Hello from the editor") {
		t.Error("unpublish not committed")
	}
}

func TestScheduledPublish(t *testing.T) {
	h := newHarness(t)
	h.setup()
	draft := h.newDraft("Future post", "Not yet.\n")
	at := h.clock.now().Add(2 * time.Hour).Format("2006-01-02T15:04")
	if resp, _ := h.form(draft+"/schedule", "publish_at", at); !strings.Contains(resp.Header.Get("Location"), "Scheduled") {
		t.Fatalf("schedule: %s", resp.Header.Get("Location"))
	}
	ctx := context.Background()
	h.srv.enqueueDueDrafts()
	h.srv.RunJobs(ctx)
	if _, err := os.Stat(filepath.Join(h.root, "content/posts/future-post/index.md")); err == nil {
		t.Fatal("published before its time")
	}
	h.clock.add(2*time.Hour + time.Minute)
	h.srv.enqueueDueDrafts()
	h.srv.RunJobs(ctx)
	b, err := os.ReadFile(filepath.Join(h.root, "content/posts/future-post/index.md"))
	if err != nil {
		t.Fatal("not published at its time")
	}
	if !strings.Contains(string(b), "date: \"2026-10-25T14:00:00Z\"") && !strings.Contains(string(b), "2026-10-25T14:00:00") {
		t.Errorf("scheduled post date not set to its publish time:\n%s", b)
	}
	if resp, _ := h.req("GET", "/posts/future-post/", nil, nil); resp.StatusCode != 200 {
		t.Errorf("scheduled post not served: %d", resp.StatusCode)
	}
}

func TestPasswordHashing(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Error("short password accepted")
	}
	h, err := HashPassword("a long enough password")
	if err != nil || !CheckPassword("a long enough password", h) || CheckPassword("a long enough passworD", h) {
		t.Fatalf("hash/check: %v", err)
	}
}

func TestSigner(t *testing.T) {
	s := NewSigner(testSecret, "x")
	now := time.Now()
	tok := s.Sign("hello", time.Minute, now)
	if v, err := s.Verify(tok, now); err != nil || v != "hello" {
		t.Fatal(v, err)
	}
	if _, err := s.Verify(tok, now.Add(2*time.Minute)); err == nil {
		t.Error("expired token accepted")
	}
	if _, err := NewSigner(testSecret, "y").Verify(tok, now); err == nil {
		t.Error("token from another purpose accepted")
	}
}

func TestSVGSanitizer(t *testing.T) {
	good := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><defs><linearGradient id="g"><stop offset="0"/></linearGradient></defs><rect width="10" height="10" fill="url(#g)"/></svg>`
	if _, err := sanitizeSVG([]byte(good)); err != nil {
		t.Errorf("good SVG refused: %v", err)
	}
	for _, bad := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg" onload="x()"/>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><foreignObject/></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><a href="javascript:x()"><rect/></a></svg>`,
		`<svg xmlns="http://www.w3.org/2000/svg"><image href="https://evil/x.png"/></svg>`,
		`<!DOCTYPE svg [<!ENTITY x "y">]><svg xmlns="http://www.w3.org/2000/svg"/>`,
		`<html/>`,
	} {
		if _, err := sanitizeSVG([]byte(bad)); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}
}
