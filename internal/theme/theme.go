// SPDX-License-Identifier: AGPL-3.0-or-later

// Package theme loads a theme: templates, partials and assets, layered so a
// site can override any single file without forking the theme.
//
// Lookup order for every file: <site>/theme/ (overrides), then the chosen
// theme folder, then the built-in default theme.
package theme

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/GTKottman/skimdit/internal/version"
	"github.com/GTKottman/skimdit/themes"
)

// Pages are the page templates every theme must provide (in templates/).
var Pages = []string{"home", "post", "page", "list", "tags", "series-list", "archive", "search", "404", "redirect"}

// Manifest is a theme's theme.toml.
type Manifest struct {
	Name        string `toml:"name"`
	Version     string `toml:"version"`
	EngineAPI   int    `toml:"engine_api"`
	Description string `toml:"description"`
	License     string `toml:"license"`
	Author      string `toml:"author"`
}

// Theme is a loaded theme.
type Theme struct {
	Manifest Manifest
	fsys     fs.FS
	pages    map[string]*template.Template
	// AssetBase is the fingerprinted folder assets are served from, e.g. "/assets/3fa2c1d0/".
	assetDir string
	mu       sync.Mutex
}

// layered is an fs.FS that returns the first layer holding a file.
type layered []fs.FS

func (l layered) Open(name string) (fs.File, error) {
	var lastErr error = fs.ErrNotExist
	for _, f := range l {
		file, err := f.Open(name)
		if err == nil {
			return file, nil
		}
		lastErr = err
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: lastErr}
}

// ReadDir merges directories across layers.
func (l layered) ReadDir(name string) ([]fs.DirEntry, error) {
	seen := map[string]fs.DirEntry{}
	found := false
	for _, f := range l {
		entries, err := fs.ReadDir(f, name)
		if err != nil {
			continue
		}
		found = true
		for _, e := range entries {
			if _, ok := seen[e.Name()]; !ok {
				seen[e.Name()] = e
			}
		}
	}
	if !found {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	out := make([]fs.DirEntry, 0, len(seen))
	for _, e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

// Load loads the theme named in the config ("default" or a folder path,
// relative to the site root) with the site's overrides on top.
func Load(siteRoot, name string, funcs template.FuncMap) (*Theme, error) {
	def, err := fs.Sub(themes.Default, "default")
	if err != nil {
		return nil, err
	}
	var layers layered
	if st, err := os.Stat(filepath.Join(siteRoot, "theme")); err == nil && st.IsDir() {
		layers = append(layers, os.DirFS(filepath.Join(siteRoot, "theme")))
	}
	if name != "" && name != "default" {
		dir := name
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(siteRoot, name)
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("theme %q: folder %s not found", name, dir)
		}
		layers = append(layers, os.DirFS(dir))
	}
	layers = append(layers, def)

	t := &Theme{fsys: layers, pages: map[string]*template.Template{}}
	// The manifest comes from the chosen theme, not from site overrides.
	manifestFS := def
	if name != "" && name != "default" {
		dir := name
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(siteRoot, name)
		}
		manifestFS = os.DirFS(dir)
	}
	b, err := fs.ReadFile(manifestFS, "theme.toml")
	if err != nil {
		return nil, fmt.Errorf("theme %q: theme.toml is missing", name)
	}
	md, err := toml.Decode(string(b), &t.Manifest)
	if err != nil {
		return nil, fmt.Errorf("theme %q: theme.toml: %w", name, err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		return nil, fmt.Errorf("theme %q: theme.toml: unknown key %s", name, und[0])
	}
	if err := CheckAPI(t.Manifest.EngineAPI); err != nil {
		return nil, fmt.Errorf("theme %q: %w", t.Manifest.Name, err)
	}
	if err := t.parse(funcs); err != nil {
		return nil, err
	}
	sum, err := t.hashAssets()
	if err != nil {
		return nil, err
	}
	t.assetDir = "/assets/" + sum + "/"
	return t, nil
}

// CheckAPI says whether a theme or plugin built for api works with this engine.
func CheckAPI(api int) error {
	switch {
	case api == 0:
		return errors.New("engine_api is missing from theme.toml")
	case api > version.EngineAPI:
		return fmt.Errorf("needs engine_api %d, but this Skimdit supports %d; upgrade Skimdit", api, version.EngineAPI)
	case api < version.EngineAPI-1:
		return fmt.Errorf("was built for engine_api %d, which this Skimdit no longer supports (current: %d)", api, version.EngineAPI)
	}
	return nil
}

// Deprecated reports whether a theme uses the previous engine_api (still
// supported, with a warning, for 12 months).
func (t *Theme) Deprecated() bool { return t.Manifest.EngineAPI < version.EngineAPI }

func (t *Theme) parse(funcs template.FuncMap) error {
	base := template.New("").Funcs(funcs)
	partials, err := fs.Glob(t.fsys, "templates/partials/*.html")
	if err != nil {
		return err
	}
	// Merge partial names across layers.
	if entries, err := fs.ReadDir(t.fsys, "templates/partials"); err == nil {
		partials = partials[:0]
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".html") {
				partials = append(partials, "templates/partials/"+e.Name())
			}
		}
	}
	for _, name := range append([]string{"templates/base.html"}, partials...) {
		src, err := fs.ReadFile(t.fsys, name)
		if err != nil {
			return fmt.Errorf("theme: %s: %w", name, err)
		}
		tname := strings.TrimSuffix(path.Base(name), ".html")
		if strings.Contains(name, "/partials/") {
			tname = "partials/" + tname
		}
		if _, err := base.New(tname).Parse(string(src)); err != nil {
			return fmt.Errorf("theme: %w", err)
		}
	}
	for _, p := range Pages {
		name := "templates/" + p + ".html"
		src, err := fs.ReadFile(t.fsys, name)
		if err != nil {
			return fmt.Errorf("theme: %s is missing", name)
		}
		pt, err := base.Clone()
		if err != nil {
			return err
		}
		if _, err := pt.New(p).Parse(string(src)); err != nil {
			return fmt.Errorf("theme: %w", err)
		}
		t.pages[p] = pt
	}
	return nil
}

// Render executes page template name with data.
func (t *Theme) Render(w io.Writer, name string, data any) error {
	pt, ok := t.pages[name]
	if !ok {
		return fmt.Errorf("theme has no %s template", name)
	}
	var buf bytes.Buffer
	if err := pt.ExecuteTemplate(&buf, name, data); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// AssetDir is the URL folder for assets, relative to the base path: "/assets/<hash>/".
func (t *Theme) AssetDir() string { return t.assetDir }

// ReadAsset returns an asset file (relative to assets/).
func (t *Theme) ReadAsset(name string) ([]byte, error) {
	return fs.ReadFile(t.fsys, path.Join("assets", name))
}

// Assets lists every asset path relative to assets/.
func (t *Theme) Assets() ([]string, error) {
	var out []string
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := fs.ReadDir(t.fsys, dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		for _, e := range entries {
			p := path.Join(dir, e.Name())
			if e.IsDir() {
				if err := walk(p); err != nil {
					return err
				}
				continue
			}
			out = append(out, strings.TrimPrefix(p, "assets/"))
		}
		return nil
	}
	if err := walk("assets"); err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

func (t *Theme) hashAssets() (string, error) {
	names, err := t.Assets()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, n := range names {
		b, err := t.ReadAsset(n)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", n, len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:10], nil
}

// HasTemplate reports whether the theme layers hold a file.
func (t *Theme) HasFile(name string) bool {
	_, err := fs.Stat(t.fsys, name)
	return err == nil
}
