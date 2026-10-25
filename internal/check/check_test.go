// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSite(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const cfg = "[site]\ntitle = \"T\"\nurl = \"https://t.example\"\n[feeds]\natom = false\nrss = false\njson = false\n[webmention]\nreceive = false\n"

func TestBrokenLinksAndAnchors(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"config.toml":              cfg,
		"content/posts/a/index.md": "---\ntitle: A\ndate: 2026-01-01\n---\n[gone](/nowhere/) [bad anchor](post:b#missing) [ok](post:b#real) [rel](../b/)\n",
		"content/posts/b/index.md": "---\ntitle: B\ndate: 2026-01-02\n---\n## Real\n",
	})
	rep, err := Run(dir, time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, d := range rep.Diags.Errors() {
		msgs = append(msgs, d.Error())
	}
	all := strings.Join(msgs, "\n")
	if !strings.Contains(all, `href="/nowhere/"`) || !strings.Contains(all, `no element with id "missing"`) {
		t.Errorf("missing expected errors:\n%s", all)
	}
	if strings.Contains(all, "#real") || strings.Contains(all, "../b/") {
		t.Errorf("false positive:\n%s", all)
	}
}

func TestDraftCommittedToGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := writeSite(t, map[string]string{
		"config.toml":                   cfg,
		"content/posts/secret/index.md": "---\ntitle: Secret\ndate: 2026-01-01\nstatus: draft\n---\nhidden\n",
	})
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	rep, err := Run(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() || !strings.Contains(rep.Diags.Error(), "draft is committed to git") {
		t.Errorf("want draft-in-git error, got %s", rep.Diags.Error())
	}
}

func TestCleanSitePasses(t *testing.T) {
	dir := writeSite(t, map[string]string{
		"config.toml":              cfg,
		"content/posts/a/index.md": "---\ntitle: A\ndate: 2026-01-01\n---\nHello.\n",
	})
	rep, err := Run(dir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Errorf("clean site failed:\n%s", rep.Diags.Error())
	}
}
