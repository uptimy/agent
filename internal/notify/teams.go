package notify

import (
	"context"

	"github.com/uptimy/agent/internal/schema"
)

func init() {
	Register(Channel{
		Type:      "teams",
		Reminders: true,
		Label:     "Microsoft Teams",
		Help: "In Teams, add the \"Post to a channel when a webhook request is received\" workflow to a channel " +
			"and paste its URL. Older incoming webhook URLs work too.",
		Order:    25,
		Fields:   []schema.Field{webhookURLField("https://prod-00.westus.logic.azure.com/workflows/…")},
		Validate: validateWebhookURL,
		Send: func(ctx context.Context, s *Sender, c Config, a Alert) error {
			return s.PostJSON(ctx, c.URL, teamsCard(a))
		},
	})
}

// teamsCard is the alert as an Adaptive Card, the format both Teams workflows
// and incoming webhooks accept.
func teamsCard(a Alert) map[string]any {
	color := "Attention"
	switch {
	case a.Status == "up":
		color = "Good"
	case a.Type == AlertAcknowledged:
		color = "Accent"
	}
	facts := []map[string]string{}
	add := func(title, value string) {
		if value != "" {
			facts = append(facts, map[string]string{"title": title, "value": value})
		}
	}
	add("Watching", a.Target)
	if a.Kind != "heartbeat" {
		add("Message", a.Message)
	}
	add("Time", a.Time.UTC().Format("2006-01-02 15:04:05 UTC"))
	return map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{
				"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
				"type":    "AdaptiveCard",
				"version": "1.4",
				"body": []map[string]any{
					{"type": "TextBlock", "text": a.Icon() + " " + a.Title(), "weight": "Bolder", "size": "Medium", "wrap": true, "color": color},
					{"type": "FactSet", "facts": facts},
				},
			},
		}},
	}
}
