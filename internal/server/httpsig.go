// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTP Signatures (draft-cavage-http-signatures-12, rsa-sha256), as used by
// Mastodon and the rest of the fediverse.

// apKey returns the site's RSA key, creating it on first use.
func (s *Server) apKey() (*rsa.PrivateKey, string, error) {
	var priv, pub string
	err := s.store.DB.QueryRow(`SELECT private_pem, public_pem FROM ap_keys WHERE id = 1`).Scan(&priv, &pub)
	if err != nil {
		k, gerr := rsa.GenerateKey(rand.Reader, 2048)
		if gerr != nil {
			return nil, "", gerr
		}
		privDER := x509.MarshalPKCS1PrivateKey(k)
		pubDER, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
		priv = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privDER}))
		pub = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))
		if _, err := s.store.DB.Exec(`INSERT OR IGNORE INTO ap_keys (id, private_pem, public_pem, created_at) VALUES (1, ?, ?, ?)`, priv, pub, s.store.Unix()); err != nil {
			return nil, "", err
		}
		// Another request may have won the race; read back what is stored.
		if err := s.store.DB.QueryRow(`SELECT private_pem, public_pem FROM ap_keys WHERE id = 1`).Scan(&priv, &pub); err != nil {
			return nil, "", err
		}
	}
	block, _ := pem.Decode([]byte(priv))
	if block == nil {
		return nil, "", errors.New("stored ActivityPub key is damaged")
	}
	k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	return k, pub, err
}

func digestHeader(body []byte) string {
	sum := sha256.Sum256(body)
	return "SHA-256=" + base64.StdEncoding.EncodeToString(sum[:])
}

// signRequest signs an outgoing request with the site actor's key.
func (s *Server) signRequest(req *http.Request, body []byte) error {
	key, _, err := s.apKey()
	if err != nil {
		return err
	}
	req.Header.Set("Date", s.now().UTC().Format(http.TimeFormat))
	req.Header.Set("Host", req.URL.Host)
	headers := []string{"(request-target)", "host", "date"}
	if body != nil {
		req.Header.Set("Digest", digestHeader(body))
		headers = append(headers, "digest")
	}
	signing := signingString(req, headers)
	h := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		return err
	}
	req.Header.Set("Signature", fmt.Sprintf(`keyId="%s",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		s.actorID()+"#main-key", strings.Join(headers, " "), base64.StdEncoding.EncodeToString(sig)))
	return nil
}

func signingString(r *http.Request, headers []string) string {
	var b strings.Builder
	for i, h := range headers {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch h {
		case "(request-target)":
			fmt.Fprintf(&b, "(request-target): %s %s", strings.ToLower(r.Method), r.URL.RequestURI())
		case "host":
			host := r.Host
			if host == "" {
				host = r.URL.Host
			}
			fmt.Fprintf(&b, "host: %s", host)
		default:
			fmt.Fprintf(&b, "%s: %s", h, strings.TrimSpace(r.Header.Get(h)))
		}
	}
	return b.String()
}

type sigParams struct {
	keyID     string
	headers   []string
	signature []byte
}

func parseSignature(h string) (*sigParams, error) {
	p := &sigParams{headers: []string{"date"}}
	for _, part := range splitParams(h) {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		switch strings.TrimSpace(k) {
		case "keyId":
			p.keyID = v
		case "headers":
			p.headers = strings.Fields(strings.ToLower(v))
		case "signature":
			sig, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				return nil, errors.New("signature is not base64")
			}
			p.signature = sig
		case "algorithm":
			if v != "rsa-sha256" && v != "hs2019" {
				return nil, fmt.Errorf("unsupported signature algorithm %q", v)
			}
		}
	}
	if p.keyID == "" || p.signature == nil {
		return nil, errors.New("signature header is incomplete")
	}
	return p, nil
}

// splitParams splits on commas outside quotes.
func splitParams(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case r == ',' && !quoted:
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	return out
}

// verifyRequest checks an incoming signed request and returns the signing
// actor's key id. The body digest, date window and signed headers are all
// checked; the key is fetched from the sender (and cached).
func (s *Server) verifyRequest(r *http.Request, body []byte) (string, error) {
	p, err := parseSignature(r.Header.Get("Signature"))
	if err != nil {
		return "", err
	}
	must := map[string]bool{"(request-target)": false, "host": false, "date": false}
	if r.Method == http.MethodPost {
		must["digest"] = false
	}
	for _, h := range p.headers {
		if _, ok := must[h]; ok {
			must[h] = true
		}
	}
	for h, ok := range must {
		if !ok {
			return "", fmt.Errorf("the signature must cover %s", h)
		}
	}
	d, err := http.ParseTime(r.Header.Get("Date"))
	if err != nil || s.now().Sub(d).Abs() > 12*time.Hour {
		return "", errors.New("date is missing or too far from now")
	}
	if body != nil && r.Header.Get("Digest") != digestHeader(body) {
		return "", errors.New("body digest does not match")
	}
	pub, err := s.actorKey(r.Context(), p.keyID)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256([]byte(signingString(r, p.headers)))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], p.signature); err != nil {
		// The actor may have rotated its key: refetch once.
		s.forgetActorKey(p.keyID)
		if pub, err2 := s.actorKey(r.Context(), p.keyID); err2 == nil && rsa.VerifyPKCS1v15(pub, crypto.SHA256, h[:], p.signature) == nil {
			return p.keyID, nil
		}
		return "", errors.New("signature does not verify")
	}
	return p.keyID, nil
}

func parsePublicKey(pemText string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, errors.New("no PEM key")
	}
	if k, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PublicKey); ok {
			return rk, nil
		}
		return nil, errors.New("only RSA keys are supported")
	}
	return x509.ParsePKCS1PublicKey(block.Bytes)
}

// signedPost delivers an activity to an inbox.
func (s *Server) signedPost(r io.Reader, inbox string) error {
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, inbox, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/activity+json")
	req.Header.Set("Accept", "application/activity+json")
	req.Header.Set("User-Agent", s.userAgent())
	if err := s.signRequest(req, body); err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return &statusError{url: inbox, code: resp.StatusCode}
	}
	return nil
}
