// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
)

// serveStatic serves the built site with caching, compression, redirects
// and the theme's 404 page.
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		httpError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.redirectHost(w, r) {
		return
	}
	s.securityHeaders(w, r, false)
	p := r.URL.Path
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "\x00") {
		httpError(w, http.StatusBadRequest, "bad path")
		return
	}
	base := s.cfg.Site.BasePath
	if base != "/" {
		if p+"/" == base {
			http.Redirect(w, r, base, http.StatusMovedPermanently)
			return
		}
		if !strings.HasPrefix(p, base) {
			s.notFound(w, r, http.StatusNotFound)
			return
		}
	}
	// Redirects and removed pages.
	s.bmu.RLock()
	rd, ok := s.redirects[p]
	if !ok && !strings.HasSuffix(p, "/") {
		rd, ok = s.redirects[p+"/"]
	}
	s.bmu.RUnlock()
	if ok {
		if rd.status == http.StatusGone {
			s.notFound(w, r, http.StatusGone)
			return
		}
		to := rd.to
		if r.URL.RawQuery != "" {
			to += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, to, rd.status)
		return
	}

	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(p, base)), "/")
	file := filepath.Join(s.out, filepath.FromSlash(rel))
	st, err := os.Stat(file)
	if err == nil && st.IsDir() {
		if !strings.HasSuffix(p, "/") {
			to := p + "/"
			if r.URL.RawQuery != "" {
				to += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, to, http.StatusMovedPermanently)
			return
		}
		file = filepath.Join(file, "index.html")
		rel = path.Join(rel, "index.html")
		st, err = os.Stat(file)
	}
	if err != nil || st.IsDir() || strings.HasPrefix(filepath.Base(file), "_") {
		s.notFound(w, r, http.StatusNotFound)
		return
	}
	s.stats.count(r, rel, st)
	s.serveFile(w, r, file, rel, st, http.StatusOK)
}

// redirectHost sends requests for www or a bare IP to the site's own host.
func (s *Server) redirectHost(w http.ResponseWriter, r *http.Request) bool {
	if !s.cfg.Server.RedirectHost || s.cfg.BaseURL == nil {
		return false
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	want := s.cfg.BaseURL.Hostname()
	if host == "" || strings.EqualFold(host, want) || host == "localhost" {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return false
	}
	if strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".ts.net") {
		return false
	}
	to := s.cfg.BaseURL.Scheme + "://" + s.cfg.BaseURL.Host + r.URL.RequestURI()
	http.Redirect(w, r, to, http.StatusMovedPermanently)
	return true
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request, code int) {
	file := filepath.Join(s.out, "404.html")
	st, err := os.Stat(file)
	if err != nil {
		httpError(w, code, http.StatusText(code))
		return
	}
	s.serveFile(w, r, file, "404.html", st, code)
}

var compressible = map[string]bool{
	".html": true, ".css": true, ".js": true, ".json": true, ".xml": true, ".svg": true, ".txt": true, ".xsl": true, ".map": true,
}

func contentType(rel string) string {
	switch {
	case rel == "feed.xml" || rel == "rss.xml" || strings.HasSuffix(rel, "/feed.xml"):
		// application/xml lets browsers apply the feed stylesheet.
		return "application/xml; charset=utf-8"
	case strings.HasSuffix(rel, "feed.json"):
		return "application/feed+json; charset=utf-8"
	case strings.HasSuffix(rel, ".xsl"):
		return "text/xsl; charset=utf-8"
	}
	if t := mime.TypeByExtension(filepath.Ext(rel)); t != "" {
		return t
	}
	return "application/octet-stream"
}

func cacheControl(rel string) string {
	switch {
	case strings.HasPrefix(rel, "assets/"), strings.HasPrefix(rel, "media/"), strings.HasPrefix(rel, "social/"):
		// Names change when content changes.
		return "public, max-age=31536000, immutable"
	case strings.HasSuffix(rel, ".html"):
		return "public, max-age=0, must-revalidate"
	case strings.HasSuffix(rel, ".xml"), strings.HasSuffix(rel, ".json"):
		return "public, max-age=300"
	}
	return "public, max-age=3600"
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, file, rel string, st os.FileInfo, code int) {
	h := w.Header()
	h.Set("Content-Type", contentType(rel))
	h.Set("Cache-Control", cacheControl(rel))
	if code != http.StatusOK {
		h.Set("Cache-Control", "no-store")
	}
	etag := fmt.Sprintf(`W/"%x-%x"`, st.ModTime().UnixNano(), st.Size())
	h.Set("ETag", etag)
	h.Set("Last-Modified", st.ModTime().UTC().Format(http.TimeFormat))
	if code == http.StatusOK && r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	f, err := os.Open(file)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not read file")
		return
	}
	defer f.Close()
	var out io.Writer = w
	ext := filepath.Ext(rel)
	if compressible[ext] && st.Size() > 512 {
		h.Add("Vary", "Accept-Encoding")
		ae := r.Header.Get("Accept-Encoding")
		switch {
		case acceptsEncoding(ae, "br"):
			h.Set("Content-Encoding", "br")
			bw := brotli.NewWriterLevel(w, 5)
			defer bw.Close()
			out = bw
		case acceptsEncoding(ae, "gzip"):
			h.Set("Content-Encoding", "gzip")
			gz := gzipPool.Get().(*gzip.Writer)
			gz.Reset(w)
			defer func() { gz.Close(); gzipPool.Put(gz) }()
			out = gz
		default:
			h.Set("Content-Length", strconv.FormatInt(st.Size(), 10))
		}
	} else {
		h.Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	}
	w.WriteHeader(code)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(out, bufio.NewReaderSize(f, 32<<10))
}

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return w
}}

func acceptsEncoding(header, enc string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(name), enc) {
			for _, p := range strings.Split(params, ";") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(p), "q="); ok {
					q, err := strconv.ParseFloat(v, 64)
					return err == nil && q > 0
				}
			}
			return true
		}
	}
	return false
}

// loadRedirects reads the build's _redirects file.
func loadRedirectsFile(file string) map[string]redirect {
	out := map[string]redirect{}
	f, err := os.Open(file)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 3 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		code, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		out[fields[0]] = redirect{to: fields[1], status: code}
	}
	return out
}
