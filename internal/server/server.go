// SPDX-License-Identifier: AGPL-3.0-or-later

// Package server is `skimdit serve`: it serves the built site and runs the
// dynamic parts (web editor, comments, newsletter, stats, webmentions,
// ActivityPub) next to it. Readers only ever get pre-built files; the
// dynamic surface is limited to /admin, /api, /webmention, /ap and
// /.well-known.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GTKottman/skimdit/internal/config"
	"github.com/GTKottman/skimdit/internal/plugins"
	"github.com/GTKottman/skimdit/internal/site"
	"github.com/GTKottman/skimdit/internal/store"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Options configure a server.
type Options struct {
	Root string
	Log  *log.Logger
	// Secret signs sessions and links; from SKIMDIT_SECRET.
	Secret string
	// Now replaces the clock (tests).
	Now func() time.Time
	// NoWorkers disables background jobs (tests that drive them by hand).
	NoWorkers bool
	// HTTPClient is used for outgoing requests (tests swap it).
	HTTPClient *http.Client
}

// Server is a running Skimdit site.
type Server struct {
	cfg    *config.Config
	opts   Options
	root   string
	data   string
	out    string
	store  *store.Store
	log    *log.Logger
	secret string
	now    func() time.Time
	git    *gitRepo
	wa     *webauthn.WebAuthn
	client *http.Client

	bmu         sync.RWMutex
	build       *site.Build
	buildErr    string
	buildAt     time.Time
	redirects   map[string]redirect
	nextChange  time.Time
	rebuildCh   chan struct{}
	buildDoneCh chan struct{}
	buildMu     sync.Mutex

	loginLimit *limiter
	apiLimit   *limiter
	postLimit  *limiter
	setupToken string
	waMu       sync.Mutex
	waSessions map[string]*webauthn.SessionData

	jobHandlers map[string]jobHandler
	mux         *http.ServeMux
	admin       *adminUI
	stats       *statsCounter
	testMailer  Mailer
	plugins     *plugins.Host
	powUsed     map[string]time.Time
	powMu       sync.Mutex
}

type redirect struct {
	to     string
	status int
}

// MinSecretLength is the shortest SKIMDIT_SECRET accepted.
const MinSecretLength = 32

// New prepares a server: it opens the database and builds the site.
func New(opts Options) (*Server, error) {
	if len(opts.Secret) < MinSecretLength {
		return nil, fmt.Errorf("SKIMDIT_SECRET must be set to at least %d random characters (for example: openssl rand -base64 48)", MinSecretLength)
	}
	cfg, err := config.Load(opts.Root)
	if err != nil {
		return nil, err
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Log == nil {
		opts.Log = log.New(os.Stderr, "", log.LstdFlags)
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = safeHTTPClient()
	}
	data := cfg.Path(cfg.Server.Data)
	st, err := store.Open(data)
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	st.SetClock(opts.Now)
	repo := cfg.Root
	if cfg.Server.Repo != "" {
		repo = cfg.Path(cfg.Server.Repo)
	}
	s := &Server{
		cfg:         cfg,
		opts:        opts,
		root:        cfg.Root,
		data:        data,
		out:         filepath.Join(data, "site"),
		store:       st,
		log:         opts.Log,
		secret:      opts.Secret,
		now:         opts.Now,
		client:      opts.HTTPClient,
		git:         &gitRepo{dir: repo, branch: cfg.Server.Branch, push: cfg.Server.Push, log: opts.Log, name: firstNonEmpty(cfg.Author.Name, "Skimdit"), email: firstNonEmpty(cfg.Author.Email, "skimdit@"+cfg.Domain())},
		redirects:   map[string]redirect{},
		rebuildCh:   make(chan struct{}, 1),
		buildDoneCh: make(chan struct{}),
		loginLimit:  newLimiter(10, 15*time.Minute),
		apiLimit:    newLimiter(30, time.Minute),
		postLimit:   newLimiter(5, 10*time.Minute),
		waSessions:  map[string]*webauthn.SessionData{},
		powUsed:     map[string]time.Time{},
		jobHandlers: map[string]jobHandler{},
	}
	s.stats = newStatsCounter(s)
	if err := s.setupWebAuthn(); err != nil {
		return nil, err
	}
	if _, err := st.Credentials(); errors.Is(err, store.ErrNotFound) {
		s.setupToken = randomToken(24)
	}
	if err := s.loadPlugins(); err != nil {
		return nil, err
	}
	s.registerJobs()
	s.admin, err = newAdminUI(s)
	if err != nil {
		return nil, err
	}
	s.routes()
	if err := s.rebuild(); err != nil {
		// Serve the last good output if there is one; the admin shows the error.
		s.log.Printf("first build failed: %v", err)
		if _, statErr := os.Stat(s.out); statErr != nil {
			return nil, err
		}
	}
	return s, nil
}

// Close releases the database and plugins.
func (s *Server) Close() error {
	s.plugins.Close()
	return s.store.Close()
}

// Store exposes the database (tests and commands).
func (s *Server) Store() *store.Store { return s.store }

// SetupURL is the one-time address for creating the owner account, or ""
// once the account exists.
func (s *Server) SetupURL() string {
	if s.setupToken == "" {
		return ""
	}
	return s.cfg.Rel("/admin/setup") + "?token=" + s.setupToken
}

// Handler is the whole HTTP handler.
func (s *Server) Handler() http.Handler { return s.mux }

// Run serves on cfg.Server.Listen until ctx ends.
func (s *Server) Run(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          s.log,
	}
	bg, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); s.rebuildLoop(bg) }()
	if !s.opts.NoWorkers {
		wg.Add(1)
		go func() { defer wg.Done(); s.workerLoop(bg) }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); s.stats.loop(bg) }()
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = srv.Shutdown(sctx)
	}()
	err := srv.Serve(ln)
	cancel()
	wg.Wait()
	s.stats.flush()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) routes() {
	m := http.NewServeMux()
	base := strings.TrimSuffix(s.cfg.Site.BasePath, "/")
	m.HandleFunc("GET /healthz", s.health)
	m.Handle(base+"/admin/", s.adminHandler())
	m.HandleFunc(base+"/admin", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, base+"/admin/", http.StatusMovedPermanently)
	})
	m.Handle(base+"/api/", s.apiHandler())
	m.HandleFunc("POST "+base+"/webmention", s.receiveWebmention)
	m.HandleFunc("/.well-known/webfinger", s.webfinger)
	m.HandleFunc("/.well-known/nodeinfo", s.nodeinfoIndex)
	m.Handle(base+"/ap/", s.activityPubHandler())
	m.HandleFunc("/", s.serveStatic)
	s.mux = m
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	s.bmu.RLock()
	errMsg, at := s.buildErr, s.buildAt
	s.bmu.RUnlock()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := s.store.Version(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, "database: %v\n", err)
		return
	}
	if _, err := os.Stat(filepath.Join(s.out, "index.html")); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, "no site built yet")
		return
	}
	fmt.Fprintf(w, "ok\nbuilt %s\n", at.UTC().Format(time.RFC3339))
	if errMsg != "" {
		fmt.Fprintf(w, "last build failed (serving the previous one): %s\n", firstLine(errMsg))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " ..."
	}
	return s
}

func (s *Server) adminHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.securityHeaders(w, r, true)
		s.admin.ServeHTTP(w, r)
	})
}

// httpError writes a short plain-text error.
func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	fmt.Fprintln(w, msg)
}
