package api

import (
	"context"
	"time"

	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/store"
)

// periodDays are the windows longer than a day that uptime is shown for.
// They're whole UTC days, today included, so they add up from daily
// rollups; "24h" is the last 24 hours, from raw history.
var periodDays = map[string]int{"7d": 7, "30d": 30, "90d": 90}

// ratio is a monitor's uptime (a healthcheck) or share of runs on time (a
// heartbeat) over a period: "24h" or one of periodDays.
func (s *Server) ratio(ctx context.Context, m monitor.Monitor, period string) (*float64, error) {
	if days, ok := periodDays[period]; ok {
		h, err := s.Store.History(ctx, m.ID, days)
		if err != nil {
			return nil, err
		}
		if m.Kind == monitor.KindHeartbeat {
			return h.RunStats().Ratio, nil
		}
		return h.Uptime().Ratio, nil
	}
	since := time.Now().Add(-24 * time.Hour)
	if m.Kind == monitor.KindHeartbeat {
		st, err := s.Store.RunStatsSince(ctx, m.ID, since)
		return st.Ratio, err
	}
	u, err := s.Store.UptimeSince(ctx, m.ID, since)
	return u.Ratio, err
}

// healthcheckUptimes is a healthcheck's uptime for 24h and every one of
// periodDays.
func (s *Server) healthcheckUptimes(ctx context.Context, id int64) (map[string]store.Uptime, error) {
	out := map[string]store.Uptime{}
	var err error
	if out["24h"], err = s.Store.UptimeSince(ctx, id, time.Now().Add(-24*time.Hour)); err != nil {
		return nil, err
	}
	h, err := s.Store.History(ctx, id, 90)
	if err != nil {
		return nil, err
	}
	for label, days := range periodDays {
		out[label] = h.Last(days).Uptime()
	}
	return out, nil
}
