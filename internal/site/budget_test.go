// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBuildBudget keeps big sites fast: 1,000 posts must build in under 5 seconds.
func TestBuildBudget(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing is meaningless in -short or -race mode")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[site]\ntitle = \"Big\"\nurl = \"https://big.example\"\n"), 0o644)
	body := "## Heading\n\nSome text with a [link](https://example.com), `code`, *emphasis* and a list:\n\n- one\n- two\n\n```go\nfunc f() int { return 1 }\n```\n\n$x^2$\n\n"
	for i := 0; i < 1000; i++ {
		p := filepath.Join(dir, "content/posts", fmt.Sprintf("post-%04d", i), "index.md")
		os.MkdirAll(filepath.Dir(p), 0o755)
		md := fmt.Sprintf("---\ntitle: Post %d\ndate: 2025-01-01T00:%02d:%02d\ntags: [t%d, common]\n---\n\n%s", i, i/60%60, i%60, i%25, body+body+body)
		os.WriteFile(p, []byte(md), 0o644)
	}
	start := time.Now()
	res, err := Run(Options{Root: dir, Out: filepath.Join(dir, "dist"), Now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("%v %v", err, res)
	}
	took := time.Since(start)
	t.Logf("1000 posts: %d files in %s", len(res.Files), took)
	if took > 5*time.Second {
		t.Errorf("build took %s, budget is 5s", took)
	}
}
