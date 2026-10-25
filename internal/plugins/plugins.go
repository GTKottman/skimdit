// SPDX-License-Identifier: AGPL-3.0-or-later

// Package plugins runs WebAssembly plugins in a sandbox: no files, no
// network, no environment, a memory cap and a time limit per call.
package plugins

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/GTKottman/skimdit/plugin"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

const (
	// MemoryLimitPages caps a plugin's memory (64 KiB pages): 32 MiB.
	MemoryLimitPages = 512
	// CallTimeout bounds one call.
	CallTimeout = 2 * time.Second
	// MaxOutput bounds what a plugin may answer.
	MaxOutput = 4 << 20
)

// Plugin is a loaded plugin.
type Plugin struct {
	Path     string
	Manifest plugin.Manifest
	config   json.RawMessage
	rt       wazero.Runtime
	mod      wazero.CompiledModule
	mu       sync.Mutex
}

// Host holds every loaded plugin.
type Host struct {
	Plugins    []*Plugin
	directives map[string]*Plugin
}

// Load compiles each plugin and asks for its manifest.
func Load(root string, specs []struct {
	Path   string
	Config map[string]any
}) (*Host, error) {
	h := &Host{directives: map[string]*Plugin{}}
	for _, sp := range specs {
		path := sp.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		p, err := load(path, sp.Config)
		if err != nil {
			h.Close()
			return nil, fmt.Errorf("plugin %s: %w", sp.Path, err)
		}
		for _, d := range p.Manifest.Directives {
			if other, ok := h.directives[d]; ok {
				h.Close()
				return nil, fmt.Errorf("plugins %s and %s both want the %q directive", other.Manifest.Name, p.Manifest.Name, d)
			}
			h.directives[d] = p
		}
		h.Plugins = append(h.Plugins, p)
	}
	return h, nil
}

func load(path string, cfg map[string]any) (*Plugin, error) {
	wasm, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().
		WithMemoryLimitPages(MemoryLimitPages).
		WithCloseOnContextDone(true))
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, err
	}
	mod, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("not a valid WebAssembly module: %w", err)
	}
	c, _ := json.Marshal(cfg)
	p := &Plugin{Path: path, config: c, rt: rt, mod: mod}
	var m plugin.Manifest
	if err := p.call("manifest", nil, &m); err != nil {
		p.Close()
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if m.Name == "" {
		p.Close()
		return nil, errors.New("manifest has no name")
	}
	if m.EngineAPI != plugin.API {
		p.Close()
		return nil, fmt.Errorf("built for engine_api %d; this Skimdit speaks %d", m.EngineAPI, plugin.API)
	}
	p.Manifest = m
	return p, nil
}

// Close releases a plugin.
func (p *Plugin) Close() { p.rt.Close(context.Background()) }

// Close releases every plugin.
func (h *Host) Close() {
	if h == nil {
		return
	}
	for _, p := range h.Plugins {
		p.Close()
	}
}

// Has reports whether a plugin implements a hook.
func (p *Plugin) Has(hook string) bool {
	for _, h := range p.Manifest.Hooks {
		if h == hook {
			return true
		}
	}
	return false
}

// call runs the module once: request on stdin, response on stdout.
func (p *Plugin) call(hook string, data any, out any) error {
	d, err := json.Marshal(data)
	if err != nil {
		return err
	}
	req, _ := json.Marshal(plugin.Request{Hook: hook, API: plugin.API, Config: p.config, Data: d})
	var stdout, stderr limitedBuffer
	stdout.max, stderr.max = MaxOutput, 64<<10
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	cfg := wazero.NewModuleConfig().
		WithStdin(bytes.NewReader(req)).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithRandSource(rand.Reader).
		WithSysWalltime().
		WithSysNanotime().
		WithName("")
	p.mu.Lock()
	mod, err := p.rt.InstantiateModule(ctx, p.mod, cfg)
	p.mu.Unlock()
	if mod != nil {
		mod.Close(context.Background())
	}
	if err != nil {
		var exit *sys.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 0 {
			err = nil
		} else if ctx.Err() != nil {
			return fmt.Errorf("took longer than %s", CallTimeout)
		} else {
			return fmt.Errorf("%v: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	if stdout.over {
		return fmt.Errorf("answer is larger than %d bytes", MaxOutput)
	}
	var probe struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(stdout.Bytes(), &probe) == nil && probe.Error != "" {
		return errors.New(probe.Error)
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		return fmt.Errorf("answer is not valid JSON: %v", err)
	}
	return nil
}

type limitedBuffer struct {
	bytes.Buffer
	max  int
	over bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		b.over = true
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

// PostRender passes a rendered post through every post_render plugin.
func (h *Host) PostRender(post plugin.Post) (string, error) {
	if h == nil {
		return post.HTML, nil
	}
	for _, p := range h.Plugins {
		if !p.Has("post_render") {
			continue
		}
		var res plugin.HTMLResult
		if err := p.call("post_render", post, &res); err != nil {
			return "", fmt.Errorf("plugin %s: %w", p.Manifest.Name, err)
		}
		post.HTML = res.HTML
	}
	return post.HTML, nil
}

// Directive renders a plugin directive; ok is false when no plugin owns it.
func (h *Host) Directive(name string, attrs map[string]string, inner string) (string, bool, error) {
	if h == nil {
		return "", false, nil
	}
	p, ok := h.directives[name]
	if !ok {
		return "", false, nil
	}
	var res plugin.HTMLResult
	if err := p.call("directive", plugin.Directive{Name: name, Attrs: attrs, Inner: inner}, &res); err != nil {
		return "", true, fmt.Errorf("plugin %s: %w", p.Manifest.Name, err)
	}
	return res.HTML, true, nil
}

// BuildDone collects extra files from build_done plugins.
func (h *Host) BuildDone(info plugin.BuildDone) ([]plugin.File, error) {
	if h == nil {
		return nil, nil
	}
	var out []plugin.File
	for _, p := range h.Plugins {
		if !p.Has("build_done") {
			continue
		}
		var res plugin.FilesResult
		if err := p.call("build_done", info, &res); err != nil {
			return nil, fmt.Errorf("plugin %s: %w", p.Manifest.Name, err)
		}
		for _, f := range res.Files {
			ext := strings.ToLower(filepath.Ext(f.Path))
			if ext == ".html" || ext == ".htm" || ext == ".js" || ext == ".mjs" || ext == ".svg" || ext == ".xhtml" {
				return nil, fmt.Errorf("plugin %s: may not write %s files (%s)", p.Manifest.Name, ext, f.Path)
			}
			clean := filepath.ToSlash(filepath.Clean("/" + f.Path))
			if strings.HasPrefix(clean, "/assets/") || strings.HasPrefix(clean, "/admin") || strings.HasPrefix(clean, "/api") {
				return nil, fmt.Errorf("plugin %s: may not write under %s", p.Manifest.Name, clean)
			}
			out = append(out, plugin.File{Path: strings.TrimPrefix(clean, "/"), Content: f.Content})
		}
	}
	return out, nil
}

// Route answers /api/plugins/<name>/<path>.
func (h *Host) Route(name string, req plugin.RouteRequest) (*plugin.RouteResponse, bool, error) {
	if h == nil {
		return nil, false, nil
	}
	for _, p := range h.Plugins {
		if p.Manifest.Name != name || !p.Has("route") {
			continue
		}
		var res plugin.RouteResponse
		if err := p.call("route", req, &res); err != nil {
			return nil, true, fmt.Errorf("plugin %s: %w", name, err)
		}
		return &res, true, nil
	}
	return nil, false, nil
}
