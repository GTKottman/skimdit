// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"database/sql"
	"errors"
)

// Comment is a reader's comment on a post.
type Comment struct {
	ID          int64
	Post        string
	ParentID    int64
	Author      string
	Email       string
	URL         string
	Body        string
	HTML        string
	Status      string
	Notify      int // 0 off, 1 asked (awaiting confirmation), 2 confirmed
	NotifyToken string
	IPHash      string
	CreatedAt   int64
}

const commentCols = `id, post, COALESCE(parent_id, 0), author, email, url, body, html, status, notify, notify_token, ip_hash, created_at`

func scanComment(r interface{ Scan(...any) error }) (*Comment, error) {
	c := &Comment{}
	err := r.Scan(&c.ID, &c.Post, &c.ParentID, &c.Author, &c.Email, &c.URL, &c.Body, &c.HTML, &c.Status, &c.Notify, &c.NotifyToken, &c.IPHash, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// AddComment stores a new comment.
func (s *Store) AddComment(c *Comment) (int64, error) {
	var parent any
	if c.ParentID > 0 {
		parent = c.ParentID
	}
	res, err := s.DB.Exec(`INSERT INTO comments (post, parent_id, author, email, url, body, html, status, notify, notify_token, ip_hash, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, c.Post, parent, c.Author, c.Email, c.URL, c.Body, c.HTML, c.Status, c.Notify, c.NotifyToken, c.IPHash, s.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Comment returns one comment.
func (s *Store) Comment(id int64) (*Comment, error) {
	return scanComment(s.DB.QueryRow(`SELECT `+commentCols+` FROM comments WHERE id = ?`, id))
}

// CommentByToken finds a comment by its notification token.
func (s *Store) CommentByToken(token string) (*Comment, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	return scanComment(s.DB.QueryRow(`SELECT `+commentCols+` FROM comments WHERE notify_token = ?`, token))
}

// Comments lists comments with a status ("" = all except deleted), newest first.
func (s *Store) Comments(status string, limit int) ([]*Comment, error) {
	q := `SELECT ` + commentCols + ` FROM comments WHERE status != 'deleted'`
	args := []any{}
	if status != "" {
		q = `SELECT ` + commentCols + ` FROM comments WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	return s.queryComments(q, args...)
}

// ApprovedComments lists a post's approved comments, oldest first.
func (s *Store) ApprovedComments(post string) ([]*Comment, error) {
	return s.queryComments(`SELECT `+commentCols+` FROM comments WHERE post = ? AND status = 'approved' ORDER BY created_at, id`, post)
}

func (s *Store) queryComments(q string, args ...any) ([]*Comment, error) {
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Comment
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetCommentStatus moderates a comment.
func (s *Store) SetCommentStatus(id int64, status string) error {
	_, err := s.DB.Exec(`UPDATE comments SET status = ? WHERE id = ?`, status, id)
	return err
}

// SetCommentNotify changes a comment's reply-notification state.
func (s *Store) SetCommentNotify(id int64, notify int) error {
	_, err := s.DB.Exec(`UPDATE comments SET notify = ? WHERE id = ?`, notify, id)
	return err
}

// CountComments counts comments with a status.
func (s *Store) CountComments(status string) int {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM comments WHERE status = ?`, status).Scan(&n)
	return n
}

// RecentCommentsFrom counts comments from an IP hash since a time (spam limits).
func (s *Store) RecentCommentsFrom(ipHash string, since int64) int {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM comments WHERE ip_hash = ? AND created_at >= ?`, ipHash, since).Scan(&n)
	return n
}

// ForgetCommentIPs clears IP hashes older than a time: they are only kept
// for rate limits.
func (s *Store) ForgetCommentIPs(before int64) error {
	_, err := s.DB.Exec(`UPDATE comments SET ip_hash = '' WHERE created_at < ? AND ip_hash != ''`, before)
	return err
}

// Webmention is a mention of a post from another site.
type Webmention struct {
	ID         int64
	Source     string
	Target     string
	Post       string
	Type       string
	Author     string
	AuthorURL  string
	HTML       string
	Status     string
	Published  int64
	VerifiedAt int64
	CreatedAt  int64
}

const wmCols = `id, source, target, post, type, author, author_url, html, status, COALESCE(published, 0), COALESCE(verified_at, 0), created_at`

func scanWM(r interface{ Scan(...any) error }) (*Webmention, error) {
	w := &Webmention{}
	err := r.Scan(&w.ID, &w.Source, &w.Target, &w.Post, &w.Type, &w.Author, &w.AuthorURL, &w.HTML, &w.Status, &w.Published, &w.VerifiedAt, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return w, err
}

// UpsertWebmention records an incoming webmention for (re)verification and
// returns its id. A known one keeps its moderation status.
func (s *Store) UpsertWebmention(source, target, post string) (int64, error) {
	_, err := s.DB.Exec(`INSERT INTO webmentions (source, target, post, status, created_at) VALUES (?, ?, ?, 'verifying', ?)
		ON CONFLICT(source, target) DO NOTHING`, source, target, post, s.Unix())
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.DB.QueryRow(`SELECT id FROM webmentions WHERE source = ? AND target = ?`, source, target).Scan(&id)
	return id, err
}

// Webmention returns one webmention.
func (s *Store) Webmention(id int64) (*Webmention, error) {
	return scanWM(s.DB.QueryRow(`SELECT `+wmCols+` FROM webmentions WHERE id = ?`, id))
}

// SaveWebmention stores verification results.
func (s *Store) SaveWebmention(w *Webmention) error {
	_, err := s.DB.Exec(`UPDATE webmentions SET type = ?, author = ?, author_url = ?, html = ?, status = ?, published = ?, verified_at = ? WHERE id = ?`,
		w.Type, w.Author, w.AuthorURL, w.HTML, w.Status, nullable(w.Published), s.Unix(), w.ID)
	return err
}

// SetWebmentionStatus moderates a webmention.
func (s *Store) SetWebmentionStatus(id int64, status string) error {
	_, err := s.DB.Exec(`UPDATE webmentions SET status = ? WHERE id = ?`, status, id)
	return err
}

// Webmentions lists webmentions with a status, newest first.
func (s *Store) Webmentions(status string, limit int) ([]*Webmention, error) {
	return s.queryWM(`SELECT `+wmCols+` FROM webmentions WHERE status = ? ORDER BY created_at DESC LIMIT ?`, status, limit)
}

// ApprovedWebmentions lists a post's approved webmentions, oldest first.
func (s *Store) ApprovedWebmentions(post string) ([]*Webmention, error) {
	return s.queryWM(`SELECT `+wmCols+` FROM webmentions WHERE post = ? AND status = 'approved' ORDER BY COALESCE(published, created_at)`, post)
}

func (s *Store) queryWM(q string, args ...any) ([]*Webmention, error) {
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Webmention
	for rows.Next() {
		w, err := scanWM(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// CountWebmentions counts webmentions with a status.
func (s *Store) CountWebmentions(status string) int {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM webmentions WHERE status = ?`, status).Scan(&n)
	return n
}
