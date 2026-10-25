// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"os"

	"github.com/GTKottman/skimdit/internal/site"
)

// Placeholders for parts built in later milestones (M5: replies, M6:
// newsletter and stats, M7: fediverse). Each is replaced by its real file.

func (s *Server) apiHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpError(w, http.StatusNotFound, "not found")
	})
}

func (s *Server) receiveWebmention(w http.ResponseWriter, r *http.Request) {
	httpError(w, http.StatusNotFound, "not found")
}

func (s *Server) webfinger(w http.ResponseWriter, r *http.Request) {
	httpError(w, http.StatusNotFound, "not found")
}

func (s *Server) nodeinfoIndex(w http.ResponseWriter, r *http.Request) {
	httpError(w, http.StatusNotFound, "not found")
}

func (s *Server) activityPubHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpError(w, http.StatusNotFound, "not found")
	})
}

type statsCounter struct{}

func newStatsCounter(*Server) *statsCounter                                            { return &statsCounter{} }
func (c *statsCounter) count(*http.Request, string, os.FileInfo)                       {}
func (c *statsCounter) loop(ctx context.Context)                                       { <-ctx.Done() }
func (c *statsCounter) flush()                                                         {}
func (s *Server) registerFeatureJobs()                                                 {}
func (s *Server) announceChannels() []string                                           { return nil }
func (s *Server) buildHooks() site.Hooks                                               { return site.Hooks{} }
func (a *adminUI) featureRoutes(w http.ResponseWriter, r *http.Request, p string) bool { return false }
func (a *adminUI) pendingCounts() map[string]int                                       { return nil }
