// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func load(t *testing.T, body string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(dir)
}

func TestMinimalConfig(t *testing.T) {
	c, err := load(t, "[site]\ntitle = \"X\"\nurl = \"https://x.example/blog\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Site.BasePath != "/blog/" || c.Rel("/a/") != "/blog/a/" || c.AbsURL("/blog/a/") != "https://x.example/blog/a/" {
		t.Errorf("paths: %q %q %q", c.Site.BasePath, c.Rel("/a/"), c.AbsURL("/blog/a/"))
	}
	if c.Permalinks.Post != "/posts/:slug/" || c.Location == nil {
		t.Error("defaults not applied")
	}
}

func TestUnknownKeysRejected(t *testing.T) {
	_, err := load(t, "[site]\ntitle = \"X\"\nurl = \"https://x.example\"\ntitel = \"typo\"\n[plugins_x]\n")
	if err == nil || !strings.Contains(err.Error(), "site.titel") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidation(t *testing.T) {
	_, err := load(t, "[site]\nurl = \"ftp://x\"\ntimezone = \"Mars/Base\"\n[permalinks]\npost = \"/p/:id/\"\n[robots]\nai = \"maybe\"\n")
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"site.title", "site.url", "Mars/Base", "permalinks.post", "robots.ai"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestParseErrorHasLine(t *testing.T) {
	_, err := load(t, "[site]\ntitle = \"X\"\nurl = \n")
	if err == nil || !strings.Contains(err.Error(), "config.toml:3") {
		t.Fatalf("err = %v", err)
	}
}
