package investigation

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/uptimy/agent/internal/incident"
	"github.com/uptimy/agent/internal/monitor"
)

var sensitiveText = regexp.MustCompile(`(?i)(?:[a-z][a-z0-9+.-]*://[^\s<>"']+|(?:bearer\s+|upa_)[a-z0-9_./+=-]+|(?:password|passwd|authorization|api[_-]?key|token|secret)\s*[:=]\s*[^\s,;]+)`)

func text(value string, secrets ...string) Text {
	original := value
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	value = sensitiveText.ReplaceAllString(value, "[redacted]")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, strings.ToValidUTF8(value, ""))
	out := Text{Classification: "untrusted_data", Redacted: value != original}
	if len(value) > 1024 {
		value = value[:1024]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
		out.Truncated = true
	}
	out.Value = value
	return out
}

func monitorSecrets(m monitor.Monitor) []string {
	var out []string
	if m.Heartbeat != nil {
		out = append(out, m.Heartbeat.Token)
	}
	if m.Check != nil {
		c := m.Check
		out = append(out, c.Target, c.Config.Keyword, c.Config.Expected, c.Config.Query)
		for _, v := range c.Config.Headers {
			out = append(out, v)
			parts := strings.Fields(v)
			if len(parts) == 2 && (strings.EqualFold(parts[0], "Bearer") || strings.EqualFold(parts[0], "Basic")) {
				out = append(out, parts[1])
			}
		}
		if u, err := url.Parse(c.Target); err == nil && u.User != nil {
			if pw, ok := u.User.Password(); ok {
				out = append(out, pw)
			}
		}
	}
	return out
}

func observationText(m monitor.Monitor, value string) Text {
	// Arbitrary SQL values, DNS records and heartbeat payloads cannot be
	// reliably scrubbed. Preserve timing/outcome but suppress these texts.
	if m.Heartbeat != nil || (m.Check != nil && (m.Check.Type == monitor.TypePostgres || m.Check.Type == monitor.TypeMySQL || m.Check.Type == monitor.TypeRedis || m.Check.Type == monitor.TypeDNS)) {
		return Text{Value: "[observation text withheld by security policy]", Classification: "untrusted_data", Redacted: true}
	}
	return text(value, monitorSecrets(m)...)
}

func projectResult(m monitor.Monitor, r monitor.Result) Observation {
	return Observation{ObservedAt: r.Time, Source: "persisted_probe", OK: r.OK, LatencyMS: r.LatencyMS, Message: observationText(m, r.Message)}
}
func projectRun(m monitor.Monitor, r monitor.Run) Run {
	at := r.FinishedAt
	if at == nil {
		at = r.StartedAt
	}
	if at == nil {
		at = r.DueAt
	}
	return Run{ID: r.ID, ObservedAt: at, Source: "persisted_heartbeat_run", DueAt: r.DueAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Outcome: string(r.Outcome), OnTime: r.OnTime, DurationMS: r.DurationMS, Message: observationText(m, r.Message)}
}
func projectEvent(m monitor.Monitor, e monitor.Event) Transition {
	return Transition{ID: e.ID, ObservedAt: e.Time, Source: "persisted_status_transition", Status: string(e.Status), Message: observationText(m, e.Message)}
}
func projectIncident(in incident.Incident) Incident {
	return Incident{ID: in.ID, Title: text(in.Title), Severity: string(in.Severity), Status: string(in.Status), Source: "manually_authored_status_page_incident", CreatedAt: in.CreatedAt, ResolvedAt: in.ResolvedAt}
}
