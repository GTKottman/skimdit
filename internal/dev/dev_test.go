// SPDX-License-Identifier: AGPL-3.0-or-later

package dev

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestDevServerRebuildsAndShowsErrors(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[site]\ntitle = \"Dev\"\nurl = \"https://d.example\"\n"), 0o644)
	post := filepath.Join(dir, "content/posts/a/index.md")
	os.MkdirAll(filepath.Dir(post), 0o755)
	os.WriteFile(post, []byte("---\ntitle: First\ndate: 2026-01-01\nstatus: draft\n---\nHi\n"), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	go func() {
		if err := Run(ctx, Options{Root: dir, Addr: "127.0.0.1:0", Drafts: true, Log: log.New(io.Discard, "", 0), Ready: ready}); err != nil {
			t.Error(err)
		}
	}()
	addr := "http://" + <-ready
	code, body := get(t, addr+"/posts/a/")
	if code != 200 || !strings.Contains(body, "First") || !strings.Contains(body, "/__skimdit/livereload.js") {
		t.Fatalf("draft page: %d %q", code, body)
	}
	wait := func(cond func() bool) bool {
		for i := 0; i < 50; i++ {
			if cond() {
				return true
			}
			time.Sleep(100 * time.Millisecond)
		}
		return false
	}
	os.WriteFile(post, []byte("---\ntitle: Second\ndate: 2026-01-01\n---\nHi\n"), 0o644)
	if !wait(func() bool { _, b := get(t, addr+"/posts/a/"); return strings.Contains(b, "Second") }) {
		t.Fatal("did not rebuild after an edit")
	}
	os.WriteFile(post, []byte("---\ntitle: Broken\ndatee: x\n---\n"), 0o644)
	if !wait(func() bool {
		c, b := get(t, addr+"/")
		return c == 500 && strings.Contains(b, "unknown frontmatter field")
	}) {
		t.Fatal("did not show the build error")
	}
	if c, _ := get(t, addr+"/__skimdit/livereload.js"); c != 200 {
		t.Error("livereload.js missing")
	}
}
