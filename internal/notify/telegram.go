package notify

import (
	"context"
	"errors"

	"github.com/uptimy/agent/internal/schema"
)

func init() {
	Register(Channel{
		Type:      "telegram",
		Reminders: true,
		Label:     "Telegram",
		Help:      "Create a bot with @BotFather, then add it to your chat.",
		Order:     30,
		Fields: []schema.Field{
			{Key: "bot_token", Label: "Bot token", Input: schema.Password, Required: true},
			{Key: "chat_id", Label: "Chat ID", Input: schema.Text, Required: true},
		},
		Validate: func(c *Config) error {
			if c.BotToken == "" || c.ChatID == "" {
				return errors.New("bot token and chat ID are required")
			}
			return nil
		},
		Send: func(ctx context.Context, s *Sender, c Config, a Alert) error {
			endpoint := "https://api.telegram.org/bot" + c.BotToken + "/sendMessage"
			return s.PostJSON(ctx, endpoint, map[string]string{"chat_id": c.ChatID, "text": a.Text()})
		},
	})
}
