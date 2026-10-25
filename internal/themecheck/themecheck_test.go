// SPDX-License-Identifier: AGPL-3.0-or-later

package themecheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDefaultThemeMeetsContract: the built-in theme must always pass.
func TestDefaultThemeMeetsContract(t *testing.T) {
	rep, err := Run("", "")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("default theme fails its own contract:\n%s", rep.Diags.Error())
	}
}

func TestBrokenThemeFails(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "theme.toml"), []byte("name = \"broken\"\nengine_api = 99\n"), 0o644)
	if _, err := Run(dir, ""); err == nil || !strings.Contains(err.Error(), "engine_api 99") {
		t.Fatalf("want engine_api error, got %v", err)
	}
}
