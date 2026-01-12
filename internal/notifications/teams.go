package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const teamsWebhookDefaultTimeout = 10 * time.Second

const teamsWebhookErrorBodyLimitBytes int64 = 4096

type TeamsWebhookNotifier struct {
	WebhookURL string
	Client     *http.Client
}

type teamsWebhookPayload struct {
	Text string `json:"text"`
}

func (n *TeamsWebhookNotifier) Send(ctx context.Context, message string) error {
	if err := n.validate(message); err != nil {
		return err
	}

	client := n.httpClient()

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
		return teamsWebhookHTTPError(resp)
	}

	return nil
}

func (n *TeamsWebhookNotifier) validate(message string) error {
	if n == nil {
		return errors.New("notifier is nil")
	}
	if n.WebhookURL == "" {
		return errors.New("webhook url is empty")
	}
	if message == "" {
		return errors.New("message is empty")
	}
	return nil
}

func (n *TeamsWebhookNotifier) httpClient() *http.Client {
	if n.Client != nil {
		return n.Client
	}
	return &http.Client{Timeout: teamsWebhookDefaultTimeout}
}

func teamsWebhookHTTPError(resp *http.Response) error {
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, teamsWebhookErrorBodyLimitBytes))
	bodyText := strings.TrimSpace(string(respBody))
	if bodyText == "" {
		return fmt.Errorf("teams webhook request failed: status %d", resp.StatusCode)
	}
	return fmt.Errorf("teams webhook request failed: status %d: %s", resp.StatusCode, bodyText)
}
