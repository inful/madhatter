package notifications

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTeamsWebhookNotifier_Send_Success(t *testing.T) {
	t.Parallel()

	var gotContentType string
	bodyCh := make(chan string, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		bodyCh <- string(b)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	n := &TeamsWebhookNotifier{WebhookURL: srv.URL}
	err := n.Send(context.Background(), "Hello")
	require.NoError(t, err)
	require.Equal(t, "application/json", gotContentType)
	require.JSONEq(t, `{"text":"Hello"}`, <-bodyCh)
}

func TestTeamsWebhookNotifier_Send_Non2xxIncludesStatusAndBody(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("nope"))
	}))
	t.Cleanup(srv.Close)

	n := &TeamsWebhookNotifier{WebhookURL: srv.URL}
	err := n.Send(context.Background(), "Hello")
	require.Error(t, err)
	require.Contains(t, err.Error(), "status 400")
	require.Contains(t, err.Error(), "nope")
}

func TestTeamsWebhookNotifier_Send_EmptyMessage(t *testing.T) {
	t.Parallel()

	n := &TeamsWebhookNotifier{WebhookURL: "http://example.invalid"}
	err := n.Send(context.Background(), "")
	require.Error(t, err)
}
