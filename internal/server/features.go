// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/GTKottman/skimdit/internal/site"
)

// apiHandler routes /api/*: the few dynamic endpoints readers reach.
func (s *Server) apiHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, s.cfg.Rel("/api"))
		w.Header().Set("X-Robots-Tag", "noindex")
		switch {
		case p == "/pow" && r.Method == http.MethodGet:
			s.powChallenge(w, r)
		case p == "/comments" && r.Method == http.MethodPost:
			s.postComment(w, r)
		case p == "/comments/confirm":
			s.confirmCommentNotify(w, r)
		case p == "/comments/unsubscribe":
			s.unsubscribeCommentNotify(w, r)
		case p == "/hooks/git" && r.Method == http.MethodPost:
			s.gitWebhook(w, r)
		case strings.HasPrefix(p, "/plugins/"):
			s.pluginRoute(w, r, strings.TrimPrefix(p, "/plugins/"))
		default:
			if s.newsletterRoutes(w, r, p) {
				return
			}
			httpError(w, http.StatusNotFound, "not found")
		}
	})
}

// gitWebhook pulls and rebuilds after a push to the site's repository
// (a GitHub "push" webhook signed with SKIMDIT_WEBHOOK_SECRET).
func (s *Server) gitWebhook(w http.ResponseWriter, r *http.Request) {
	secret := getenv("SKIMDIT_WEBHOOK_SECRET")
	if secret == "" {
		httpError(w, http.StatusNotFound, "webhook not configured")
		return
	}
	body, err := readLimited(http.MaxBytesReader(w, r.Body, 1<<20), 1<<20)
	if err != nil {
		httpError(w, http.StatusBadRequest, "body too large")
		return
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	want := "sha256=" + hex.EncodeToString(m.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(r.Header.Get("X-Hub-Signature-256"))) {
		httpError(w, http.StatusUnauthorized, "bad signature")
		return
	}
	if ev := r.Header.Get("X-GitHub-Event"); ev == "ping" {
		httpError(w, http.StatusOK, "pong")
		return
	}
	go func() {
		if err := s.git.Pull(); err != nil {
			s.log.Printf("webhook: pull: %v", err)
			s.setBuildError("could not pull from git: " + err.Error())
			return
		}
		s.requestRebuild()
	}()
	httpError(w, http.StatusAccepted, "pulling and rebuilding")
}

func (s *Server) registerFeatureJobs() {
	s.jobHandlers["comment_confirm"] = s.jobCommentConfirm
	s.jobHandlers["comment_owner"] = s.jobCommentOwner
	s.jobHandlers["comment_reply"] = s.jobCommentReply
	s.jobHandlers["webmention_verify"] = s.jobWebmentionVerify
	s.jobHandlers["webmention_send"] = s.jobWebmentionSend
	s.jobHandlers["newsletter_confirm"] = s.jobNewsletterConfirm
	s.jobHandlers["newsletter"] = s.jobNewsletter
	s.registerLaterJobs()
}

// announceChannels are the jobs a newly published post fans out to.
func (s *Server) announceChannels() []string {
	var out []string
	if s.cfg.Webmention.Send {
		out = append(out, "webmention_send")
	}
	if s.cfg.Newsletter.Enabled {
		out = append(out, "newsletter")
	}
	return append(out, s.laterChannels()...)
}

func (s *Server) buildHooks() site.Hooks {
	return site.Hooks{Responses: s.responses, Syndication: s.syndication}
}

// featureRoutes serves the admin pages added by later milestones.
func (a *adminUI) featureRoutes(w http.ResponseWriter, r *http.Request, p string) bool {
	switch {
	case p == "/comments":
		a.moderation(w, r)
		return true
	case p == "/comments/action" && r.Method == http.MethodPost:
		a.moderate(w, r)
		return true
	case p == "/subscribers":
		a.subscribers(w, r)
		return true
	case p == "/subscribers/action" && r.Method == http.MethodPost:
		a.subscribersAction(w, r)
		return true
	case p == "/subscribers.csv":
		a.subscribersCSV(w, r)
		return true
	case p == "/stats":
		a.statsPage(w, r)
		return true
	}
	return false
}

func (a *adminUI) pendingCounts() map[string]int {
	return map[string]int{"comments": a.s.store.CountComments("pending") + a.s.store.CountWebmentions("pending")}
}
