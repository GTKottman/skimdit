// SPDX-License-Identifier: AGPL-3.0-or-later

package plugins

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/GTKottman/skimdit/plugin"
)

// buildWasm compiles a Go plugin to WebAssembly.
func buildWasm(t *testing.T, pkg string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("compiling plugins is slow")
	}
	out := filepath.Join(t.TempDir(), filepath.Base(pkg)+".wasm")
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", out, pkg)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building %s: %v\n%s", pkg, err, b)
	}
	return out
}

func spec(path string, cfg map[string]any) []struct {
	Path   string
	Config map[string]any
} {
	return []struct {
		Path   string
		Config map[string]any
	}{{Path: path, Config: cfg}}
}

func TestExamplePlugins(t *testing.T) {
	abbr := buildWasm(t, "github.com/GTKottman/skimdit/plugins/examples/abbreviations")
	books := buildWasm(t, "github.com/GTKottman/skimdit/plugins/examples/reading-list")
	h, err := Load("/", append(spec(abbr, map[string]any{"terms": map[string]any{"HTML": "HyperText Markup Language"}}),
		spec(books, map[string]any{"books": []any{map[string]any{"title": "Middlemarch", "author": "George Eliot", "year": 1871}}})...))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	out, err := h.PostRender(plugin.Post{HTML: "<p>HTML and <code>HTML</code> and HTML.</p>"})
	if err != nil || !strings.Contains(out, `<abbr title="HyperText Markup Language">HTML</abbr> and <code>HTML</code> and HTML.`) {
		t.Fatalf("abbreviations: %v %q", err, out)
	}
	html, ok, err := h.Directive("books", map[string]string{"year": "1871"}, "")
	if err != nil || !ok || !strings.Contains(html, "<cite>Middlemarch</cite> by George Eliot (1871)") {
		t.Fatalf("books directive: %v %v %q", ok, err, html)
	}
	files, err := h.BuildDone(plugin.BuildDone{})
	if err != nil || len(files) != 1 || files[0].Path != "books.json" {
		t.Fatalf("build_done: %v %+v", err, files)
	}
}

func TestSandbox(t *testing.T) {
	hostile := buildWasm(t, "github.com/GTKottman/skimdit/internal/plugins/testdata/hostile")
	h, err := Load("/", spec(hostile, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if _, _, err := h.Directive("loop", nil, ""); err == nil || !strings.Contains(err.Error(), "took longer") {
		t.Errorf("infinite loop not stopped: %v", err)
	}
	if _, _, err := h.Directive("hog", nil, ""); err == nil {
		t.Error("memory hog not stopped")
	}
	out, _, err := h.Directive("readfile", nil, "")
	if err != nil || !strings.Contains(out, "no file access") {
		t.Errorf("plugin read a host file: %v %q", err, out)
	}
	if _, err := h.BuildDone(plugin.BuildDone{}); err == nil || !strings.Contains(err.Error(), "may not write .html") {
		t.Errorf("html output allowed: %v", err)
	}
	// The plugin keeps working after being stopped.
	if out, _, err := h.Directive("inject", nil, ""); err != nil || out != "<p>ok</p>" {
		t.Errorf("after a stopped call: %v %q", err, out)
	}
}

func TestBadModule(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.wasm")
	os.WriteFile(p, []byte("not wasm"), 0o644)
	if _, err := Load("/", spec(p, nil)); err == nil {
		t.Fatal("garbage accepted as a plugin")
	}
}
