// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
)

// Draft is an unpublished post or page, or an edit of a published one.
type Draft struct {
	ID        int64
	Kind      string
	Source    string
	Fields    string // JSON
	Body      string
	PublishAt int64 // 0 = not scheduled
	CreatedAt int64
	UpdatedAt int64
}

// Revision is a saved version of a draft.
type Revision struct {
	ID        int64
	DraftID   int64
	Fields    string
	Body      string
	CreatedAt int64
}

// RevisionInterval is the minimum gap between automatic revisions.
const RevisionInterval = 60

const draftCols = `id, kind, source, fields, body, COALESCE(publish_at, 0), created_at, updated_at`

func scanDraft(r interface{ Scan(...any) error }) (*Draft, error) {
	d := &Draft{}
	err := r.Scan(&d.ID, &d.Kind, &d.Source, &d.Fields, &d.Body, &d.PublishAt, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// CreateDraft adds a draft.
func (s *Store) CreateDraft(kind, source, fields, body string) (*Draft, error) {
	now := s.Unix()
	res, err := s.DB.Exec(`INSERT INTO drafts (kind, source, fields, body, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, kind, source, fields, body, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if _, err := s.DB.Exec(`INSERT INTO draft_revisions (draft_id, fields, body, created_at) VALUES (?, ?, ?, ?)`, id, fields, body, now); err != nil {
		return nil, err
	}
	return s.Draft(id)
}

// Draft returns one draft.
func (s *Store) Draft(id int64) (*Draft, error) {
	return scanDraft(s.DB.QueryRow(`SELECT `+draftCols+` FROM drafts WHERE id = ?`, id))
}

// DraftForSource returns the open draft that edits a published file, if any.
func (s *Store) DraftForSource(source string) (*Draft, error) {
	return scanDraft(s.DB.QueryRow(`SELECT `+draftCols+` FROM drafts WHERE source = ? AND source != '' ORDER BY id LIMIT 1`, source))
}

// Drafts lists drafts, most recently edited first.
func (s *Store) Drafts() ([]*Draft, error) {
	rows, err := s.DB.Query(`SELECT ` + draftCols + ` FROM drafts ORDER BY updated_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Draft
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SaveDraft updates a draft. A revision is kept at most once a minute (and
// always when force is set), so autosave never floods the history.
func (s *Store) SaveDraft(id int64, fields, body string, force bool) error {
	now := s.Unix()
	return s.Tx(context.Background(), func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE drafts SET fields = ?, body = ?, updated_at = ? WHERE id = ?`, fields, body, now, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		var last int64
		var lastFields, lastBody string
		err = tx.QueryRow(`SELECT created_at, fields, body FROM draft_revisions WHERE draft_id = ? ORDER BY id DESC LIMIT 1`, id).Scan(&last, &lastFields, &lastBody)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if lastFields == fields && lastBody == body {
			return nil
		}
		if force || now-last >= RevisionInterval {
			_, err = tx.Exec(`INSERT INTO draft_revisions (draft_id, fields, body, created_at) VALUES (?, ?, ?, ?)`, id, fields, body, now)
		}
		return err
	})
}

// ScheduleDraft sets (or clears, with 0) a draft's publish time.
func (s *Store) ScheduleDraft(id, at int64) error {
	var v any
	if at > 0 {
		v = at
	}
	_, err := s.DB.Exec(`UPDATE drafts SET publish_at = ? WHERE id = ?`, v, id)
	return err
}

// Revisions lists a draft's revisions, newest first.
func (s *Store) Revisions(id int64) ([]*Revision, error) {
	rows, err := s.DB.Query(`SELECT id, draft_id, fields, body, created_at FROM draft_revisions WHERE draft_id = ? ORDER BY id DESC LIMIT 200`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Revision
	for rows.Next() {
		r := &Revision{}
		if err := rows.Scan(&r.ID, &r.DraftID, &r.Fields, &r.Body, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Revision returns one revision.
func (s *Store) Revision(id int64) (*Revision, error) {
	r := &Revision{}
	err := s.DB.QueryRow(`SELECT id, draft_id, fields, body, created_at FROM draft_revisions WHERE id = ?`, id).Scan(&r.ID, &r.DraftID, &r.Fields, &r.Body, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

// DeleteDraft removes a draft and its revisions.
func (s *Store) DeleteDraft(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM drafts WHERE id = ?`, id)
	return err
}

// DueDrafts lists drafts whose scheduled time has come.
func (s *Store) DueDrafts() ([]*Draft, error) {
	rows, err := s.DB.Query(`SELECT `+draftCols+` FROM drafts WHERE publish_at IS NOT NULL AND publish_at <= ? ORDER BY publish_at`, s.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Draft
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
