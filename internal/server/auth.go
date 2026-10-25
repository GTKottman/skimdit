// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/GTKottman/skimdit/internal/store"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	sessionCookie   = "skimdit_session"
	passkeyCookie   = "skimdit_passkey"
	sessionLifetime = 14 * 24 * time.Hour
	// Lockouts: per address, and for password logins from everywhere at once.
	ipFailLimit     = 5
	ipFailWindow    = 15 * time.Minute
	globalFailLimit = 100
)

type ctxKey int

const sessionKey ctxKey = 1

// owner is the single account, as WebAuthn sees it.
type owner struct {
	handle []byte
	name   string
	creds  []webauthn.Credential
}

func (o *owner) WebAuthnID() []byte                         { return o.handle }
func (o *owner) WebAuthnName() string                       { return o.name }
func (o *owner) WebAuthnDisplayName() string                { return o.name }
func (o *owner) WebAuthnCredentials() []webauthn.Credential { return o.creds }

func (s *Server) setupWebAuthn() error {
	origin := s.cfg.BaseURL.Scheme + "://" + s.cfg.BaseURL.Host
	origins := []string{origin}
	if extra := strings.TrimSpace(getenv("SKIMDIT_EXTRA_ORIGINS")); extra != "" {
		origins = append(origins, strings.Split(extra, ",")...)
	}
	rpID := s.cfg.BaseURL.Hostname()
	if v := getenv("SKIMDIT_RP_ID"); v != "" {
		rpID = v
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPDisplayName: s.cfg.Site.Title,
		RPID:          rpID,
		RPOrigins:     origins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return fmt.Errorf("passkeys: %w", err)
	}
	s.wa = wa
	return nil
}

func (s *Server) owner() (*owner, error) {
	c, err := s.store.Credentials()
	if err != nil {
		return nil, err
	}
	o := &owner{handle: c.UserHandle, name: s.cfg.Author.Name}
	if o.name == "" {
		o.name = "owner"
	}
	keys, err := s.store.Passkeys()
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		var cred webauthn.Credential
		if json.Unmarshal([]byte(k.Data), &cred) == nil {
			o.creds = append(o.creds, cred)
		}
	}
	return o, nil
}

// ---- sessions ----

func (s *Server) cookiePath() string { return s.cfg.Rel("/admin/") }

func (s *Server) startSession(w http.ResponseWriter, r *http.Request) error {
	token := randomToken(32)
	se := &store.Session{
		TokenHash: sha256Hex(token),
		CSRF:      randomToken(24),
		ExpiresAt: s.now().Add(sessionLifetime).Unix(),
		IP:        s.clientIP(r),
		UserAgent: truncate(r.UserAgent(), 200),
	}
	if err := s.store.CreateSession(se); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: s.cookiePath(), HttpOnly: true,
		Secure: s.isHTTPS(r), SameSite: http.SameSiteLaxMode, MaxAge: int(sessionLifetime.Seconds()),
	})
	return nil
}

func (s *Server) session(r *http.Request) *store.Session {
	c, err := r.Cookie(sessionCookie)
	if err != nil || len(c.Value) < 20 {
		return nil
	}
	se, err := s.store.Session(sha256Hex(c.Value))
	if err != nil {
		return nil
	}
	// Keep active sessions alive; extend at most once an hour.
	if remaining := time.Until(time.Unix(se.ExpiresAt, 0)); remaining < sessionLifetime-time.Hour {
		_ = s.store.ExtendSession(se.TokenHash, s.now().Add(sessionLifetime).Unix())
	}
	return se
}

func (s *Server) endSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.store.DeleteSession(sha256Hex(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: s.cookiePath(), MaxAge: -1, HttpOnly: true, Secure: s.isHTTPS(r)})
}

// checkCSRF accepts a state-changing request only from our own pages: the
// Origin must match and the session's token must be present.
func (s *Server) checkCSRF(r *http.Request, se *store.Session) bool {
	if !s.sameOrigin(r) {
		return false
	}
	tok := r.Header.Get("X-CSRF-Token")
	if tok == "" {
		tok = r.FormValue("csrf")
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(se.CSRF)) == 1
}

// ---- lockout ----

func (s *Server) loginBlocked(ip string) (bool, string) {
	since := s.now().Add(-ipFailWindow).Unix()
	if n, _ := s.store.FailedLogins(ip, since); n >= ipFailLimit {
		return true, "Too many failed attempts from your address. Wait 15 minutes, or sign in with a passkey."
	}
	// Addresses the owner signed in from recently are never caught by the
	// site-wide pause, so an attacker cannot lock the owner out.
	if !s.store.RecentSuccess(ip, s.now().Add(-30*24*time.Hour).Unix()) {
		if n, _ := s.store.FailedLogins("", s.now().Add(-time.Hour).Unix()); n >= globalFailLimit {
			return true, "Password sign-in is paused because of many failed attempts. Use a passkey, or wait an hour."
		}
	}
	if !s.loginLimit.allow(ip, s.now()) {
		return true, "Too many attempts. Wait a few minutes."
	}
	return false, ""
}

func failDelay() { time.Sleep(400*time.Millisecond + time.Duration(rand.IntN(400))*time.Millisecond) }

// passwordLogin checks a password (and the TOTP code when enabled).
func (s *Server) passwordLogin(r *http.Request, password, code string) error {
	ip := limitKey(s.clientIP(r))
	if blocked, msg := s.loginBlocked(ip); blocked {
		return errors.New(msg)
	}
	c, err := s.store.Credentials()
	if err != nil {
		return errors.New("this site has no account yet")
	}
	ok := CheckPassword(password, c.PasswordHash)
	if ok && c.TOTPSecret != "" {
		ok = totp.Validate(strings.ReplaceAll(code, " ", ""), c.TOTPSecret)
	}
	_ = s.store.RecordLogin(ip, ok)
	if !ok {
		failDelay()
		if c.TOTPSecret != "" {
			return errors.New("The password or the 6-digit code is wrong.")
		}
		return errors.New("The password is wrong.")
	}
	return nil
}

// ---- setup ----

func (s *Server) completeSetup(token, password, confirm string) error {
	if s.setupToken == "" {
		return errors.New("the account already exists")
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.setupToken)) != 1 {
		return errors.New("this setup link is not valid; use the one printed in the server log")
	}
	if password != confirm {
		return errors.New("the two passwords differ")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.store.SetPassword(hash); err != nil {
		return err
	}
	s.setupToken = ""
	return nil
}

// ---- TOTP ----

func (s *Server) newTOTP() (*otp.Key, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: s.cfg.Site.Title, AccountName: s.cfg.Author.Name + " (" + s.cfg.Domain() + ")"})
	if err != nil {
		return nil, "", err
	}
	img, err := key.Image(220, 220)
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", err
	}
	return key, "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// ---- passkeys ----

func (s *Server) passkeyBegin(w http.ResponseWriter, r *http.Request, se *store.Session) {
	o, err := s.owner()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var exclude []protocol.CredentialDescriptor
	for _, c := range o.creds {
		exclude = append(exclude, c.Descriptor())
	}
	opts, data, err := s.wa.BeginRegistration(o, webauthn.WithExclusions(exclude), webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.waMu.Lock()
	s.waSessions["reg:"+se.TokenHash] = data
	s.waMu.Unlock()
	writeJSON(w, http.StatusOK, opts)
}

func (s *Server) passkeyFinish(w http.ResponseWriter, r *http.Request, se *store.Session) {
	s.waMu.Lock()
	data := s.waSessions["reg:"+se.TokenHash]
	delete(s.waSessions, "reg:"+se.TokenHash)
	s.waMu.Unlock()
	if data == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start again"})
		return
	}
	o, err := s.owner()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	cred, err := s.wa.FinishRegistration(o, *data, r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the passkey was not accepted: " + protocolError(err)})
		return
	}
	b, _ := json.Marshal(cred)
	name := truncate(strings.TrimSpace(r.URL.Query().Get("name")), 60)
	if name == "" {
		name = "Passkey added " + s.now().In(s.cfg.Location).Format("Jan 2, 2006")
	}
	if err := s.store.SavePasskey(&store.Passkey{ID: cred.ID, Name: name, Data: string(b)}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "saved"})
}

func (s *Server) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	o, err := s.owner()
	if err != nil || len(o.creds) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no passkeys are set up; sign in with your password"})
		return
	}
	if !s.loginLimit.allow(limitKey(s.clientIP(r)), s.now()) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts; wait a few minutes"})
		return
	}
	opts, data, err := s.wa.BeginLogin(o)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	id := randomToken(18)
	s.waMu.Lock()
	s.waSessions["login:"+id] = data
	s.waMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: passkeyCookie, Value: id, Path: s.cookiePath(), HttpOnly: true, Secure: s.isHTTPS(r), SameSite: http.SameSiteStrictMode, MaxAge: 300})
	writeJSON(w, http.StatusOK, opts)
}

func (s *Server) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(passkeyCookie)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start again"})
		return
	}
	s.waMu.Lock()
	data := s.waSessions["login:"+c.Value]
	delete(s.waSessions, "login:"+c.Value)
	s.waMu.Unlock()
	if data == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "start again"})
		return
	}
	o, err := s.owner()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	cred, err := s.wa.FinishLogin(o, *data, r)
	ip := limitKey(s.clientIP(r))
	if err != nil {
		_ = s.store.RecordLogin(ip, false)
		failDelay()
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "the passkey was not accepted: " + protocolError(err)})
		return
	}
	if cred.Authenticator.CloneWarning {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "this passkey looks cloned; remove it in Settings and add a new one"})
		return
	}
	b, _ := json.Marshal(cred)
	_ = s.store.SavePasskey(&store.Passkey{ID: cred.ID, Data: string(b), LastUsed: s.store.Unix()})
	_ = s.store.RecordLogin(ip, true)
	if err := s.startSession(w, r); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "signed in"})
}

func protocolError(err error) string {
	var pe *protocol.Error
	if errors.As(err, &pe) && pe.DevInfo != "" {
		return pe.DevInfo
	}
	return err.Error()
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
