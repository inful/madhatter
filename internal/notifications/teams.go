package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const teamsWebhookDefaultTimeout = 10 * time.Second

type TeamsWebhookNotifier struct {
	WebhookURL string
	Client     *http.Client
}

type teamsWebhookPayload struct {
	Text string `json:"text"`
}

func (n *TeamsWebhookNotifier) Send(ctx context.Context, message string) error {
	if n == nil {
		return errors.New("notifier is nil")
	}
	if n.WebhookURL == "" {
		return errors.New("webhook url is empty")
	}
	if message == "" {
		return errors.New("message is empty")
	}

	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: teamsWebhookDefaultTimeout}
	}

	body, err := json.Marshal(teamsWebhookPayload{Text: message})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("teams webhook request failed")
	}

	return nil
}
