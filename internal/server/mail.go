// SPDX-License-Identifier: AGPL-3.0-or-later

package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	mail "github.com/wneessen/go-mail"
)

// Mail is one outgoing message.
type Mail struct {
	To              string
	Subject         string
	Text            string
	HTML            string
	ListUnsubscribe string // URL for one-click unsubscribe (RFC 8058)
	MessageID       string
}

// Mailer sends email. The SMTP one is used in production; tests record.
type Mailer interface {
	Send(ctx context.Context, msgs []*Mail) error
}

// errNoMail means no mail server is configured.
var errNoMail = errors.New("no mail server configured ([newsletter] smtp_host)")

type smtpMailer struct{ s *Server }

func (s *Server) mailer() Mailer {
	if s.testMailer != nil {
		return s.testMailer
	}
	return smtpMailer{s}
}

func (m smtpMailer) Send(ctx context.Context, msgs []*Mail) error {
	cfg := m.s.cfg.Newsletter
	if cfg.SMTPHost == "" || cfg.From == "" {
		return errNoMail
	}
	opts := []mail.Option{
		mail.WithPort(cfg.SMTPPort),
		mail.WithTimeout(30_000_000_000),
	}
	switch cfg.SMTPSecurity {
	case "tls":
		opts = append(opts, mail.WithSSL())
	case "starttls":
		opts = append(opts, mail.WithTLSPortPolicy(mail.TLSMandatory))
	case "none":
		opts = append(opts, mail.WithTLSPortPolicy(mail.NoTLS))
	}
	if cfg.SMTPUser != "" {
		opts = append(opts, mail.WithSMTPAuth(mail.SMTPAuthAutoDiscover), mail.WithUsername(cfg.SMTPUser), mail.WithPassword(getenv("SKIMDIT_SMTP_PASSWORD")))
	}
	client, err := mail.NewClient(cfg.SMTPHost, opts...)
	if err != nil {
		return err
	}
	var out []*mail.Msg
	for _, x := range msgs {
		msg := mail.NewMsg()
		if err := msg.From(cfg.From); err != nil {
			return fmt.Errorf("newsletter.from: %w", err)
		}
		if err := msg.To(x.To); err != nil {
			return err
		}
		msg.Subject(x.Subject)
		msg.SetMessageIDWithValue(firstNonEmpty(x.MessageID, randomToken(16)) + "@" + m.s.cfg.Domain())
		msg.SetDate()
		msg.SetGenHeader("Auto-Submitted", "auto-generated")
		if x.ListUnsubscribe != "" {
			msg.SetGenHeader("List-Unsubscribe", "<"+x.ListUnsubscribe+">")
			msg.SetGenHeader("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
		}
		msg.SetBodyString(mail.TypeTextPlain, x.Text)
		if x.HTML != "" {
			msg.AddAlternativeString(mail.TypeTextHTML, x.HTML)
		}
		out = append(out, msg)
	}
	return client.DialAndSendWithContext(ctx, out...)
}

// recordingMailer keeps sent mail in memory (tests).
type recordingMailer struct {
	sent []*Mail
	fail error
}

func (r *recordingMailer) Send(_ context.Context, msgs []*Mail) error {
	if r.fail != nil {
		return r.fail
	}
	r.sent = append(r.sent, msgs...)
	return nil
}

func (r *recordingMailer) find(to, subjectPart string) *Mail {
	for _, m := range r.sent {
		if m.To == to && strings.Contains(m.Subject, subjectPart) {
			return m
		}
	}
	return nil
}
