// SPDX-License-Identifier: AGPL-3.0-or-later

// Package themecheck verifies a theme against the theme contract: it builds
// a sample site that uses every page type and content feature with the
// theme, then runs the same checks as `skimdit check`.
package themecheck

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GTKottman/skimdit/internal/check"
	"github.com/GTKottman/skimdit/internal/content"
)

//go:embed all:sample
var sample embed.FS

// Now is the time the sample site is built at.
var Now = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

// Run checks the theme in themeDir ("" for the built-in theme). If out is
// set, the built sample site is kept there (for accessibility testing).
func Run(themeDir, out string) (*check.Report, error) {
	tmp, err := os.MkdirTemp("", "skimdit-themecheck-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	root := filepath.Join(tmp, "site")
	if err := fs.WalkDir(sample, "sample", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(root, strings.TrimPrefix(p, "sample"))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := sample.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	}); err != nil {
		return nil, err
	}
	if themeDir != "" {
		abs, err := filepath.Abs(themeDir)
		if err != nil {
			return nil, err
		}
		cfg := filepath.Join(root, "config.toml")
		b, err := os.ReadFile(cfg)
		if err != nil {
			return nil, err
		}
		b = append(b, []byte(fmt.Sprintf("\n[theme]\nname = %q\n", abs))...)
		if err := os.WriteFile(cfg, b, 0o644); err != nil {
			return nil, err
		}
	}
	rep, err := check.RunWith(root, Now, out)
	if err != nil {
		return nil, err
	}
	// The sample's own warnings are not the theme's fault.
	var kept content.Diagnostics
	for _, d := range rep.Diags {
		if !d.Warning {
			kept = append(kept, d)
		}
	}
	rep.Diags = kept
	return rep, nil
}
