package notify

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/nicholas-fedor/shoutrrr/pkg/router"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"

	"github.com/uptimy/agent/internal/schema"
)

// shoutrrrTimeout bounds one send, like the Sender's HTTP timeout.
const shoutrrrTimeout = 10 * time.Second

func init() {
	Register(Channel{
		Type:      "shoutrrr",
		Reminders: true,
		Label:     "More services (Shoutrrr)",
		Help: "One URL for any of 30+ services Shoutrrr supports: Pushover, Gotify, Matrix, Google Chat, Mattermost, Rocket.Chat, " +
			"Opsgenie, Signal, Zulip, Home Assistant, MQTT, Bark and more. The URL format for each is at https://shoutrrr.nickfedor.com/latest/services/overview/.",
		Order: 90,
		Fields: []schema.Field{
			{Key: "url", Label: "Service URL", Input: schema.Password, Placeholder: "pushover://shoutrrr:token@userKey", Required: true, Wide: true,
				Hint: "It holds the service's credentials, so it's stored like a password."},
		},
		Validate: func(c *Config) error {
			c.URL = strings.TrimSpace(c.URL)
			scheme, _, ok := strings.Cut(c.URL, "://")
			if !ok || scheme == "" {
				return errors.New("enter a Shoutrrr URL, like pushover://shoutrrr:token@userKey")
			}
			r := router.ServiceRouter{}
			if !slices.Contains(r.ListServices(), strings.ToLower(scheme)) {
				return fmt.Errorf("Shoutrrr doesn't support %q URLs", scheme)
			}
			if _, err := shoutrrrSender(c.URL); err != nil {
				return err
			}
			return nil
		},
		Send: func(ctx context.Context, _ *Sender, c Config, a Alert) error {
			sender, err := shoutrrrSender(c.URL)
			if err != nil {
				return err
			}
			// The alert leads the message, since some services show no title.
			msg := a.Icon() + " " + a.Title() + "\n" + alertDetails(a)
			errc := sender.SendAsync(msg, &types.Params{"title": "Uptimy Agent"})
			var errs []error
			for {
				select {
				case err, more := <-errc:
					if !more {
						return redactURL(errors.Join(errs...), c.URL)
					}
					if err != nil {
						errs = append(errs, err)
					}
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		},
	})
}

// shoutrrrSender parses url into a sender. Errors never repeat the URL,
// which holds credentials.
func shoutrrrSender(rawURL string) (*router.ServiceRouter, error) {
	sender, err := router.NewWithOptions(nil, types.SenderOptions{Timeout: shoutrrrTimeout}, rawURL)
	if err != nil {
		return nil, redactURL(fmt.Errorf("invalid Shoutrrr URL: %w", err), rawURL)
	}
	return sender, nil
}

// redactURL removes url from err's message, and every part of it that may
// be a credential: services repeat a bad token on its own ("invalid gotify
// token: ...").
func redactURL(err error, rawURL string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	scheme, rest, _ := strings.Cut(rawURL, "://")
	secrets := []string{rawURL, rest}
	if u, perr := url.Parse(rawURL); perr == nil {
		if u.User != nil {
			pw, _ := u.User.Password()
			secrets = append(secrets, u.User.String(), u.User.Username(), pw)
		}
		secrets = append(secrets, strings.Split(u.Path, "/")...)
		for _, vs := range u.Query() {
			secrets = append(secrets, vs...)
		}
	}
	// Longest first, so a part inside another is already gone.
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	for _, s := range secrets {
		if len(s) >= 4 && s != scheme {
			msg = strings.ReplaceAll(msg, s, "…")
		}
	}
	return errors.New(msg)
}
