// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// ---- passwords (argon2id) ----

const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
	// MinPasswordLength is the shortest password the owner may set.
	MinPasswordLength = 12
)

// HashPassword returns an encoded argon2id hash.
func HashPassword(pw string) (string, error) {
	if len([]rune(pw)) < MinPasswordLength {
		return "", fmt.Errorf("use at least %d characters", MinPasswordLength)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// argonSem bounds concurrent hashing: each one holds 64 MiB.
var argonSem = make(chan struct{}, 2)

// CheckPassword compares a password with an encoded hash in constant time.
func CheckPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || m > 1<<20 || t > 10 {
		return false
	}
	argonSem <- struct{}{}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	<-argonSem
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---- tokens ----

// randomToken returns n random bytes, URL-safe encoded.
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Signer makes and checks HMAC-signed, expiring tokens (proof-of-work
// challenges, email links). The key comes from SKIMDIT_SECRET.
type Signer struct{ key []byte }

// NewSigner derives a signer for one purpose from the server secret.
func NewSigner(secret, purpose string) *Signer {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte("skimdit:" + purpose))
	return &Signer{key: m.Sum(nil)}
}

// Sign returns "value.expiry.signature".
func (s *Signer) Sign(value string, ttl time.Duration, now time.Time) string {
	exp := strconv.FormatInt(now.Add(ttl).Unix(), 10)
	return value + "." + exp + "." + s.mac(value+"."+exp)
}

// Verify checks a token and returns its value.
func (s *Signer) Verify(token string, now time.Time) (string, error) {
	i := strings.LastIndexByte(token, '.')
	if i < 0 {
		return "", errors.New("malformed token")
	}
	body, sig := token[:i], token[i+1:]
	if !hmac.Equal([]byte(sig), []byte(s.mac(body))) {
		return "", errors.New("bad signature")
	}
	j := strings.LastIndexByte(body, '.')
	if j < 0 {
		return "", errors.New("malformed token")
	}
	exp, err := strconv.ParseInt(body[j+1:], 10, 64)
	if err != nil || now.Unix() > exp {
		return "", errors.New("expired")
	}
	return body[:j], nil
}

func (s *Signer) mac(v string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(v))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// ---- client IP ----

// clientIP is the requester's IP: the socket address, or the right-most
// untrusted X-Forwarded-For entry when the server sits behind a proxy.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if s.cfg.Server.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	return host
}

// ipHash is a daily-rotating, salted hash of an IP, used only for rate
// limits and spam checks. The IP itself is never stored.
func (s *Server) ipHash(ip string) string {
	day := s.now().UTC().Format("2006-01-02")
	return sha256Hex(s.secret + "|ip|" + day + "|" + ip)[:24]
}

func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.cfg.Server.TrustProxy && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// sameOrigin rejects cross-site form posts to authenticated endpoints.
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		ref := r.Header.Get("Referer")
		if ref == "" {
			return r.Header.Get("Sec-Fetch-Site") == "" || r.Header.Get("Sec-Fetch-Site") == "same-origin"
		}
		origin = ref
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// ---- rate limiting ----

// limiter is a fixed-window counter per key, held in memory.
type limiter struct {
	mu      sync.Mutex
	window  time.Duration
	max     int
	buckets map[string]*bucket
	last    time.Time
}

type bucket struct {
	start time.Time
	n     int
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{window: window, max: max, buckets: map[string]*bucket{}}
}

// allow counts one event for key and reports whether it is within the limit.
func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.last) > l.window {
		for k, b := range l.buckets {
			if now.Sub(b.start) > l.window {
				delete(l.buckets, k)
			}
		}
		l.last = now
	}
	b := l.buckets[key]
	if b == nil || now.Sub(b.start) > l.window {
		b = &bucket{start: now}
		l.buckets[key] = b
	}
	b.n++
	return b.n <= l.max
}

// ---- headers ----

// readerCSP is the Content-Security-Policy for public pages. Styles allow
// inline attributes because KaTeX positions math with them; scripts never
// run inline.
const readerCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; " +
	"connect-src 'self'; media-src 'self'; frame-src https://www.youtube-nocookie.com; object-src 'none'; base-uri 'self'; " +
	"form-action 'self'; frame-ancestors 'none'; upgrade-insecure-requests"

const adminCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; " +
	"connect-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'self'"

func (s *Server) securityHeaders(w http.ResponseWriter, r *http.Request, admin bool) {
	h := w.Header()
	if admin {
		h.Set("Content-Security-Policy", adminCSP)
		h.Set("Cache-Control", "no-store")
		h.Set("X-Robots-Tag", "noindex, nofollow")
	} else {
		h.Set("Content-Security-Policy", readerCSP)
	}
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	if s.isHTTPS(r) {
		h.Set("Strict-Transport-Security", "max-age=31536000")
	}
	if os.Getenv("SKIMDIT_STAGING") == "1" {
		h.Set("X-Robots-Tag", "noindex, nofollow")
	}
}

// limitKey groups an address for rate limits: one IPv4 address, or one
// IPv6 /64 (a single customer usually gets a whole /64).
func limitKey(ip string) string {
	p := net.ParseIP(ip)
	if p == nil || p.To4() != nil {
		return ip
	}
	return p.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
