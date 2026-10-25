// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/GTKottman/skimdit/internal/content"
	"github.com/GTKottman/skimdit/internal/site"
	"github.com/GTKottman/skimdit/internal/store"
	"github.com/gen2brain/webp"
)

// Fields and FileText live in the content package.
type Fields = content.Fields

// FileText renders frontmatter and body as a content file.
func FileText(f Fields, body string) (string, error) { return content.FileText(f, body) }

func (s *Server) draftFields(d *store.Draft) (Fields, error) {
	var f Fields
	err := json.Unmarshal([]byte(d.Fields), &f)
	return f, err
}

// uploadDir is where a draft's uploaded files wait until publishing.
func (s *Server) uploadDir(id int64) string {
	return filepath.Join(s.data, "uploads", fmt.Sprint(id))
}

// NewDraft starts a draft for a new post or page.
func (s *Server) NewDraft(kind, title string) (*store.Draft, error) {
	f := Fields{Title: title}
	if kind == "post" {
		f.Date = s.now().In(s.cfg.Location).Format("2006-01-02T15:04:05-07:00")
	}
	b, _ := json.Marshal(f)
	return s.store.CreateDraft(kind, "", string(b), "")
}

// EditDraft returns the draft that edits a published file, creating it
// from the file (and its co-located files) when there is none yet.
func (s *Server) EditDraft(source string) (*store.Draft, error) {
	if d, err := s.store.DraftForSource(source); err == nil {
		return d, nil
	}
	file := filepath.Join(s.root, filepath.FromSlash(source))
	if !strings.HasPrefix(filepath.Clean(file), filepath.Join(s.root, s.cfg.Build.Content)+string(filepath.Separator)) {
		return nil, errors.New("not a content file")
	}
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	kind := content.KindPost
	if strings.Contains(source, "/pages/") {
		kind = content.KindPage
	}
	it, diags := content.Parse(src, source, kind, slugFromSource(source), s.cfg.Location)
	if diags.HasErrors() {
		return nil, diags
	}
	b, _ := json.Marshal(content.FieldsFromItem(it, s.cfg.Location))
	return s.store.CreateDraft(string(kind), source, string(b), string(it.Body))
}

func slugFromSource(source string) string {
	dir, name := path.Split(source)
	if name == "index.md" || name == "index.html" {
		return path.Base(strings.TrimSuffix(dir, "/"))
	}
	return strings.TrimSuffix(name, path.Ext(name))
}

// PublishResult says where a post went live.
type PublishResult struct {
	URL    string
	Commit string
	Source string
}

var errBuildFailed = errors.New("the site does not build with this change, so nothing was published")

// publishDraft writes a draft into the site, builds it and commits it.
// When the build fails, every file is put back as it was.
func (s *Server) publishDraft(d *store.Draft) (*PublishResult, error) {
	f, err := s.draftFields(d)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(f.Title) == "" {
		return nil, errors.New("give the post a title first")
	}
	if f.Slug == "" {
		f.Slug = content.Slugify(f.Title)
	}
	if !content.ValidSlug(f.Slug) {
		return nil, fmt.Errorf("slug %q must be lowercase letters, digits and dashes", f.Slug)
	}
	if f.Status == "draft" || f.Status == "scheduled" {
		f.Status = ""
	}
	if d.Kind == "post" && f.Date == "" {
		f.Date = s.now().In(s.cfg.Location).Format("2006-01-02T15:04:05-07:00")
	}
	if d.PublishAt > 0 && d.Kind == "post" {
		// A scheduled post goes out dated at its publish time.
		f.Date = time.Unix(d.PublishAt, 0).In(s.cfg.Location).Format("2006-01-02T15:04:05-07:00")
	}
	text, err := FileText(f, d.Body)
	if err != nil {
		return nil, err
	}
	source := d.Source
	if source == "" {
		source = path.Join(filepath.ToSlash(s.cfg.Build.Content), d.Kind+"s", f.Slug, "index.md")
		if _, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(source))); err == nil {
			return nil, fmt.Errorf("a post already lives at %s; pick another slug", source)
		}
	}
	// Validate before touching the disk.
	if _, diags := content.Parse([]byte(text), source, content.Kind(d.Kind), f.Slug, s.cfg.Location); diags.HasErrors() {
		return nil, diags
	}
	if err := s.git.Pull(); err != nil {
		return nil, fmt.Errorf("could not update from git before publishing: %w", err)
	}

	file := filepath.Join(s.root, filepath.FromSlash(source))
	dir := filepath.Dir(file)
	undo := newUndo()
	if err := undo.writeFile(file, []byte(text)); err != nil {
		return nil, err
	}
	// Uploaded files join the post folder.
	if entries, err := os.ReadDir(s.uploadDir(d.ID)); err == nil {
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(s.uploadDir(d.ID), e.Name()))
			if err != nil {
				undo.revert()
				return nil, err
			}
			if err := undo.writeFile(filepath.Join(dir, e.Name()), b); err != nil {
				undo.revert()
				return nil, err
			}
		}
	}
	for _, f := range []string{site.LedgerFile, site.RedirectsFile} {
		undo.remember(filepath.Join(s.root, f))
	}
	if err := s.rebuild(); err != nil {
		undo.revert()
		_ = s.rebuild()
		return nil, fmt.Errorf("%w:\n%s", errBuildFailed, strings.TrimPrefix(err.Error(), "build: "))
	}
	paths := append(undo.paths(), filepath.Join(s.root, site.LedgerFile), filepath.Join(s.root, site.RedirectsFile))
	var rels []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			rels = append(rels, s.git.rel(p))
		}
	}
	verb := "Publish"
	if d.Source != "" {
		verb = "Update"
	}
	sha, gitErr := s.git.Commit(rels, fmt.Sprintf("%s: %s", verb, f.Title))
	if gitErr != nil && !errors.Is(gitErr, ErrNotRepo) {
		s.log.Printf("publish: commit: %v", gitErr)
	}
	_ = os.RemoveAll(s.uploadDir(d.ID))
	if err := s.store.DeleteDraft(d.ID); err != nil {
		return nil, err
	}
	res := &PublishResult{Source: source, Commit: sha}
	if b := s.lastBuild(); b != nil {
		for _, e := range b.Entries {
			if e.Source == source {
				res.URL = e.URL
			}
		}
	}
	if gitErr != nil && !errors.Is(gitErr, ErrNotRepo) {
		return res, fmt.Errorf("published, but saving to git failed: %w", gitErr)
	}
	return res, nil
}

// Unpublish takes a post off the site: its URL answers 410 Gone, the files
// leave the repository, and the text comes back as a draft so nothing is lost.
func (s *Server) Unpublish(source string) (*store.Draft, error) {
	d, err := s.EditDraft(source)
	if err != nil {
		return nil, err
	}
	// Keep the co-located files with the draft.
	file := filepath.Join(s.root, filepath.FromSlash(source))
	dir := filepath.Dir(file)
	if filepath.Base(file) == "index.md" || filepath.Base(file) == "index.html" {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() || e.Name() == filepath.Base(file) {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err == nil {
				_ = os.MkdirAll(s.uploadDir(d.ID), 0o700)
				_ = os.WriteFile(filepath.Join(s.uploadDir(d.ID), e.Name()), b, 0o600)
			}
		}
	}
	var url string
	if b := s.lastBuild(); b != nil {
		for _, e := range b.Entries {
			if e.Source == source {
				url = strings.TrimPrefix(e.URL, strings.TrimSuffix(s.cfg.Site.BasePath, "/"))
			}
		}
	}
	undo := newUndo()
	redirectsPath := filepath.Join(s.root, site.RedirectsFile)
	undo.remember(redirectsPath)
	if url != "" {
		if err := appendGone(redirectsPath, url); err != nil {
			return nil, err
		}
	}
	removed := file
	if filepath.Base(file) == "index.md" || filepath.Base(file) == "index.html" {
		removed = dir
	}
	if err := undo.removeAll(removed); err != nil {
		undo.revert()
		return nil, err
	}
	// The draft now owns the text; it is no longer tied to the removed file.
	if _, err := s.store.DB.Exec(`UPDATE drafts SET source = '' WHERE id = ?`, d.ID); err != nil {
		undo.revert()
		return nil, err
	}
	if err := s.rebuild(); err != nil {
		undo.revert()
		_, _ = s.store.DB.Exec(`UPDATE drafts SET source = ? WHERE id = ?`, source, d.ID)
		_ = s.rebuild()
		return nil, fmt.Errorf("%w:\n%s", errBuildFailed, err)
	}
	fields, _ := s.draftFields(d)
	s.announceDelete(slugFromSource(source))
	if _, err := s.git.Commit([]string{s.git.rel(removed), s.git.rel(redirectsPath), s.git.rel(filepath.Join(s.root, site.LedgerFile))}, "Unpublish: "+fields.Title); err != nil && !errors.Is(err, ErrNotRepo) {
		return d, fmt.Errorf("unpublished, but saving to git failed: %w", err)
	}
	return s.store.Draft(d.ID)
}

func appendGone(file, url string) error {
	b, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	text := string(b)
	if !strings.Contains(text, "[redirects]") {
		text += "[redirects]\n"
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += fmt.Sprintf("%q = %q\n", url, site.Gone)
	return os.WriteFile(file, []byte(text), 0o644)
}

// undo records file changes so a failed publish leaves no trace.
type undo struct {
	saved   map[string][]byte // nil value: the file did not exist
	created []string          // directories created
	order   []string
}

func newUndo() *undo { return &undo{saved: map[string][]byte{}} }

func (u *undo) remember(p string) {
	if _, ok := u.saved[p]; ok {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		b = nil
	}
	u.saved[p] = b
	u.order = append(u.order, p)
}

func (u *undo) writeFile(p string, data []byte) error {
	u.remember(p)
	dir := filepath.Dir(p)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		u.created = append(u.created, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := p + ".skimdit-tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (u *undo) removeAll(p string) error {
	_ = filepath.Walk(p, func(f string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			u.remember(f)
		}
		return nil
	})
	return os.RemoveAll(p)
}

func (u *undo) revert() {
	for _, p := range u.order {
		if b := u.saved[p]; b != nil {
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.WriteFile(p, b, 0o644)
		} else {
			_ = os.Remove(p)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(u.created)))
	for _, d := range u.created {
		_ = os.Remove(d)
	}
}

func (u *undo) paths() []string { return append([]string(nil), u.order...) }

// ---- uploads ----

// MaxUpload is the largest file the editor accepts.
const MaxUpload = 25 << 20

var uploadTypes = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif",
	".webp": "image/webp", ".svg": "image/svg+xml", ".pdf": "application/pdf",
}

var nameCleanRe = regexp.MustCompile(`[^a-z0-9]+`)

// saveUpload checks, cleans and stores an uploaded file for a draft. Images
// are decoded and re-encoded, which drops EXIF and GPS data and anything
// hidden after the image. It returns the stored file name.
func (s *Server) saveUpload(draftID int64, name string, r io.Reader) (string, error) {
	ext := strings.ToLower(filepath.Ext(name))
	want, ok := uploadTypes[ext]
	if !ok {
		return "", errors.New("only JPEG, PNG, GIF, WebP, SVG and PDF files can be uploaded (embed large video instead)")
	}
	data, err := readLimited(r, MaxUpload)
	if err != nil {
		return "", fmt.Errorf("file is larger than %d MB", MaxUpload>>20)
	}
	sniffed := http.DetectContentType(data)
	var out []byte
	switch want {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		if !strings.HasPrefix(sniffed, want) && !(want == "image/webp" && strings.HasPrefix(sniffed, "image/webp")) {
			return "", fmt.Errorf("the file is not really a %s image", strings.TrimPrefix(ext, "."))
		}
		if out, err = reencode(data, want); err != nil {
			return "", err
		}
	case "image/svg+xml":
		if out, err = sanitizeSVG(data); err != nil {
			return "", err
		}
	case "application/pdf":
		if !strings.HasPrefix(sniffed, "application/pdf") {
			return "", errors.New("the file is not really a PDF")
		}
		out = data
	}
	base := strings.Trim(nameCleanRe.ReplaceAllString(strings.ToLower(strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))), "-"), "-")
	if base == "" || len(base) > 60 {
		base = "file"
	}
	if ext == ".jpeg" {
		ext = ".jpg"
	}
	final := base + "-" + strings.ToLower(randomToken(4)[:6]) + ext
	final = strings.ReplaceAll(final, "_", "x")
	dir := s.uploadDir(draftID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return final, os.WriteFile(filepath.Join(dir, final), out, 0o600)
}

func reencode(data []byte, typ string) ([]byte, error) {
	var buf bytes.Buffer
	switch typ {
	case "image/gif":
		g, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			return nil, errors.New("could not read the GIF")
		}
		if err := gif.EncodeAll(&buf, g); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("could not read the image")
	}
	if cfg.Width*cfg.Height > 80_000_000 {
		return nil, errors.New("image is over 80 megapixels")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.New("could not read the image")
	}
	switch typ {
	case "image/jpeg":
		err = jpeg.Encode(&buf, orientJPEG(img, data), &jpeg.Options{Quality: 92})
	case "image/png":
		err = png.Encode(&buf, img)
	case "image/webp":
		err = webp.Encode(&buf, img, webp.Options{Quality: 92})
	}
	return buf.Bytes(), err
}
