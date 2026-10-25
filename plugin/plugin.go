// SPDX-License-Identifier: AGPL-3.0-or-later

// Package plugin is the Skimdit plugin SDK. A plugin is a WebAssembly
// (WASI) program: Skimdit runs it once per call with a JSON request on
// standard input and reads a JSON response from standard output.
//
// Build a Go plugin with:
//
//	GOOS=wasip1 GOARCH=wasm go build -o my-plugin.wasm .
//
// A plugin has no access to files, the network or the environment. See
// docs/PLUGINS.md for the protocol, which any language can implement.
package plugin

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// API is the plugin protocol version (the engine_api a plugin targets).
const API = 1

// Request is one call from Skimdit.
type Request struct {
	// Hook is manifest, post_render, directive, build_done or route.
	Hook   string          `json:"hook"`
	API    int             `json:"api"`
	Config json.RawMessage `json:"config"`
	Data   json.RawMessage `json:"data"`
}

// Manifest describes a plugin; it answers the "manifest" hook.
type Manifest struct {
	Name       string   `json:"name"`
	Version    string   `json:"version"`
	EngineAPI  int      `json:"engine_api"`
	Hooks      []string `json:"hooks"`
	Directives []string `json:"directives,omitempty"`
}

// Post is what the post_render hook receives.
type Post struct {
	Slug  string   `json:"slug"`
	Title string   `json:"title"`
	URL   string   `json:"url"`
	Tags  []string `json:"tags"`
	HTML  string   `json:"html"`
}

// HTMLResult answers post_render and directive hooks. The HTML is sanitized
// again by Skimdit: scripts, styles and event handlers never get through.
type HTMLResult struct {
	HTML string `json:"html"`
}

// Directive is what the directive hook receives.
type Directive struct {
	Name  string            `json:"name"`
	Attrs map[string]string `json:"attrs"`
	Inner string            `json:"inner"`
}

// BuildDone is what the build_done hook receives.
type BuildDone struct {
	Site  string   `json:"site"`
	URL   string   `json:"url"`
	Posts []Post   `json:"posts"`
	Files []string `json:"files"`
}

// File is an extra output file (build_done). Paths are relative to the
// output folder; .html and .js files are not allowed.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// FilesResult answers build_done.
type FilesResult struct {
	Files []File `json:"files"`
}

// RouteRequest is a request to /api/plugins/<name>/<path> (server only).
type RouteRequest struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Query  map[string]string `json:"query"`
	Body   string            `json:"body"`
}

// RouteResponse answers a route request.
type RouteResponse struct {
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Body        string `json:"body"`
}

// Handler answers one request; return any JSON-encodable value.
type Handler func(req *Request) (any, error)

// Run reads the request, calls h and writes the response. Call it from main.
func Run(h Handler) {
	if err := run(os.Stdin, os.Stdout, h); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer, h Handler) error {
	var req Request
	if err := json.NewDecoder(in).Decode(&req); err != nil {
		return fmt.Errorf("reading request: %w", err)
	}
	res, err := h(&req)
	if err != nil {
		return json.NewEncoder(out).Encode(map[string]string{"error": err.Error()})
	}
	return json.NewEncoder(out).Encode(res)
}

// Decode reads a request's data into v.
func (r *Request) Decode(v any) error { return json.Unmarshal(r.Data, v) }

// DecodeConfig reads the plugin's config table from config.toml into v.
func (r *Request) DecodeConfig(v any) error {
	if len(r.Config) == 0 || string(r.Config) == "null" {
		return nil
	}
	return json.Unmarshal(r.Config, v)
}
