// SPDX-License-Identifier: AGPL-3.0-or-later

package content

import (
	"bytes"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Fields is frontmatter in the order Skimdit writes it (the editor and
// the importers use it).
type Fields struct {
	Title       string         `json:"title" yaml:"title"`
	Slug        string         `json:"slug,omitempty" yaml:"slug,omitempty"`
	Date        string         `json:"date,omitempty" yaml:"date,omitempty"`
	Updated     string         `json:"updated,omitempty" yaml:"updated,omitempty"`
	Status      string         `json:"status,omitempty" yaml:"status,omitempty"`
	Tags        []string       `json:"tags,omitempty" yaml:"tags,omitempty,flow"`
	Series      string         `json:"series,omitempty" yaml:"series,omitempty"`
	SeriesOrder int            `json:"series_order,omitempty" yaml:"series_order,omitempty"`
	Excerpt     string         `json:"excerpt,omitempty" yaml:"excerpt,omitempty"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Cover       string         `json:"cover,omitempty" yaml:"cover,omitempty"`
	CoverAlt    string         `json:"cover_alt,omitempty" yaml:"cover_alt,omitempty"`
	SocialImage string         `json:"social_image,omitempty" yaml:"social_image,omitempty"`
	Aliases     []string       `json:"aliases,omitempty" yaml:"aliases,omitempty,flow"`
	Canonical   string         `json:"canonical,omitempty" yaml:"canonical,omitempty"`
	Lang        string         `json:"lang,omitempty" yaml:"lang,omitempty"`
	TOC         *bool          `json:"toc,omitempty" yaml:"toc,omitempty"`
	Comments    *bool          `json:"comments,omitempty" yaml:"comments,omitempty"`
	Extra       map[string]any `json:"extra,omitempty" yaml:"extra,omitempty"`
}

// FileText renders frontmatter and body as a content file.
func FileText(f Fields, body string) (string, error) {
	var buf bytes.Buffer
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(f); err != nil {
		return "", err
	}
	enc.Close()
	buf.WriteString("---\n\n")
	buf.WriteString(strings.TrimLeft(strings.ReplaceAll(body, "\r\n", "\n"), "\n"))
	if !strings.HasSuffix(buf.String(), "\n") {
		buf.WriteByte('\n')
	}
	return buf.String(), nil
}

// FieldsFromItem turns a parsed item back into frontmatter fields.
func FieldsFromItem(it *Item, loc *time.Location) Fields {
	f := Fields{
		Title: it.Title, Slug: it.Slug, Tags: it.Tags, Series: it.Series, SeriesOrder: it.SeriesOrder,
		Excerpt: it.Excerpt, Description: it.Description, Cover: it.Cover, CoverAlt: it.CoverAlt,
		SocialImage: it.SocialImage, Aliases: it.Aliases, Canonical: it.Canonical, Lang: it.Lang,
		TOC: it.TOC, Comments: it.Comments, Extra: it.Extra,
	}
	if !it.Date.IsZero() {
		f.Date = it.Date.In(loc).Format("2006-01-02T15:04:05-07:00")
	}
	if !it.Updated.IsZero() {
		f.Updated = it.Updated.In(loc).Format("2006-01-02T15:04:05-07:00")
	}
	if it.Status != StatusPublished {
		f.Status = string(it.Status)
	}
	return f
}
