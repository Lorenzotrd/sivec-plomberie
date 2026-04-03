package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// SlackNotifier sends alerts to a Slack channel via incoming webhook.
type SlackNotifier struct {
	webhookURL string
	client     *http.Client

	// Rate limiting: avoid spamming the same alert repeatedly.
	mu       sync.Mutex
	lastSent map[string]time.Time
	cooldown time.Duration
}

type slackMessage struct {
	Text   string       `json:"text,omitempty"`
	Blocks []slackBlock `json:"blocks,omitempty"`
}

type slackBlock struct {
	Type string     `json:"type"`
	Text *slackText `json:"text,omitempty"`
}

type slackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// NewSlackNotifier creates a notifier. If webhookURL is empty, all sends are
// silently no-ops so callers don't need nil-checks.
func NewSlackNotifier(webhookURL string) *SlackNotifier {
	return &SlackNotifier{
		webhookURL: webhookURL,
		client:     &http.Client{Timeout: 10 * time.Second},
		lastSent:   make(map[string]time.Time),
		cooldown:   15 * time.Minute, // don't repeat the same alert within 15 min
	}
}

// Enabled returns true if a webhook URL is configured.
func (s *SlackNotifier) Enabled() bool {
	return s.webhookURL != ""
}

// SendLowBalanceAlert sends a formatted low-balance warning to Slack.
// The dedupKey prevents the same alert from firing more than once per cooldown period.
func (s *SlackNotifier) SendLowBalanceAlert(address string, tokenSymbol string, balance float64, threshold float64, botName string) error {
	if !s.Enabled() {
		return nil
	}

	dedupKey := fmt.Sprintf("low_balance:%s:%s", address, tokenSymbol)

	s.mu.Lock()
	if last, ok := s.lastSent[dedupKey]; ok && time.Since(last) < s.cooldown {
		s.mu.Unlock()
		return nil // recently sent, skip
	}
	// Reserve the slot immediately to prevent TOCTOU race
	s.lastSent[dedupKey] = time.Now()
	s.mu.Unlock()

	msg := slackMessage{
		Blocks: []slackBlock{
			{
				Type: "header",
				Text: &slackText{Type: "plain_text", Text: "⚠️ Low Balance Alert"},
			},
			{
				Type: "section",
				Text: &slackText{
					Type: "mrkdwn",
					Text: fmt.Sprintf(
						"*Bot:* `%s`\n*Wallet:* `%s`\n*Token:* %s\n*Balance:* `%.4f`\n*Threshold:* `%.4f`",
						botName, address, tokenSymbol, balance, threshold,
					),
				},
			},
			{
				Type: "section",
				Text: &slackText{
					Type: "mrkdwn",
					Text: "Please top up the owner wallet to continue betting.",
				},
			},
		},
	}

	if err := s.send(msg); err != nil {
		// Roll back the reservation so the alert can be retried
		s.mu.Lock()
		delete(s.lastSent, dedupKey)
		s.mu.Unlock()
		return err
	}

	return nil
}

// Send sends an arbitrary text message.
func (s *SlackNotifier) Send(text string) error {
	if !s.Enabled() {
		return nil
	}
	return s.send(slackMessage{Text: text})
}

func (s *SlackNotifier) send(msg slackMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("slack: marshal error: %w", err)
	}

	resp, err := s.client.Post(s.webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack: request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack: unexpected status %d", resp.StatusCode)
	}
	return nil
}
