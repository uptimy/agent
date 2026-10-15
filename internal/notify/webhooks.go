package notify

import (
	"context"
	"errors"
	"net/url"

	"github.com/uptimy/agent/internal/schema"
)

// Slack, Discord and generic webhooks all POST JSON to a URL the user pastes.

func webhookURLField(placeholder string) schema.Field {
	return schema.Field{Key: "url", Label: "Webhook URL", Input: schema.Password, Placeholder: placeholder, Required: true, Wide: true}
}

func validateWebhookURL(c *Config) error {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("a valid webhook URL is required")
	}
	return nil
}

func init() {
	Register(Channel{
		Type:      "slack",
		Reminders: true,
		Label:     "Slack",
		Help:      "Paste an incoming webhook URL from Slack.",
		Order:     10,
		Fields:    []schema.Field{webhookURLField("https://hooks.slack.com/services/…")},
		Validate:  validateWebhookURL,
		Send: func(ctx context.Context, s *Sender, c Config, a Alert) error {
			return s.PostJSON(ctx, c.URL, map[string]string{"text": a.Text()})
		},
	})
	Register(Channel{
		Type:      "discord",
		Reminders: true,
		Label:     "Discord",
		Help:      "Paste a webhook URL from your Discord channel's integrations.",
		Order:     20,
		Fields:    []schema.Field{webhookURLField("https://discord.com/api/webhooks/…")},
		Validate:  validateWebhookURL,
		Send: func(ctx context.Context, s *Sender, c Config, a Alert) error {
			return s.PostJSON(ctx, c.URL, map[string]string{"content": a.Text()})
		},
	})
	Register(Channel{
		Type:      "webhook",
		Reminders: true,
		Label:     "Webhook",
		Help:      "We POST a JSON body with monitor_name, status, message and time.",
		Order:     40,
		Fields:    []schema.Field{webhookURLField("https://example.com/hooks/uptime")},
		Validate:  validateWebhookURL,
		Send: func(ctx context.Context, s *Sender, c Config, a Alert) error {
			return s.PostJSON(ctx, c.URL, a)
		},
	})
}
