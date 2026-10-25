// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fixtureNow is the build time for fixture tests: after every fixture post
// except the scheduled one.
var fixtureNow = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

func buildFixture(t *testing.T, out string) *Result {
	t.Helper()
	cache := t.TempDir()
	root := copyFixture(t, cache)
	res, err := Run(Options{Root: root, Out: out, Now: fixtureNow, Workers: 4})
	if err != nil {
		if res != nil {
			t.Fatalf("%v:\n%s", err, res.Diags.Error())
		}
		t.Fatal(err)
	}
	return res
}

// copyFixture copies testdata/site so cache files never land in testdata.
func copyFixture(t *testing.T, dir string) string {
	t.Helper()
	root := filepath.Join(dir, "site")
	err := filepath.Walk("testdata/site", func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("testdata/site", p)
		dst := filepath.Join(root, rel)
		if info.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func isText(name string) bool {
	switch filepath.Ext(name) {
	case ".html", ".xml", ".json", ".txt", ".css", ".js", ".webmanifest":
		return !strings.HasPrefix(name, "assets/")
	}
	return false
}

// TestGolden compares every text output file with testdata/golden.
// Run `go test ./internal/site -run Golden -update` after an intended change.
func TestGolden(t *testing.T) {
	out := filepath.Join(t.TempDir(), "dist")
	res := buildFixture(t, out)
	for _, d := range res.Diags {
		t.Logf("diag: %s", d.Error())
	}
	got := readTree(t, out)
	golden := "testdata/golden"
	var list bytes.Buffer
	for _, f := range res.Files {
		list.WriteString(f + "\n")
	}
	got["FILES.txt"] = list.Bytes()
	if *update {
		os.RemoveAll(golden)
		for name, b := range got {
			if name != "FILES.txt" && !isText(name) {
				continue
			}
			p := filepath.Join(golden, filepath.FromSlash(name))
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	want := readTree(t, golden)
	for name, w := range want {
		g, ok := got[name]
		if !ok {
			t.Errorf("missing output file %s", name)
			continue
		}
		if !bytes.Equal(g, w) {
			t.Errorf("%s differs from golden (run with -update if intended)\n%s", name, firstDiff(string(w), string(g)))
		}
	}
	for name := range got {
		if (isText(name) || name == "FILES.txt") && want[name] == nil {
			t.Errorf("new output file %s is not in golden", name)
		}
	}
}

func firstDiff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			return "line " + strconv.Itoa(i+1) + ":\n  want: " + x + "\n  got:  " + y
		}
	}
	return ""
}

// TestDeterministic builds twice and requires byte-identical output.
func TestDeterministic(t *testing.T) {
	a := readTree(t, buildFixture(t, filepath.Join(t.TempDir(), "a")).Out)
	b := readTree(t, buildFixture(t, filepath.Join(t.TempDir(), "b")).Out)
	if len(a) != len(b) {
		t.Fatalf("file count differs: %d vs %d", len(a), len(b))
	}
	for name, x := range a {
		if !bytes.Equal(x, b[name]) {
			t.Errorf("%s differs between two builds", name)
		}
	}
}

// TestNoDraftLeaks is the single-gate test: no draft or scheduled post may
// appear anywhere in the output, and unlisted posts only at their own URL.
func TestNoDraftLeaks(t *testing.T) {
	res := buildFixture(t, filepath.Join(t.TempDir(), "dist"))
	files := readTree(t, res.Out)
	for name, b := range files {
		s := string(b)
		for _, secret := range []string{"Gamma draft", "/posts/gamma/", "Delta scheduled", "/posts/delta/"} {
			if strings.Contains(s, secret) {
				t.Errorf("%s leaks %q", name, secret)
			}
		}
		if name != "posts/epsilon/index.html" && (strings.Contains(s, "Epsilon unlisted") || strings.Contains(s, "/posts/epsilon/")) {
			t.Errorf("%s lists the unlisted post", name)
		}
	}
	if _, ok := files["posts/epsilon/index.html"]; !ok {
		t.Error("unlisted post was not built at its URL")
	}
	if !strings.Contains(string(files["posts/epsilon/index.html"]), `<meta name="robots" content="noindex">`) {
		t.Error("unlisted post must be noindex")
	}
}

func TestFeatures(t *testing.T) {
	res := buildFixture(t, filepath.Join(t.TempDir(), "dist"))
	files := readTree(t, res.Out)
	alpha := string(files["posts/alpha/index.html"])
	for _, want := range []string{
		`href="/posts/beta/"`, `href="/posts/alpha/notes.pdf"`, "<picture>", `type="image/avif"`,
		`class="math math-display"`, `<figure class="code" data-lang="go">`, `callout-note`,
		`data-embed="youtube"`, `<p class="site-note">site directive</p>`, `Part 1 of Intro`, `role="doc-endnotes"`,
	} {
		if !strings.Contains(alpha, want) {
			t.Errorf("alpha missing %q", want)
		}
	}
	if _, ok := files["posts/alpha/notes.pdf"]; !ok {
		t.Error("co-located PDF not copied")
	}
	beta := string(files["posts/beta/index.html"])
	for _, want := range []string{`href="/posts/alpha/#code"`, `Beta has a custom description.`, `class="entry-cover"`, `datetime="2026-10-10T09:30:00-05:00"`, `Previous in series`} {
		if !strings.Contains(beta, want) {
			t.Errorf("beta missing %q", want)
		}
	}
	if !strings.Contains(string(files["old-beta/index.html"]), `url=/posts/beta/`) {
		t.Error("alias redirect missing")
	}
	zeta := string(files["posts/zeta/index.html"])
	if strings.Contains(zeta, "<script>alert") {
		t.Error("HTML post not sanitized")
	}
	// Pagination: 3 listed posts at 2 per page.
	if _, ok := files["page/2/index.html"]; !ok {
		t.Error("home page 2 missing")
	}
	if !strings.Contains(string(files["tags/go/index.html"]), "Beta") {
		t.Error("tag page missing post")
	}
	if !strings.Contains(string(files["humans.txt"]), "test") {
		t.Error("static file not copied")
	}
	if !strings.Contains(alpha, "Posts that link here") {
		t.Error("alpha should list beta as a backlink")
	}
}

func TestBasePath(t *testing.T) {
	cache := t.TempDir()
	root := copyFixture(t, cache)
	cfgPath := filepath.Join(root, "config.toml")
	b, _ := os.ReadFile(cfgPath)
	os.WriteFile(cfgPath, bytes.Replace(b, []byte(`url = "https://fixture.example"`), []byte(`url = "https://fixture.example/blog/"`), 1), 0o644)
	out := filepath.Join(t.TempDir(), "dist")
	res, err := Run(Options{Root: root, Out: out, Now: fixtureNow})
	if err != nil {
		t.Fatalf("%v %v", err, res)
	}
	alpha, _ := os.ReadFile(filepath.Join(out, "posts/alpha/index.html"))
	for _, want := range []string{`href="/blog/posts/beta/"`, `href="/blog/"`, `src="/blog/media/`, `https://fixture.example/blog/posts/alpha/`} {
		if !strings.Contains(string(alpha), want) {
			t.Errorf("base path: alpha missing %q", want)
		}
	}
}
