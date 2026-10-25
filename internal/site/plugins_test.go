// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHostilePluginCannotInjectScripts(t *testing.T) {
	if testing.Short() {
		t.Skip("compiling plugins is slow")
	}
	root := copyFixture(t, t.TempDir())
	wasm := filepath.Join(root, "hostile.wasm")
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", wasm, "github.com/GTKottman/skimdit/internal/plugins/testdata/hostile")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	cfg := filepath.Join(root, "config.toml")
	b, _ := os.ReadFile(cfg)
	os.WriteFile(cfg, append(b, []byte("\n[[plugins]]\npath = \"hostile.wasm\"\n")...), 0o644)
	out := filepath.Join(t.TempDir(), "dist")
	res, err := Run(Options{Root: root, Out: out, Now: fixtureNow})
	// The plugin's build_done asks for evil.html: the build refuses.
	if err == nil || !strings.Contains(res.Diags.Error(), "may not write .html") {
		t.Fatalf("want build_done refusal, got %v", err)
	}
	// Its post_render output is sanitized in every built post.
	for _, d := range res.Build.Entries {
		h := string(d.Content)
		if d.Kind == "post" && (strings.Contains(h, "<script") || strings.Contains(h, "onclick") || strings.Contains(h, "onerror")) {
			t.Fatalf("%s carries plugin script: %s", d.Slug, h)
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("a failed build replaced the output")
	}
}
