// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"os"

	"github.com/GTKottman/skimdit/internal/site"
)

// Placeholders for M6 (newsletter, stats) and M7 (fediverse, cross-posting).

func (s *Server) newsletterRoutes(w http.ResponseWriter, r *http.Request, p string) bool {
	return false
}
func (s *Server) webfinger(w http.ResponseWriter, r *http.Request) {
	httpError(w, http.StatusNotFound, "not found")
}
func (s *Server) nodeinfoIndex(w http.ResponseWriter, r *http.Request) {
	httpError(w, http.StatusNotFound, "not found")
}
func (s *Server) activityPubHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { httpError(w, http.StatusNotFound, "not found") })
}
func (s *Server) fediverseReplies(e *site.Entry) []*site.Response { return nil }
func (s *Server) registerLaterJobs()                              {}
func (s *Server) laterChannels() []string                         { return nil }
func (a *adminUI) laterRoutes(w http.ResponseWriter, r *http.Request, p string) bool {
	return false
}

type statsCounter struct{}

func newStatsCounter(*Server) *statsCounter                      { return &statsCounter{} }
func (c *statsCounter) count(*http.Request, string, os.FileInfo) {}
func (c *statsCounter) loop(ctx context.Context)                 { <-ctx.Done() }
func (c *statsCounter) flush()                                   {}
