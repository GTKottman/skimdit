// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dev is the local preview server: it rebuilds on every change,
// reloads open pages and shows build errors in the browser.
package dev

import (
	"compress/gzip"
	"context"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GTKottman/skimdit/internal/config"
	"github.com/GTKottman/skimdit/internal/site"
	"github.com/fsnotify/fsnotify"
)

// Options configure the dev server.
type Options struct {
	Root   string
	Addr   string
	Drafts bool
	Log    *log.Logger
	// Ready, if set, receives the listening address.
	Ready chan<- string
}

type server struct {
	opts    Options
	out     string
	mu      sync.RWMutex
	lastErr string
	version int
	subs    map[chan int]bool
	smu     sync.Mutex
	base    string
}

// Run serves until ctx ends.
func Run(ctx context.Context, opts Options) error {
	out, err := os.MkdirTemp("", "skimdit-dev-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(out)
	s := &server{opts: opts, out: filepath.Join(out, "site"), subs: map[chan int]bool{}}
	s.rebuild()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	if err := s.watchAll(w); err != nil {
		return err
	}
	go s.watchLoop(ctx, w)

	ln, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	addr := ln.Addr().String()
	opts.Log.Printf("dev server: http://%s%s (drafts shown, Ctrl+C to stop)", displayAddr(addr), s.base)
	if opts.Ready != nil {
		opts.Ready <- addr
	}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func displayAddr(a string) string {
	if strings.HasPrefix(a, "[::]") || strings.HasPrefix(a, "0.0.0.0") {
		_, port, _ := net.SplitHostPort(a)
		return "localhost:" + port
	}
	return a
}

func (s *server) rebuild() {
	start := time.Now()
	cfg, err := config.Load(s.opts.Root)
	var res *site.Result
	if err == nil {
		s.base = cfg.Site.BasePath
		res, err = site.Run(site.Options{Root: s.opts.Root, Config: cfg, Out: s.out, Drafts: s.opts.Drafts, Dev: true})
	}
	s.mu.Lock()
	switch {
	case err != nil && res != nil:
		s.lastErr = res.Diags.Errors().Error()
	case err != nil:
		s.lastErr = err.Error()
	default:
		s.lastErr = ""
	}
	s.version++
	v := s.version
	msg := s.lastErr
	s.mu.Unlock()
	if msg != "" {
		s.opts.Log.Printf("build failed:\n%s", msg)
	} else {
		warn := 0
		for _, d := range res.Diags {
			if d.Warning {
				s.opts.Log.Printf("warning: %s", d.Error())
				warn++
			}
		}
		s.opts.Log.Printf("built %d entries in %s", res.Entries, time.Since(start).Round(time.Millisecond))
	}
	s.smu.Lock()
	for ch := range s.subs {
		select {
		case ch <- v:
		default:
		}
	}
	s.smu.Unlock()
}

func (s *server) watchAll(w *fsnotify.Watcher) error {
	return filepath.WalkDir(s.opts.Root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if p != s.opts.Root && (strings.HasPrefix(name, ".") || name == "dist" || name == "node_modules") {
				return filepath.SkipDir
			}
			return w.Add(p)
		}
		return nil
	})
}

func (s *server) watchLoop(ctx context.Context, w *fsnotify.Watcher) {
	var timer *time.Timer
	fire := make(chan struct{}, 1)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if st, err := os.Stat(ev.Name); err == nil && st.IsDir() && ev.Has(fsnotify.Create) {
				_ = s.watchAll(w)
			}
			base := filepath.Base(ev.Name)
			if strings.HasPrefix(base, ".") || strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".swp") {
				continue
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(120*time.Millisecond, func() {
				select {
				case fire <- struct{}{}:
				default:
				}
			})
		case <-fire:
			s.rebuild()
		case err := <-w.Errors:
			s.opts.Log.Printf("watch: %v", err)
		}
	}
}

const reloadJS = `(function(){var v=null;function c(){var e=new EventSource("/__skimdit/events");e.onmessage=function(m){if(v!==null&&m.data!==v){location.reload()}v=m.data};e.onerror=function(){e.close();setTimeout(c,1000)}}c()})();`

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/__skimdit/livereload.js":
		w.Header().Set("Content-Type", "text/javascript")
		io.WriteString(w, reloadJS)
		return
	case "/__skimdit/events":
		s.events(w, r)
		return
	}
	s.mu.RLock()
	errMsg := s.lastErr
	s.mu.RUnlock()
	if errMsg != "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Build failed</title><style>body{background:#1a0d0d;color:#ffd7d7;font:15px/1.5 ui-monospace,monospace;padding:2rem}h1{font-size:1.2rem}</style><h1>Skimdit build failed</h1><pre>%s</pre><p>Fix the file and save; this page reloads by itself.</p><script src="/__skimdit/livereload.js"></script>`, html.EscapeString(errMsg))
		return
	}
	p := r.URL.Path
	if s.base != "/" {
		if !strings.HasPrefix(p, s.base) && p != strings.TrimSuffix(s.base, "/") {
			http.Redirect(w, r, s.base, http.StatusFound)
			return
		}
		p = "/" + strings.TrimPrefix(p, s.base)
	}
	file := filepath.Join(s.out, filepath.FromSlash(p))
	if st, err := os.Stat(file); err == nil && st.IsDir() {
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
		file = filepath.Join(file, "index.html")
	}
	if _, err := os.Stat(file); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		if b, err := os.ReadFile(filepath.Join(s.out, "404.html")); err == nil {
			w.Write(b)
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") && compressible(file) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		http.ServeFile(gzipWriter{w, gz}, r, file)
		return
	}
	http.ServeFile(w, r, file)
}

func compressible(f string) bool {
	switch strings.ToLower(filepath.Ext(f)) {
	case ".html", ".css", ".js", ".json", ".xml", ".svg", ".txt":
		return true
	}
	return false
}

type gzipWriter struct {
	http.ResponseWriter
	gz *gzip.Writer
}

func (g gzipWriter) Write(b []byte) (int, error) { return g.gz.Write(b) }
func (g gzipWriter) WriteHeader(code int) {
	g.ResponseWriter.Header().Del("Content-Length")
	g.ResponseWriter.WriteHeader(code)
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch := make(chan int, 1)
	s.smu.Lock()
	s.subs[ch] = true
	s.smu.Unlock()
	defer func() {
		s.smu.Lock()
		delete(s.subs, ch)
		s.smu.Unlock()
	}()
	s.mu.RLock()
	v := s.version
	s.mu.RUnlock()
	fmt.Fprintf(w, "data: %d\n\n", v)
	fl.Flush()
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case v := <-ch:
			fmt.Fprintf(w, "data: %d\n\n", v)
			fl.Flush()
		case <-tick.C:
			io.WriteString(w, ": ping\n\n")
			fl.Flush()
		}
	}
}
