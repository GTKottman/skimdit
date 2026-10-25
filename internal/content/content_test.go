// SPDX-License-Identifier: AGPL-3.0-or-later

package content

import (
	"strings"
	"testing"
	"time"
)

var chicago, _ = time.LoadLocation("America/Chicago")

func TestParseErrorsHaveLines(t *testing.T) {
	src := "---\ntitle: Hi\ndate: 2026-01-02\ntitel: typo\nstatus: maybe\n---\nbody\n"
	_, diags := Parse([]byte(src), "content/posts/x.md", KindPost, "x", chicago)
	if len(diags) != 2 {
		t.Fatalf("diags = %v", diags)
	}
	if diags[0].Line != 4 || !strings.Contains(diags[0].Message, `did you mean "title"`) {
		t.Errorf("typo diag = %+v", diags[0])
	}
	if diags[1].Line != 5 {
		t.Errorf("status diag = %+v", diags[1])
	}
}

func TestMissingFrontmatterAndTitle(t *testing.T) {
	if _, d := Parse([]byte("no frontmatter"), "a.md", KindPage, "a", time.UTC); len(d) != 1 {
		t.Errorf("want 1 diag, got %v", d)
	}
	if _, d := Parse([]byte("---\nslug: a\n---\n"), "a.md", KindPage, "a", time.UTC); !d.HasErrors() {
		t.Error("missing title must fail")
	}
	if _, d := Parse([]byte("---\ntitle: x\n---\n"), "a.md", KindPost, "a", time.UTC); !d.HasErrors() {
		t.Error("post without date must fail")
	}
}

func TestDatesWithoutZoneUseSiteZone(t *testing.T) {
	cases := map[string]string{
		"2026-03-08":                "2026-03-08T00:00:00-06:00",
		"2026-07-01T09:30":          "2026-07-01T09:30:00-05:00",
		"2026-07-01 09:30:15":       "2026-07-01T09:30:15-05:00",
		"2026-07-01T09:30:00Z":      "2026-07-01T09:30:00Z",
		"2026-07-01T09:30:00+02:00": "2026-07-01T09:30:00+02:00",
	}
	for in, want := range cases {
		got, err := ParseDate(in, chicago)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got.Format(time.RFC3339) != want {
			t.Errorf("%s = %s, want %s", in, got.Format(time.RFC3339), want)
		}
	}
	if _, err := ParseDate("next tuesday", chicago); err == nil {
		t.Error("want error")
	}
}

func TestVisibility(t *testing.T) {
	now := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	mk := func(s Status, d time.Time) *Item { return &Item{Kind: KindPost, Status: s, Date: d} }
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	if mk(StatusDraft, past).Visible(now) {
		t.Error("draft visible")
	}
	if mk(StatusPublished, future).Visible(now) {
		t.Error("future post visible")
	}
	if !mk(StatusScheduled, past).Listed(now) {
		t.Error("scheduled post past its date not listed")
	}
	u := mk(StatusUnlisted, past)
	if !u.Visible(now) || u.Listed(now) {
		t.Error("unlisted must be visible but not listed")
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Hello, World!":   "hello-world",
		"Don't Panic":     "dont-panic",
		"Café au lait":    "cafe-au-lait",
		"  --Go 1.27--  ": "go-1-27",
		"日本語":             "",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTagsAsString(t *testing.T) {
	it, d := Parse([]byte("---\ntitle: x\ndate: 2026-01-01\ntags: go, web\n---\n"), "a.md", KindPost, "a", time.UTC)
	if d.HasErrors() || len(it.Tags) != 2 || it.Tags[1] != "web" {
		t.Errorf("tags = %v %v", it, d)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte("---\ntitle: x\ndate: 2026-01-01\n---\nbody"))
	f.Add([]byte("---\n: :\n---"))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = Parse(b, "f.md", KindPost, "f", time.UTC)
	})
}
