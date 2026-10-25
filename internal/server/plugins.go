// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"io"
	"net/http"
	"strings"

	"github.com/GTKottman/skimdit/internal/plugins"
	"github.com/GTKottman/skimdit/plugin"
)

// loadPlugins loads the plugins that answer routes on the server.
func (s *Server) loadPlugins() error {
	if len(s.cfg.Plugins) == 0 {
		return nil
	}
	specs := make([]struct {
		Path   string
		Config map[string]any
	}, len(s.cfg.Plugins))
	for i, p := range s.cfg.Plugins {
		specs[i].Path, specs[i].Config = p.Path, p.Config
	}
	h, err := plugins.Load(s.root, specs)
	if err != nil {
		return err
	}
	s.plugins = h
	return nil
}

var pluginTypes = map[string]bool{"application/json": true, "text/plain": true, "text/csv": true, "application/xml": true, "application/feed+json": true}

// pluginRoute serves /api/plugins/<name>/<path>. Plugins answer data, never
// HTML pages, and the answer is sandboxed by its headers.
func (s *Server) pluginRoute(w http.ResponseWriter, r *http.Request, rest string) {
	name, sub, _ := strings.Cut(rest, "/")
	if !s.apiLimit.allow("plugin:"+s.clientIP(r), s.now()) {
		httpError(w, http.StatusTooManyRequests, "slow down")
		return
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	q := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			q[k] = v[0]
		}
	}
	res, ok, err := s.plugins.Route(name, plugin.RouteRequest{Method: r.Method, Path: "/" + sub, Query: q, Body: string(body)})
	if !ok {
		httpError(w, http.StatusNotFound, "no such plugin route")
		return
	}
	if err != nil {
		s.log.Printf("plugin route %s: %v", name, err)
		httpError(w, http.StatusBadGateway, "the plugin failed")
		return
	}
	ct := strings.TrimSpace(strings.SplitN(res.ContentType, ";", 2)[0])
	if !pluginTypes[ct] {
		ct = "text/plain"
	}
	status := res.Status
	if status < 200 || status > 599 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", ct+"; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, res.Body)
}
