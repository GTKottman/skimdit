// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"crypto/tls"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

var (
	finalRecipientRe = regexp.MustCompile(`(?im)^(?:Final|Original)-Recipient:\s*rfc822;\s*<?([^\s>]+@[^\s>]+)>?`)
	statusRe         = regexp.MustCompile(`(?im)^Status:\s*([245])\.\d{1,3}\.\d{1,3}`)
	feedbackRe       = regexp.MustCompile(`(?im)^Feedback-Type:\s*abuse`)
	rcptToRe         = regexp.MustCompile(`(?im)^Original-Rcpt-To:\s*<?([^\s>]+@[^\s>]+)>?`)
)

// Bounce is what one returned email tells us.
type Bounce struct {
	Email     string
	Hard      bool
	Complaint bool
}

// parseBounce reads a delivery status notification (RFC 3464) or an abuse
// report (RFC 5965). It returns nil for anything else.
func parseBounce(raw []byte) *Bounce {
	if feedbackRe.Match(raw) {
		if m := rcptToRe.FindSubmatch(raw); m != nil {
			return &Bounce{Email: strings.ToLower(string(m[1])), Complaint: true}
		}
		return nil
	}
	m := finalRecipientRe.FindSubmatch(raw)
	if m == nil {
		return nil
	}
	b := &Bounce{Email: strings.ToLower(string(m[1]))}
	if st := statusRe.FindSubmatch(raw); st != nil {
		switch string(st[1]) {
		case "2":
			return nil // delivered (a positive DSN)
		case "5":
			b.Hard = true
		}
	}
	return b
}

// applyBounce updates the subscriber a bounce is about.
func (s *Server) applyBounce(b *Bounce) {
	sub, err := s.store.SubscriberByEmail(b.Email)
	if err != nil {
		return
	}
	if b.Complaint {
		_ = s.store.SetSubscriberStatus(sub.ID, "complained")
		return
	}
	_ = s.store.AddBounce(b.Email, b.Hard)
}

// checkBounces reads unseen mail in the bounce mailbox over IMAPS and
// turns off addresses that hard-bounce, soft-bounce three times, or
// report the newsletter as spam.
func (s *Server) checkBounces() (int, error) {
	cfg := s.cfg.Newsletter
	if cfg.BounceIMAP == "" || cfg.BounceUser == "" {
		return 0, nil
	}
	c, err := client.DialTLS(cfg.BounceIMAP, &tls.Config{MinVersion: tls.VersionTLS12})
	if err != nil {
		return 0, fmt.Errorf("bounce mailbox: %w", err)
	}
	defer c.Logout()
	if err := c.Login(cfg.BounceUser, getenv("SKIMDIT_IMAP_PASSWORD")); err != nil {
		return 0, fmt.Errorf("bounce mailbox login: %w", err)
	}
	if _, err := c.Select("INBOX", false); err != nil {
		return 0, err
	}
	crit := imap.NewSearchCriteria()
	crit.WithoutFlags = []string{imap.SeenFlag}
	ids, err := c.Search(crit)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	if len(ids) > 200 {
		ids = ids[:200]
	}
	set := new(imap.SeqSet)
	set.AddNum(ids...)
	section := &imap.BodySectionName{}
	msgs := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() { done <- c.Fetch(set, []imap.FetchItem{section.FetchItem()}, msgs) }()
	n := 0
	for msg := range msgs {
		r := msg.GetBody(section)
		if r == nil {
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(r, 2<<20))
		if b := parseBounce(raw); b != nil {
			s.applyBounce(b)
			n++
		}
	}
	if err := <-done; err != nil {
		return n, err
	}
	// Fetching the body marks messages as seen.
	return n, nil
}
