// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"net/http"

	"github.com/GTKottman/skimdit/internal/site"
)

// Placeholders for M7 (fediverse, cross-posting).

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
