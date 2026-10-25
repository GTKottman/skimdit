// SPDX-License-Identifier: AGPL-3.0-or-later

package content

import (
	"fmt"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Fields lists every frontmatter key, for validation and docs.
var Fields = []string{
	"title", "slug", "date", "updated", "status", "tags", "series", "series_order",
	"excerpt", "description", "cover", "cover_alt", "social_image", "aliases", "canonical", "lang",
	"toc", "comments", "schema_version", "extra",
}

var fieldSet = func() map[string]bool {
	m := map[string]bool{}
	for _, f := range Fields {
		m[f] = true
	}
	return m
}()

// decodeFrontmatter fills it from YAML. Line numbers in diagnostics are
// file lines (the frontmatter starts on line 2).
func decodeFrontmatter(src []byte, it *Item, file string, loc *time.Location) Diagnostics {
	const lineOffset = 1
	var diags Diagnostics
	errAt := func(n *yaml.Node, format string, args ...any) {
		line := 0
		if n != nil {
			line = n.Line + lineOffset
		}
		diags = append(diags, Diagnostic{File: file, Line: line, Message: fmt.Sprintf(format, args...)})
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		msg := strings.TrimPrefix(err.Error(), "yaml: ")
		line := 0
		if _, scanErr := fmt.Sscanf(msg, "line %d:", &line); scanErr == nil {
			msg = msg[strings.Index(msg, ":")+1:]
			line += lineOffset
		}
		return Diagnostics{{File: file, Line: line, Message: "frontmatter: " + strings.TrimSpace(msg)}}
	}
	if len(doc.Content) == 0 {
		return Diagnostics{{File: file, Line: 1, Message: "frontmatter is empty; at least a title is required"}}
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return Diagnostics{{File: file, Line: m.Line + lineOffset, Message: "frontmatter must be key: value pairs"}}
	}
	it.Status = StatusPublished
	it.Schema = SchemaVersion
	seen := map[string]bool{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		key := k.Value
		if !fieldSet[key] {
			hint := ""
			if s := closest(key); s != "" {
				hint = fmt.Sprintf(" (did you mean %q?)", s)
			}
			errAt(k, "unknown frontmatter field %q%s", key, hint)
			continue
		}
		if seen[key] {
			errAt(k, "field %q appears twice", key)
			continue
		}
		seen[key] = true
		switch key {
		case "title":
			it.Title = scalar(v, key, errAt)
		case "slug":
			it.Slug = scalar(v, key, errAt)
		case "date":
			it.Date = date(v, key, loc, errAt)
		case "updated":
			it.Updated = date(v, key, loc, errAt)
		case "status":
			s := Status(scalar(v, key, errAt))
			switch s {
			case StatusDraft, StatusPublished, StatusScheduled, StatusUnlisted:
				it.Status = s
			default:
				errAt(v, "status must be draft, published, scheduled or unlisted, not %q", s)
			}
		case "tags":
			it.Tags = list(v, key, errAt)
		case "series":
			it.Series = scalar(v, key, errAt)
		case "series_order":
			var n int
			if err := v.Decode(&n); err != nil || n < 0 {
				errAt(v, "series_order must be a whole number")
			}
			it.SeriesOrder = n
		case "excerpt":
			it.Excerpt = scalar(v, key, errAt)
		case "description":
			it.Description = scalar(v, key, errAt)
		case "cover":
			it.Cover = scalar(v, key, errAt)
		case "cover_alt":
			it.CoverAlt = scalar(v, key, errAt)
		case "social_image":
			it.SocialImage = scalar(v, key, errAt)
		case "aliases":
			it.Aliases = list(v, key, errAt)
			for _, a := range it.Aliases {
				if !strings.HasPrefix(a, "/") {
					errAt(v, "alias %q must be a site path starting with /", a)
				}
			}
		case "canonical":
			it.Canonical = scalar(v, key, errAt)
			if it.Canonical != "" && !strings.HasPrefix(it.Canonical, "https://") && !strings.HasPrefix(it.Canonical, "http://") {
				errAt(v, "canonical must be a full URL")
			}
		case "lang":
			it.Lang = scalar(v, key, errAt)
		case "toc":
			b := boolean(v, key, errAt)
			it.TOC = &b
		case "comments":
			b := boolean(v, key, errAt)
			it.Comments = &b
		case "schema_version":
			var n int
			if err := v.Decode(&n); err != nil || n < 1 {
				errAt(v, "schema_version must be a whole number of at least 1")
			} else if n > SchemaVersion {
				errAt(v, "schema_version %d is newer than this engine supports (%d); upgrade Skimdit", n, SchemaVersion)
			}
			it.Schema = n
		case "extra":
			var x map[string]any
			if err := v.Decode(&x); err != nil {
				errAt(v, "extra must be a map of key: value pairs")
			}
			it.Extra = x
		}
	}
	if strings.TrimSpace(it.Title) == "" {
		diags = append(diags, Diagnostic{File: file, Line: 1, Message: "title is required"})
	}
	if it.Cover != "" && it.CoverAlt == "" {
		diags = append(diags, Diagnostic{File: file, Line: 1, Message: "cover needs cover_alt (describe the image for readers who cannot see it)"})
	}
	return diags
}

type errFunc func(n *yaml.Node, format string, args ...any)

func scalar(v *yaml.Node, key string, errAt errFunc) string {
	if v.Kind != yaml.ScalarNode {
		errAt(v, "%s must be a single value", key)
		return ""
	}
	if v.Tag == "!!null" {
		return ""
	}
	return v.Value
}

func boolean(v *yaml.Node, key string, errAt errFunc) bool {
	var b bool
	if v.Kind != yaml.ScalarNode || v.Decode(&b) != nil {
		errAt(v, "%s must be true or false", key)
	}
	return b
}

func list(v *yaml.Node, key string, errAt errFunc) []string {
	switch v.Kind {
	case yaml.SequenceNode:
		out := make([]string, 0, len(v.Content))
		for _, c := range v.Content {
			if c.Kind != yaml.ScalarNode {
				errAt(c, "%s entries must be plain values", key)
				continue
			}
			if s := strings.TrimSpace(c.Value); s != "" {
				out = append(out, s)
			}
		}
		return out
	case yaml.ScalarNode:
		if v.Tag == "!!null" || strings.TrimSpace(v.Value) == "" {
			return nil
		}
		// "tags: go, web" is accepted as a convenience.
		var out []string
		for _, s := range strings.Split(v.Value, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	errAt(v, "%s must be a list", key)
	return nil
}

var dateLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02T15:04",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// ParseDate parses a frontmatter date. A date written without a timezone is
// read in loc (the site timezone), never in the machine's local zone.
func ParseDate(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range dateLayouts {
		var t time.Time
		var err error
		if strings.Contains(layout, "Z07:00") {
			t, err = time.Parse(layout, s)
		} else {
			t, err = time.ParseInLocation(layout, s, loc)
		}
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a date; use 2006-01-02 or 2006-01-02T15:04:05-07:00", s)
}

func date(v *yaml.Node, key string, loc *time.Location, errAt errFunc) time.Time {
	if v.Kind != yaml.ScalarNode {
		errAt(v, "%s must be a date", key)
		return time.Time{}
	}
	t, err := ParseDate(v.Value, loc)
	if err != nil {
		errAt(v, "%s: %v", key, err)
	}
	return t
}

// closest suggests a known field for a typo, using edit distance.
func closest(s string) string {
	best, bestD := "", 3
	for _, f := range Fields {
		if d := editDistance(s, f); d < bestD {
			best, bestD = f, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
