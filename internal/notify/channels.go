package notify

import (
	"context"
	"fmt"
	"sort"

	"github.com/uptimy/agent/internal/schema"
)

// A Channel is one kind of alert destination (Slack, a webhook, ...). Each
// built-in channel registers itself from its own file; see CONTRIBUTING.md,
// "Adding a notification channel".
type Channel struct {
	Type  string `json:"type"`
	Label string `json:"label"`
	// Help tells people where to get the settings, shown above the form.
	Help   string         `json:"help"`
	Order  int            `json:"order"`
	Fields []schema.Field `json:"fields"`
	// Reminders is set for channels people read, which get reminders while
	// a monitor stays down and a note when someone acknowledges it. Paging
	// tools (PagerDuty) track outages themselves.
	Reminders bool `json:"reminders"`

	// Validate checks the settings, and may fill defaults.
	Validate func(c *Config) error                                         `json:"-"`
	Send     func(ctx context.Context, s *Sender, c Config, a Alert) error `json:"-"`
}

var channels = map[string]Channel{}

// Register adds a channel. It panics on a duplicate so mistakes show up at
// startup and in tests.
func Register(ch Channel) {
	if ch.Type == "" || ch.Label == "" || ch.Validate == nil || ch.Send == nil {
		panic("notify: Register needs a type, a label, Validate and Send")
	}
	if _, dup := channels[ch.Type]; dup {
		panic(fmt.Sprintf("notify: channel %q registered twice", ch.Type))
	}
	channels[ch.Type] = ch
}

// Lookup returns the registered channel for t.
func Lookup(t string) (Channel, bool) {
	ch, ok := channels[t]
	return ch, ok
}

// Channels returns every registered channel, in display order.
func Channels() []Channel {
	out := make([]Channel, 0, len(channels))
	for _, ch := range channels {
		out = append(out, ch)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}
