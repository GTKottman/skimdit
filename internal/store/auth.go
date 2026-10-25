// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
)

// Credentials is the single owner account.
type Credentials struct {
	PasswordHash string
	TOTPSecret   string
	UserHandle   []byte
}

// Credentials returns the owner account, or ErrNotFound before setup.
func (s *Store) Credentials() (*Credentials, error) {
	c := &Credentials{}
	err := s.DB.QueryRow(`SELECT password_hash, totp_secret, user_handle FROM credentials WHERE id = 1`).Scan(&c.PasswordHash, &c.TOTPSecret, &c.UserHandle)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

// SetPassword stores a password hash, creating the account if needed.
func (s *Store) SetPassword(hash string) error {
	handle := make([]byte, 32)
	_, _ = rand.Read(handle)
	_, err := s.DB.Exec(`INSERT INTO credentials (id, password_hash, user_handle, updated_at) VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET password_hash = excluded.password_hash, updated_at = excluded.updated_at`, hash, handle, s.Unix())
	return err
}

// SetTOTP stores (or clears, with "") the TOTP secret.
func (s *Store) SetTOTP(secret string) error {
	_, err := s.DB.Exec(`UPDATE credentials SET totp_secret = ?, updated_at = ? WHERE id = 1`, secret, s.Unix())
	return err
}

// Passkey is a stored WebAuthn credential.
type Passkey struct {
	ID        []byte
	Name      string
	Data      string
	CreatedAt int64
	LastUsed  int64
}

// Passkeys lists the owner's passkeys.
func (s *Store) Passkeys() ([]*Passkey, error) {
	rows, err := s.DB.Query(`SELECT id, name, data, created_at, COALESCE(last_used, 0) FROM passkeys ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Passkey
	for rows.Next() {
		p := &Passkey{}
		if err := rows.Scan(&p.ID, &p.Name, &p.Data, &p.CreatedAt, &p.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SavePasskey adds or updates a passkey.
func (s *Store) SavePasskey(p *Passkey) error {
	_, err := s.DB.Exec(`INSERT INTO passkeys (id, name, data, created_at, last_used) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET data = excluded.data, last_used = excluded.last_used`, p.ID, p.Name, p.Data, s.Unix(), nullable(p.LastUsed))
	return err
}

// DeletePasskey removes a passkey.
func (s *Store) DeletePasskey(id []byte) error {
	_, err := s.DB.Exec(`DELETE FROM passkeys WHERE id = ?`, id)
	return err
}

func nullable(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// Session is a logged-in browser.
type Session struct {
	TokenHash string
	CSRF      string
	CreatedAt int64
	ExpiresAt int64
	IP        string
	UserAgent string
}

// CreateSession stores a session.
func (s *Store) CreateSession(se *Session) error {
	_, err := s.DB.Exec(`INSERT INTO sessions (token_hash, csrf, created_at, expires_at, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?)`,
		se.TokenHash, se.CSRF, s.Unix(), se.ExpiresAt, se.IP, se.UserAgent)
	return err
}

// Session finds a live session.
func (s *Store) Session(tokenHash string) (*Session, error) {
	se := &Session{}
	err := s.DB.QueryRow(`SELECT token_hash, csrf, created_at, expires_at, ip, user_agent FROM sessions WHERE token_hash = ? AND expires_at > ?`, tokenHash, s.Unix()).
		Scan(&se.TokenHash, &se.CSRF, &se.CreatedAt, &se.ExpiresAt, &se.IP, &se.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return se, err
}

// ExtendSession pushes a session's expiry forward.
func (s *Store) ExtendSession(tokenHash string, until int64) error {
	_, err := s.DB.Exec(`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, until, tokenHash)
	return err
}

// Sessions lists live sessions.
func (s *Store) Sessions() ([]*Session, error) {
	rows, err := s.DB.Query(`SELECT token_hash, csrf, created_at, expires_at, ip, user_agent FROM sessions WHERE expires_at > ? ORDER BY created_at DESC`, s.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		se := &Session{}
		if err := rows.Scan(&se.TokenHash, &se.CSRF, &se.CreatedAt, &se.ExpiresAt, &se.IP, &se.UserAgent); err != nil {
			return nil, err
		}
		out = append(out, se)
	}
	return out, rows.Err()
}

// DeleteSession logs one session out.
func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.DB.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteSessions logs every session out (after a password change).
func (s *Store) DeleteSessions() error {
	_, err := s.DB.Exec(`DELETE FROM sessions`)
	return err
}

// RecordLogin records a login attempt.
func (s *Store) RecordLogin(ip string, ok bool) error {
	v := 0
	if ok {
		v = 1
	}
	_, err := s.DB.Exec(`INSERT INTO login_attempts (ip, ok, created_at) VALUES (?, ?, ?)`, ip, v, s.Unix())
	return err
}

// FailedLogins counts failures since a time, for one IP ("" = all IPs).
func (s *Store) FailedLogins(ip string, since int64) (int, error) {
	var n int
	var err error
	if ip == "" {
		err = s.DB.QueryRow(`SELECT COUNT(*) FROM login_attempts WHERE ok = 0 AND created_at >= ?`, since).Scan(&n)
	} else {
		err = s.DB.QueryRow(`SELECT COUNT(*) FROM login_attempts WHERE ok = 0 AND ip = ? AND created_at >= ?`, ip, since).Scan(&n)
	}
	return n, err
}

// Cleanup removes expired sessions and old login attempts.
func (s *Store) Cleanup() error {
	now := s.Unix()
	if _, err := s.DB.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now); err != nil {
		return err
	}
	_, err := s.DB.Exec(`DELETE FROM login_attempts WHERE created_at < ?`, now-30*86400)
	return err
}
