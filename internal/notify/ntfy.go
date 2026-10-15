package notify

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/uptimy/agent/internal/schema"
)

const defaultNtfyServer = "https://ntfy.sh"

var ntfyTopic = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

func init() {
	Register(Channel{
		Type:      "ntfy",
		Reminders: true,
		Label:     "ntfy",
		Help:      "Push notifications to your phone. Pick a topic on ntfy.sh or your own ntfy server, and subscribe to it in the ntfy app.",
		Order:     50,
		Fields: []schema.Field{
			{Key: "topic", Label: "Topic", Input: schema.Text, Placeholder: "acme-alerts-7f3k", Required: true,
				Hint: "Anyone who knows a topic on ntfy.sh can read it: pick one that's hard to guess."},
			{Key: "url", Label: "Server", Input: schema.Text, Placeholder: defaultNtfyServer, Default: defaultNtfyServer},
			{Key: "token", Label: "Access token", Input: schema.Password, Hint: "Only for protected topics", Wide: true},
		},
		Validate: func(c *Config) error {
			c.Topic, c.URL = strings.TrimSpace(c.Topic), strings.TrimRight(strings.TrimSpace(c.URL), "/")
			if c.URL == "" {
				c.URL = defaultNtfyServer
			}
			if !ntfyTopic.MatchString(c.Topic) {
				return errors.New("the topic can use letters, digits, - and _ (up to 64)")
			}
			if u, err := url.Parse(c.URL); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				return errors.New("the server must be an http(s) URL, e.g. https://ntfy.sh")
			}
			return nil
		},
		Send: func(ctx context.Context, s *Sender, c Config, a Alert) error {
			// JSON publishing: https://docs.ntfy.sh/publish/#publish-as-json
			msg := map[string]any{
				"topic":    c.Topic,
				"title":    a.Title(),
				"message":  alertDetails(a),
				"priority": 5,
				"tags":     []string{"rotating_light"},
			}
			switch {
			case a.Status == "up":
				msg["priority"], msg["tags"] = 3, []string{"white_check_mark"}
			case a.Type == AlertAcknowledged:
				msg["priority"], msg["tags"] = 3, []string{"eyes"}
			case a.Type == AlertReminder:
				msg["priority"] = 4
			}
			var headers map[string]string
			if c.Token != "" {
				headers = map[string]string{"Authorization": "Bearer " + c.Token}
			}
			return s.PostJSONWithHeaders(ctx, c.URL, msg, headers)
		},
	})
}

// alertDetails is the alert's body below its title: what's watched, what
// happened and when.
func alertDetails(a Alert) string {
	var b strings.Builder
	if a.Message != "" && a.Kind != "heartbeat" {
		b.WriteString(a.Message + "\n")
	}
	if a.Target != "" {
		b.WriteString(a.Target + "\n")
	}
	b.WriteString(a.Time.UTC().Format("2006-01-02 15:04:05 UTC"))
	return b.String()
}
