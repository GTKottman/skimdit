// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

// Job is one unit of background work.
type Job struct {
	ID        int64
	Kind      string
	Key       string
	Payload   string
	RunAt     int64
	Attempts  int
	LastError string
	Result    string
	DoneAt    int64
	FailedAt  int64
	CreatedAt int64
}

// MaxAttempts is how often a job is tried before it is marked failed.
const MaxAttempts = 8

// Enqueue adds a job unless one with the same key exists. It reports whether
// a new job was added.
func (s *Store) Enqueue(kind, key, payload string, runAt int64) (bool, error) {
	if runAt == 0 {
		runAt = s.Unix()
	}
	res, err := s.DB.Exec(`INSERT OR IGNORE INTO jobs (kind, key, payload, run_at, created_at) VALUES (?, ?, ?, ?, ?)`, kind, key, payload, runAt, s.Unix())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// Claim takes up to limit due jobs and locks them for lease seconds.
func (s *Store) Claim(limit int, lease int64) ([]*Job, error) {
	now := s.Unix()
	var out []*Job
	err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		rows, err := tx.Query(`SELECT id, kind, key, payload, run_at, attempts, last_error FROM jobs
			WHERE done_at IS NULL AND failed_at IS NULL AND run_at <= ? AND locked_until <= ?
			ORDER BY run_at, id LIMIT ?`, now, now, limit)
		if err != nil {
			return err
		}
		for rows.Next() {
			j := &Job{}
			if err := rows.Scan(&j.ID, &j.Kind, &j.Key, &j.Payload, &j.RunAt, &j.Attempts, &j.LastError); err != nil {
				rows.Close()
				return err
			}
			out = append(out, j)
		}
		rows.Close()
		for _, j := range out {
			if _, err := tx.Exec(`UPDATE jobs SET locked_until = ?, attempts = attempts + 1 WHERE id = ?`, now+lease, j.ID); err != nil {
				return err
			}
			j.Attempts++
		}
		return nil
	})
	return out, err
}

// Finish marks a job done with an optional result.
func (s *Store) Finish(id int64, result string) error {
	_, err := s.DB.Exec(`UPDATE jobs SET done_at = ?, result = ?, last_error = '', locked_until = 0 WHERE id = ?`, s.Unix(), result, id)
	return err
}

// Retry records an error and schedules the next try with backoff
// (1, 4, 9, 16... minutes). After MaxAttempts the job is marked failed.
func (s *Store) Retry(j *Job, cause error) error {
	msg := cause.Error()
	if j.Attempts >= MaxAttempts {
		_, err := s.DB.Exec(`UPDATE jobs SET failed_at = ?, last_error = ?, locked_until = 0 WHERE id = ?`, s.Unix(), msg, j.ID)
		return err
	}
	delay := int64(math.Pow(float64(j.Attempts), 2)) * 60
	_, err := s.DB.Exec(`UPDATE jobs SET run_at = ?, last_error = ?, locked_until = 0 WHERE id = ?`, s.Unix()+delay, msg, j.ID)
	return err
}

// JobByKey returns a job by its idempotency key.
func (s *Store) JobByKey(key string) (*Job, error) {
	j := &Job{}
	var done, failed sql.NullInt64
	err := s.DB.QueryRow(`SELECT id, kind, key, payload, run_at, attempts, last_error, result, done_at, failed_at, created_at FROM jobs WHERE key = ?`, key).
		Scan(&j.ID, &j.Kind, &j.Key, &j.Payload, &j.RunAt, &j.Attempts, &j.LastError, &j.Result, &done, &failed, &j.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	j.DoneAt, j.FailedAt = done.Int64, failed.Int64
	return j, err
}

// RecentJobs lists the newest jobs (for the admin's activity view).
func (s *Store) RecentJobs(limit int) ([]*Job, error) {
	rows, err := s.DB.Query(`SELECT id, kind, key, payload, run_at, attempts, last_error, result, COALESCE(done_at, 0), COALESCE(failed_at, 0), created_at FROM jobs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j := &Job{}
		if err := rows.Scan(&j.ID, &j.Kind, &j.Key, &j.Payload, &j.RunAt, &j.Attempts, &j.LastError, &j.Result, &j.DoneAt, &j.FailedAt, &j.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// RequeueFailed gives a failed job another round of attempts.
func (s *Store) RequeueFailed(id int64) error {
	_, err := s.DB.Exec(`UPDATE jobs SET failed_at = NULL, attempts = 0, run_at = ? WHERE id = ? AND failed_at IS NOT NULL`, s.Unix(), id)
	return err
}

// PruneJobs deletes finished jobs older than age seconds, keeping their keys
// meaningful by never deleting jobs that guard one-time sends.
func (s *Store) PruneJobs(age int64) error {
	_, err := s.DB.Exec(`DELETE FROM jobs WHERE done_at IS NOT NULL AND done_at < ? AND kind IN ('rebuild', 'pull')`, s.Unix()-age)
	return err
}
