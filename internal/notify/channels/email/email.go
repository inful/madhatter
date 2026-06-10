// Package email implements the channels.Channel interface for SMTP
// delivery. We build a fresh *email.Email per send so headers and
// recipients don't accumulate across calls, and we set
// List-Unsubscribe / List-Unsubscribe-Post from the per-message
// UnsubscribeURL when the producer supplies one (RFC 8058).
package email

import (
	"context"
	"fmt"
	"net/smtp"
	"net/textproto"
	"strings"

	"github.com/jordan-wright/email"

	"github.com/inful/madhatter/internal/notify/channels"
)

// Channel implements channels.Channel by sending each message over SMTP
// using a fresh *email.Email instance. The Headers field on the
// outbound message is merged into the email's MIME headers so callers
// can add List-Unsubscribe and other RFC 8058 headers without us
// having to know the vocabulary ahead of time.
type Channel struct {
	host     string // "smtp.example.com:587"
	from     string
	identity string
	user     string
	password string
}

// New returns an EmailChannel configured with the given SMTP details.
// host must include the port (e.g. "smtp.example.com:587"); from is
// the From: address (e.g. "MadHatter Rota <noreply@example.com>").
// If user is empty, the channel sends without authentication.
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

// Send implements channels.Channel. It builds a fresh *email.Email per
// call so headers and recipients never carry state across sends.
// Errors are returned for the outbox worker to record; transient SMTP
// failures will be retried with backoff.
func (c *Channel) Send(ctx context.Context, msg channels.OutboundMessage) error {
	m := email.NewEmail()
	m.From = c.from
	m.To = []string{msg.Recipient}
	m.Subject = msg.Subject
	m.Text = []byte(msg.Body)
	m.Headers = textproto.MIMEHeader{}

	// Add per-message headers (e.g. List-Unsubscribe) supplied by
	// the producer. We set the email's Headers map only when there
	// is something to set so the empty-map default doesn't
	// accidentally suppress jordan-wright's default Content-Type.
	if msg.UnsubscribeURL != "" {
		m.Headers.Set("List-Unsubscribe", "<"+msg.UnsubscribeURL+">")
		// RFC 8058 one-click: mail clients can POST to the URL to
		// unsubscribe without opening a browser.
		m.Headers.Set("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
	}
	for k, v := range msg.Headers {
		m.Headers.Set(k, v)
	}

	// Auth: smtp.PlainAuth is a no-op when no fields are set,
	// leaving the underlying client to attempt anonymous
	// delivery. Useful for internal relays.
	var auth smtp.Auth
	if c.user != "" || c.identity != "" || c.password != "" {
		identity := c.identity
		if identity == "" {
			identity = c.user
		}
		user := c.user
		if user == "" {
			user = identity
		}
		auth = smtp.PlainAuth(identity, user, c.password, smtpHostOnly(c.host))
	}

	// Respect ctx so the worker can shut down promptly.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("email: send: %w", err)
	}

	if err := m.Send(c.host, auth); err != nil {
		return fmt.Errorf("email: send: %w", err)
	}
	return nil
}

// smtpHostOnly returns the host portion of a "host:port" SMTP
// destination, for the smtp.PlainAuth constructor.
func smtpHostOnly(addr string) string {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr
	}
	return addr[:i]
}
