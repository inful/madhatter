// Package email implements the channels.Channel interface for SMTP
// delivery, using github.com/nikoksr/notify/service/mail under the
// hood. We build a fresh *mail.Mail per send so recipients don't
// accumulate across calls (a footgun in the upstream API where
// AddReceivers appends to an internal slice).
package email

import (
	"context"
	"fmt"

	mailservice "github.com/nikoksr/notify/service/mail"

	"github.com/inful/madhatter/internal/notify/channels"
)

// Channel implements channels.Channel by sending each message over SMTP
// using a fresh *mail.Mail instance.
type Channel struct {
	host     string // "smtp.example.com:587"
	from     string
	identity string
	user     string
	password string
}

// New returns an EmailChannel configured with the given SMTP details.
// host must include the port (e.g. "smtp.example.com:587"); from is
// the From: address as accepted by service/mail (e.g.
// "MadHatter Rota <noreply@example.com>"). If user is empty, the channel
// sends without authentication.
func New(host, from, identity, user, password string) *Channel {
	return &Channel{
		host:     host,
		from:     from,
		identity: identity,
		user:     user,
		password: password,
	}
}

// Name implements channels.Channel.
func (c *Channel) Name() string { return "email" }

// Send implements channels.Channel. It builds a fresh *mail.Mail per
// call so AddReceivers never carries state across sends, then calls
// mail.Send to deliver. Errors are returned for the outbox worker to
// record; transient SMTP failures will be retried with backoff.
func (c *Channel) Send(ctx context.Context, msg channels.OutboundMessage) error {
	m := mailservice.New(c.from, c.host)

	// AuthenticateSMTP is a no-op when identity/user/password are all
	// empty, leaving the underlying client to attempt anonymous
	// delivery. Useful for internal relays.
	if c.user != "" || c.identity != "" || c.password != "" {
		// smtp.PlainAuth falls back to user as identity when identity
		// is empty, which is what we want.
		var identity, user string
		if c.identity != "" {
			identity = c.identity
		}
		if c.user != "" {
			user = c.user
		} else {
			user = identity
		}
		m.AuthenticateSMTP(identity, user, c.password, smtpHostOnly(c.host))
	}

	m.AddReceivers(msg.Recipient)

	// service/mail is HTML-by-default; switch to plain text.
	m.BodyFormat(mailservice.PlainText)

	if err := m.Send(ctx, msg.Subject, msg.Body); err != nil {
		return fmt.Errorf("email: send: %w", err)
	}
	return nil
}

// smtpHostOnly returns the host portion of a "host:port" SMTP
// destination, for the smtp.PlainAuth constructor.
func smtpHostOnly(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}
