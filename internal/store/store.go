// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store is Skimdit's SQLite database: drafts, comments,
// subscribers, stats, sessions and the job queue. Posts never live here;
// they are files in git.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store wraps the database.
type Store struct {
	DB   *sql.DB
	path string
	now  func() time.Time
}

// FileName is the database file inside the data folder.
const FileName = "skimdit.db"

// Open opens (or creates) the database in dir and applies migrations,
// backing the file up first when a migration is pending.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, FileName)
	dsn := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{DB: db, path: path, now: time.Now}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// Now returns the store's clock (replaceable in tests).
func (s *Store) Now() time.Time { return s.now() }

// SetClock replaces the clock (tests).
func (s *Store) SetClock(f func() time.Time) { s.now = f }

// Unix is the current time in Unix seconds.
func (s *Store) Unix() int64 { return s.now().Unix() }

func (s *Store) migrate() error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return err
	}
	applied := map[int]bool{}
	rows, err := s.DB.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	type mig struct {
		v    int
		name string
	}
	var pending []mig
	for _, n := range names {
		v, err := strconv.Atoi(strings.SplitN(filepath.Base(n), "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: bad name", n)
		}
		if !applied[v] {
			pending = append(pending, mig{v, n})
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if len(applied) > 0 {
		if err := s.Backup(fmt.Sprintf("pre-migration-%d", pending[0].v)); err != nil {
			return fmt.Errorf("backup before migration: %w", err)
		}
	}
	for _, m := range pending {
		sqlText, err := migrations.ReadFile(m.name)
		if err != nil {
			return err
		}
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(sqlText)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, m.v, s.Unix()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Backup writes a consistent copy of the database to <dir>/backups/<label>-<time>.db.
func (s *Store) Backup(label string) error {
	dir := filepath.Join(filepath.Dir(s.path), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	dst := filepath.Join(dir, fmt.Sprintf("%s-%s.db", label, s.now().UTC().Format("20060102-150405")))
	_, err := s.DB.Exec(`VACUUM INTO ?`, dst)
	return err
}

// Version is the newest applied migration.
func (s *Store) Version() (int, error) {
	var v sql.NullInt64
	err := s.DB.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	return int(v.Int64), err
}

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Tx runs f in a transaction.
func (s *Store) Tx(ctx context.Context, f func(*sql.Tx) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := f(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
