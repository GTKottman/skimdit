// SPDX-License-Identifier: AGPL-3.0-or-later

package theme

import (
	"io/fs"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/GTKottman/skimdit/themes"
)

// TestDefaultThemeContrast measures the default theme's colour tokens in
// both modes: text at least 4.5:1, control borders at least 3:1 (WCAG 2.2 AA).
func TestDefaultThemeContrast(t *testing.T) {
	css, err := fs.ReadFile(themes.Default, "default/assets/css/main.css")
	if err != nil {
		t.Fatal(err)
	}
	s := string(css)
	dark := tokens(s[:strings.Index(s, "@media (prefers-color-scheme: light)")])
	lightBlock := s[strings.Index(s, `:root[data-theme="light"]`):]
	light := tokens(lightBlock[:strings.Index(lightBlock, "}")])
	for mode, toks := range map[string]map[string]string{"dark": dark, "light": merge(dark, light)} {
		for _, bg := range []string{"--ink-0", "--ink-1", "--ink-2"} {
			for _, fg := range []string{"--ink-9", "--ink-6"} {
				if r := ratio(toks[fg], toks[bg]); r < 4.5 {
					t.Errorf("%s: text %s on %s is %.2f:1, need 4.5:1", mode, fg, bg, r)
				}
			}
		}
		for _, bg := range []string{"--ink-0", "--ink-2"} {
			if r := ratio(toks["--ink-5"], toks[bg]); r < 3 {
				t.Errorf("%s: control border on %s is %.2f:1, need 3:1", mode, bg, r)
			}
		}
	}
	if r := ratio(dark["--accent"], dark["--ink-0"]); r < 4.5 {
		t.Errorf("dark: link colour is %.2f:1 on the ground, need 4.5:1", r)
	}
}

var tokenRe = regexp.MustCompile(`(--[a-z0-9-]+):\s*(#[0-9a-fA-F]{6})`)

func tokens(css string) map[string]string {
	m := map[string]string{}
	for _, mm := range tokenRe.FindAllStringSubmatch(css, -1) {
		if _, ok := m[mm[1]]; !ok {
			m[mm[1]] = mm[2]
		}
	}
	return m
}

func merge(a, b map[string]string) map[string]string {
	m := map[string]string{}
	for k, v := range a {
		m[k] = v
	}
	for k, v := range b {
		m[k] = v
	}
	return m
}

func lum(hex string) float64 {
	var c [3]float64
	for i := 0; i < 3; i++ {
		v, _ := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		x := float64(v) / 255
		if x <= 0.03928 {
			c[i] = x / 12.92
		} else {
			c[i] = math.Pow((x+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
}

func ratio(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	x, y := lum(a), lum(b)
	if x < y {
		x, y = y, x
	}
	return (x + 0.05) / (y + 0.05)
}
