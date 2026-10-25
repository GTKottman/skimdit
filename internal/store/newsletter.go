// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Subscriber is one newsletter reader.
type Subscriber struct {
	ID            int64
	Email         string
	Status        string
	Token         string
	ConsentAt     int64
	ConsentSource string
	Bounces       int
	CreatedAt     int64
	UpdatedAt     int64
}

const subCols = `id, email, status, token, COALESCE(consent_at, 0), consent_source, bounces, created_at, updated_at`

func scanSub(r interface{ Scan(...any) error }) (*Subscriber, error) {
	x := &Subscriber{}
	err := r.Scan(&x.ID, &x.Email, &x.Status, &x.Token, &x.ConsentAt, &x.ConsentSource, &x.Bounces, &x.CreatedAt, &x.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return x, err
}

// SubscriberByEmail finds a subscriber.
func (s *Store) SubscriberByEmail(email string) (*Subscriber, error) {
	return scanSub(s.DB.QueryRow(`SELECT `+subCols+` FROM subscribers WHERE email = ?`, strings.TrimSpace(email)))
}

// SubscriberByToken finds a subscriber by the secret in their links.
func (s *Store) SubscriberByToken(token string) (*Subscriber, error) {
	if token == "" {
		return nil, ErrNotFound
	}
	return scanSub(s.DB.QueryRow(`SELECT `+subCols+` FROM subscribers WHERE token = ?`, token))
}

// Subscriber returns one subscriber.
func (s *Store) Subscriber(id int64) (*Subscriber, error) {
	return scanSub(s.DB.QueryRow(`SELECT `+subCols+` FROM subscribers WHERE id = ?`, id))
}

// AddPendingSubscriber creates (or re-opens) a subscription awaiting confirmation.
func (s *Store) AddPendingSubscriber(email, token, source string) (*Subscriber, error) {
	now := s.Unix()
	_, err := s.DB.Exec(`INSERT INTO subscribers (email, status, token, consent_source, created_at, updated_at) VALUES (?, 'pending', ?, ?, ?, ?)
		ON CONFLICT(email) DO UPDATE SET status = CASE WHEN status IN ('active') THEN status ELSE 'pending' END,
		  consent_source = CASE WHEN status IN ('active') THEN consent_source ELSE excluded.consent_source END, updated_at = excluded.updated_at`,
		strings.TrimSpace(email), token, source, now, now)
	if err != nil {
		return nil, err
	}
	return s.SubscriberByEmail(email)
}

// ConfirmSubscriber records consent.
func (s *Store) ConfirmSubscriber(id int64, ipHash string) error {
	now := s.Unix()
	_, err := s.DB.Exec(`UPDATE subscribers SET status = 'active', consent_at = ?, consent_ip_hash = ?, bounces = 0, updated_at = ? WHERE id = ?`, now, ipHash, now, id)
	return err
}

// SetSubscriberStatus changes a subscription (unsubscribed, bounced, complained).
func (s *Store) SetSubscriberStatus(id int64, status string) error {
	_, err := s.DB.Exec(`UPDATE subscribers SET status = ?, updated_at = ? WHERE id = ?`, status, s.Unix(), id)
	return err
}

// AddBounce counts a soft bounce; three in a row turn a subscriber off.
func (s *Store) AddBounce(email string, hard bool) error {
	sub, err := s.SubscriberByEmail(email)
	if err != nil {
		return err
	}
	if hard || sub.Bounces+1 >= 3 {
		return s.SetSubscriberStatus(sub.ID, "bounced")
	}
	_, err = s.DB.Exec(`UPDATE subscribers SET bounces = bounces + 1, updated_at = ? WHERE id = ?`, s.Unix(), sub.ID)
	return err
}

// DeleteSubscriber erases a subscriber and their send history (on request).
func (s *Store) DeleteSubscriber(id int64) error {
	_, err := s.DB.Exec(`DELETE FROM subscribers WHERE id = ?`, id)
	return err
}

// Subscribers lists subscribers, optionally by status and a search term.
func (s *Store) Subscribers(status, q string, limit int) ([]*Subscriber, error) {
	query := `SELECT ` + subCols + ` FROM subscribers WHERE 1=1`
	var args []any
	if status != "" {
		query += ` AND status = ?`
		args = append(args, status)
	}
	if q != "" {
		query += ` AND email LIKE ?`
		args = append(args, "%"+q+"%")
	}
	query += ` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Subscriber
	for rows.Next() {
		x, err := scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SubscriberCounts counts subscribers by status.
func (s *Store) SubscriberCounts() map[string]int {
	out := map[string]int{}
	rows, err := s.DB.Query(`SELECT status, COUNT(*) FROM subscribers GROUP BY status`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if rows.Scan(&st, &n) == nil {
			out[st] = n
		}
	}
	return out
}

// QueueSends creates one send row per active subscriber for a post. It is
// idempotent: a subscriber is never queued twice for the same post.
func (s *Store) QueueSends(post string) (int, error) {
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO newsletter_sends (post, subscriber_id) SELECT ?, id FROM subscribers WHERE status = 'active'`, post)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PendingSend is one email still to send.
type PendingSend struct {
	ID         int64
	Subscriber *Subscriber
}

// NextSends returns up to n unsent emails for a post to still-active subscribers.
func (s *Store) NextSends(post string, n int) ([]*PendingSend, error) {
	rows, err := s.DB.Query(`SELECT ns.id, sb.id, sb.email, sb.status, sb.token, COALESCE(sb.consent_at, 0), sb.consent_source, sb.bounces, sb.created_at, sb.updated_at FROM newsletter_sends ns JOIN subscribers sb ON sb.id = ns.subscriber_id
		WHERE ns.post = ? AND ns.sent_at IS NULL AND ns.error = '' AND sb.status = 'active' ORDER BY ns.id LIMIT ?`, post, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PendingSend
	for rows.Next() {
		p := &PendingSend{Subscriber: &Subscriber{}}
		x := p.Subscriber
		if err := rows.Scan(&p.ID, &x.ID, &x.Email, &x.Status, &x.Token, &x.ConsentAt, &x.ConsentSource, &x.Bounces, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkSent records a delivered (or failed) email.
func (s *Store) MarkSent(id int64, sendErr error) error {
	if sendErr != nil {
		_, err := s.DB.Exec(`UPDATE newsletter_sends SET error = ? WHERE id = ?`, sendErr.Error(), id)
		return err
	}
	_, err := s.DB.Exec(`UPDATE newsletter_sends SET sent_at = ? WHERE id = ?`, s.Unix(), id)
	return err
}

// SendReport summarizes a post's newsletter.
type SendReport struct {
	Post   string
	Sent   int
	Failed int
	Queued int
	Last   int64
}

// SendReports summarizes the newest newsletters.
func (s *Store) SendReports(limit int) ([]*SendReport, error) {
	rows, err := s.DB.Query(`SELECT post, SUM(sent_at IS NOT NULL), SUM(error != ''), SUM(sent_at IS NULL AND error = ''), COALESCE(MAX(sent_at), 0)
		FROM newsletter_sends GROUP BY post ORDER BY MAX(id) DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SendReport
	for rows.Next() {
		r := &SendReport{}
		if err := rows.Scan(&r.Post, &r.Sent, &r.Failed, &r.Queued, &r.Last); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
