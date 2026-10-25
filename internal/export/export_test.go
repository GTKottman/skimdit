// SPDX-License-Identifier: AGPL-3.0-or-later

package export

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GTKottman/skimdit/internal/site"
	"github.com/GTKottman/skimdit/internal/store"
)

func tree(t *testing.T, dir string) map[string][]byte {
	out := map[string][]byte{}
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			r, _ := filepath.Rel(dir, p)
			b, _ := os.ReadFile(p)
			out[r] = b
		}
		return nil
	})
	return out
}

// TestExportImportGivesTheSameSite: an unzipped export builds byte for byte
// into the same site as the original.
func TestExportImportGivesTheSameSite(t *testing.T) {
	src, _ := filepath.Abs("../site/testdata/site")
	root := t.TempDir()
	filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		r, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(root, r), 0o755)
		}
		b, _ := os.ReadFile(p)
		return os.WriteFile(filepath.Join(root, r), b, 0o644)
	})
	// A server database with one draft.
	st, err := store.Open(filepath.Join(root, ".skimdit/data"))
	if err != nil {
		t.Fatal(err)
	}
	st.CreateDraft("post", "", `{"title":"Work in progress"}`, "Not done.")
	st.Close()

	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	zipPath := filepath.Join(t.TempDir(), "export.zip")
	rep, err := Run(root, zipPath, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Drafts != 1 || !rep.DB {
		t.Fatalf("report: %+v", rep)
	}
	restored := t.TempDir()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		dst := filepath.Join(restored, filepath.FromSlash(f.Name))
		os.MkdirAll(filepath.Dir(dst), 0o755)
		r, _ := f.Open()
		var buf bytes.Buffer
		io.Copy(&buf, r)
		r.Close()
		os.WriteFile(dst, buf.Bytes(), 0o644)
	}
	zr.Close()
	for _, want := range []string{"config.toml", "content/posts/alpha/index.md", "content/posts/alpha/red.png", "directives/note.html", "static/humans.txt", "data/skimdit.db", "drafts/1-work-in-progress/index.md", "EXPORT.md"} {
		if !names[want] {
			t.Errorf("export misses %s", want)
		}
	}
	a, err := site.Run(site.Options{Root: root, Out: filepath.Join(t.TempDir(), "a"), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	b, err := site.Run(site.Options{Root: restored, Out: filepath.Join(t.TempDir(), "b"), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	ta, tb := tree(t, a.Out), tree(t, b.Out)
	if len(ta) != len(tb) {
		t.Fatalf("file counts differ: %d vs %d", len(ta), len(tb))
	}
	for name, x := range ta {
		if !bytes.Equal(x, tb[name]) {
			t.Errorf("%s differs after export and import", name)
		}
	}
}
