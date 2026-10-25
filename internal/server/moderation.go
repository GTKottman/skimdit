// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/GTKottman/skimdit/internal/markdown"
	"github.com/GTKottman/skimdit/internal/store"
)

func (a *adminUI) moderation(w http.ResponseWriter, r *http.Request) {
	s := a.s
	status := r.URL.Query().Get("show")
	switch status {
	case "approved", "spam":
	default:
		status = "pending"
	}
	comments, _ := s.store.Comments(status, 200)
	mentions, _ := s.store.Webmentions(status, 200)
	titles := map[string]string{}
	if b := s.lastBuild(); b != nil {
		for _, e := range b.Entries {
			titles[e.Slug] = e.Title
		}
	}
	a.render(w, r, http.StatusOK, "comments", &adminPage{Title: "Comments", Nav: "comments", Data: map[string]any{
		"Show": status, "Comments": comments, "Mentions": mentions, "Titles": titles,
		"Counts": map[string]int{
			"pending":  s.store.CountComments("pending") + s.store.CountWebmentions("pending"),
			"approved": s.store.CountComments("approved") + s.store.CountWebmentions("approved"),
			"spam":     s.store.CountComments("spam") + s.store.CountWebmentions("spam"),
		},
	}})
}

// moderate approves, marks as spam or deletes a comment or mention, or
// posts the owner's reply.
func (a *adminUI) moderate(w http.ResponseWriter, r *http.Request) {
	s := a.s
	back := "comments?show=" + r.FormValue("show")
	action := r.FormValue("action")
	kind := r.FormValue("kind")
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	status := map[string]string{"approve": "approved", "spam": "spam", "delete": "deleted", "pending": "pending"}[action]
	switch {
	case kind == "mention" && status != "":
		_ = s.store.SetWebmentionStatus(id, status)
	case kind == "comment" && status != "":
		c, err := s.store.Comment(id)
		if err != nil {
			a.redirect(w, r, back, "That comment no longer exists.")
			return
		}
		_ = s.store.SetCommentStatus(id, status)
		if status == "approved" && c.ParentID != 0 {
			payload, _ := json.Marshal(map[string]int64{"comment": id})
			_, _ = s.store.Enqueue("comment_reply", fmt.Sprintf("comment_reply:%d", id), string(payload), 0)
		}
	case kind == "comment" && action == "reply":
		parent, err := s.store.Comment(id)
		body := strings.TrimSpace(r.FormValue("body"))
		if err != nil || body == "" {
			a.redirect(w, r, back, "Write a reply first.")
			return
		}
		if parent.Status != "approved" {
			_ = s.store.SetCommentStatus(parent.ID, "approved")
		}
		rid, err := s.store.AddComment(&store.Comment{Post: parent.Post, ParentID: parent.ID, Author: firstNonEmpty(s.cfg.Author.Name, s.cfg.Site.Title),
			URL: s.cfg.AbsURL(s.cfg.Site.BasePath), Body: body, HTML: markdown.RenderComment(body), Status: "approved"})
		if err == nil {
			payload, _ := json.Marshal(map[string]int64{"comment": rid})
			_, _ = s.store.Enqueue("comment_reply", fmt.Sprintf("comment_reply:%d", rid), string(payload), 0)
		}
		s.requestRebuild()
		a.redirect(w, r, back, "Reply posted.")
		return
	default:
		a.redirect(w, r, back, "Unknown action.")
		return
	}
	s.requestRebuild()
	a.redirect(w, r, back, map[string]string{"approved": "Approved; it appears on the post in a moment.", "spam": "Marked as spam.", "deleted": "Deleted.", "pending": "Moved back to waiting."}[status])
}
