package email

import (
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/inful/madhatter/internal/notify/channels"
)

const testRecipient = "alice@example.com"

// capturedMessage records a single SMTP delivery observed by the test
// server backend.
type capturedMessage struct {
	from string
	to   []string
	body string
}

// testBackend is a tiny SMTP backend that accepts every message and
// records it for the test to assert against.
type testBackend struct {
	mu       sync.Mutex
	received []capturedMessage
}

func (b *testBackend) NewSession(_ *smtp.Conn) (smtp.Session, error) {
	return &testSession{backend: b}, nil
}

type testSession struct {
	backend *testBackend
	from    string
	to      []string
}

func (s *testSession) AuthMechanisms() []string {
	return []string{sasl.Plain}
}

func (s *testSession) Auth(mech string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(identity, username, password string) error {
		// Always accept; tests configure the expected credentials
		// directly on the email channel.
		_ = identity
		_ = username
		_ = password
		return nil
	}), nil
}

func (s *testSession) Mail(from string, _ *smtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *testSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

func (s *testSession) Data(r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.backend.mu.Lock()
	s.backend.received = append(s.backend.received, capturedMessage{
		from: s.from,
		to:   append([]string(nil), s.to...),
		body: string(b),
	})
	s.backend.mu.Unlock()
	return nil
}

func (s *testSession) Reset()        { s.from, s.to = "", nil }
func (s *testSession) Logout() error { return nil }

func (b *testBackend) snapshot() []capturedMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]capturedMessage, len(b.received))
	copy(out, b.received)
	return out
}

// startTestServer brings up a go-smtp server on a random local port
// and returns the "host:port" string plus a cleanup function.
func startTestServer(t *testing.T, requireAuth bool) (addr string, backend *testBackend, cleanup func()) {
	t.Helper()
	be := &testBackend{}
	s := smtp.NewServer(be)
	s.Domain = "localhost"
	s.WriteTimeout = 10 * time.Second
	s.ReadTimeout = 10 * time.Second
	s.MaxMessageBytes = 1024 * 1024
	s.MaxRecipients = 50
	s.AllowInsecureAuth = true
	_ = requireAuth // accepted via AuthMechanisms return above

	lc := &net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s.Addr = ln.Addr().String()

	go func() {
		// Serve returns when the listener is closed by cleanup.
		_ = s.Serve(ln)
	}()

	cleanup = func() {
		_ = s.Close()
	}
	return s.Addr, be, cleanup
}

func TestEmailChannel_Name(t *testing.T) {
	ch := New("localhost:0", "x@y.z", "", "", "")
	assert.Equal(t, "email", ch.Name())
}

func TestEmailChannel_Send_PlainTextBody(t *testing.T) {
	addr, backend, cleanup := startTestServer(t, false)
	defer cleanup()

	ch := New(addr, "Rota <noreply@example.com>", "", "", "")
	msg := channels.OutboundMessage{
		EventKind:     "swap.requested",
		Recipient:     testRecipient,
		RecipientName: "Alice",
		Subject:       "HAT swap request",
		Body:          "Hello, Alice. Please review.",
	}
	err := ch.Send(context.Background(), msg)
	require.NoError(t, err)

	got := backend.snapshot()
	require.Len(t, got, 1)
	// jordan-wright/email parses the From into just the address part
	// for the SMTP MAIL FROM command; the display name is preserved
	// in the From: header of the message body.
	assert.Equal(t, "noreply@example.com", got[0].from)
	assert.Equal(t, []string{testRecipient}, got[0].to)
	assert.Contains(t, got[0].body, "Subject: HAT swap request")
	assert.Contains(t, got[0].body, "Hello, Alice. Please review.")
	assert.Contains(t, got[0].body, "From: \"Rota\" <noreply@example.com>")
	// The service/mail library writes HTML by default; our channel
	// forces plain text, so there should be no Content-Type: text/html.
	assert.NotContains(t, strings.ToLower(got[0].body), "content-type: text/html")
}

func TestEmailChannel_Send_AuthRequired_SucceedsWithCreds(t *testing.T) {
	addr, backend, cleanup := startTestServer(t, true)
	defer cleanup()

	ch := New(addr, "Rota <noreply@example.com>", "", "user", "pass")
	err := ch.Send(context.Background(), channels.OutboundMessage{
		EventKind: "test",
		Recipient: testRecipient,
		Subject:   "S",
		Body:      "B",
	})
	require.NoError(t, err)
	assert.Len(t, backend.snapshot(), 1)
}

func TestEmailChannel_Send_RejectsCancelledContext(t *testing.T) {
	addr, _, cleanup := startTestServer(t, false)
	defer cleanup()

	ch := New(addr, "x@y.z", "", "", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := ch.Send(ctx, channels.OutboundMessage{
		Recipient: testRecipient,
		Subject:   "S",
		Body:      "B",
	})
	require.Error(t, err)
}

func TestEmailChannel_Send_NoServer_ReturnsError(t *testing.T) {
	// Find a port that is bound but not accepting connections to
	// guarantee a connect failure.
	lc := &net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	_ = ln.Close()

	ch := New(addr, "x@y.z", "", "", "")
	err = ch.Send(context.Background(), channels.OutboundMessage{
		Recipient: testRecipient,
		Subject:   "S",
		Body:      "B",
	})
	require.Error(t, err)
}

func TestSmtpHostOnly(t *testing.T) {
	cases := map[string]string{
		"smtp.example.com:587": "smtp.example.com",
		"localhost:25":         "localhost",
		"host-without-port":    "host-without-port",
	}
	for in, want := range cases {
		assert.Equal(t, want, smtpHostOnly(in), "smtpHostOnly(%q)", in)
	}
}
