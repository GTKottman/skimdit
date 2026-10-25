// SPDX-License-Identifier: AGPL-3.0-or-later

package importer

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/GTKottman/skimdit/internal/content"
)

type ghostExport struct {
	DB []struct {
		Data struct {
			Posts []struct {
				ID              string `json:"id"`
				Title           string `json:"title"`
				Slug            string `json:"slug"`
				HTML            string `json:"html"`
				Plaintext       string `json:"plaintext"`
				FeatureImage    string `json:"feature_image"`
				FeatureImageAlt string `json:"feature_image_alt"`
				Type            string `json:"type"`
				Status          string `json:"status"`
				PublishedAt     string `json:"published_at"`
				CreatedAt       string `json:"created_at"`
				UpdatedAt       string `json:"updated_at"`
				CustomExcerpt   string `json:"custom_excerpt"`
				MetaDescription string `json:"meta_description"`
				CanonicalURL    string `json:"canonical_url"`
			} `json:"posts"`
			Tags []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"tags"`
			PostsTags []struct {
				PostID string `json:"post_id"`
				TagID  string `json:"tag_id"`
			} `json:"posts_tags"`
			PostsMeta []struct {
				PostID          string `json:"post_id"`
				FeatureImageAlt string `json:"feature_image_alt"`
				MetaDescription string `json:"meta_description"`
			} `json:"posts_meta"`
		} `json:"data"`
	} `json:"db"`
}

// Ghost imports a Ghost JSON export (Settings › Labs › Export).
func Ghost(file string, opts Options) (*Report, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var ex ghostExport
	if err := json.Unmarshal(b, &ex); err != nil || len(ex.DB) == 0 {
		return nil, fmt.Errorf("%s is not a Ghost export: %v", file, err)
	}
	data := ex.DB[0].Data
	tagNames := map[string]string{}
	for _, t := range data.Tags {
		tagNames[t.ID] = t.Name
	}
	postTags := map[string][]string{}
	for _, pt := range data.PostsTags {
		if n := tagNames[pt.TagID]; n != "" && n[0] != '#' { // #internal tags stay private
			postTags[pt.PostID] = append(postTags[pt.PostID], n)
		}
	}
	meta := map[string]int{}
	for i, m := range data.PostsMeta {
		meta[m.PostID] = i
	}
	rep := &Report{}
	var posts []*Post
	for _, gp := range data.Posts {
		kind := content.KindPost
		if gp.Type == "page" {
			kind = content.KindPage
		}
		p := &Post{Kind: kind, From: gp.Title}
		p.Fields.Title = firstNonEmpty(gp.Title, "Untitled")
		p.Fields.Slug = gp.Slug
		if kind == content.KindPost {
			t, _ := time.Parse(time.RFC3339, firstNonEmpty(gp.PublishedAt, gp.CreatedAt))
			if t.IsZero() {
				t, _ = time.Parse("2006-01-02 15:04:05", firstNonEmpty(gp.PublishedAt, gp.CreatedAt))
			}
			p.Fields.Date = dateString(t, opts.Location)
		}
		switch gp.Status {
		case "published":
		case "scheduled":
			p.Fields.Status = "scheduled"
		default:
			p.Fields.Status = "draft"
		}
		p.Fields.Excerpt = gp.CustomExcerpt
		p.Fields.Description = gp.MetaDescription
		p.Fields.Tags = uniqueSorted(postTags[gp.ID])
		if i, ok := meta[gp.ID]; ok {
			p.Fields.CoverAlt = data.PostsMeta[i].FeatureImageAlt
			p.Fields.Description = firstNonEmpty(p.Fields.Description, data.PostsMeta[i].MetaDescription)
		}
		p.Fields.CoverAlt = firstNonEmpty(p.Fields.CoverAlt, gp.FeatureImageAlt)
		p.Fields.Cover = gp.FeatureImage
		if gp.CanonicalURL != "" {
			if u, err := url.Parse(gp.CanonicalURL); err == nil && u.IsAbs() {
				p.Fields.Canonical = gp.CanonicalURL
			}
		}
		// Ghost serves posts at /<slug>/: keep that address working.
		p.Fields.Aliases = []string{"/" + gp.Slug + "/"}
		if gp.HTML == "" {
			p.Markdown = gp.Plaintext
			rep.warn("%s: no HTML in the export; imported the plain text", gp.Title)
		} else {
			p.HTML = gp.HTML
		}
		posts = append(posts, p)
	}
	return rep, write(posts, opts, rep)
}
