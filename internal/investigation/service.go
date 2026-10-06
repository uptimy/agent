// Package investigation exposes factual, bounded monitoring evidence without
// interpreting causes. It has no transport or probe execution dependency.
package investigation

import (
	"context"
	"errors"
	"time"

	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/store"
)

const MaxPage = 200
const MaxOffset = 10000
const MaxWindow = 31 * 24 * time.Hour

type Service struct {
	store         Repository
	live          LiveState
	retentionDays int
	now           func() time.Time
}

func New(st Repository, live LiveState, retentionDays int) *Service {
	if retentionDays < 1 {
		retentionDays = 30
	}
	return &Service{store: st, live: live, retentionDays: retentionDays, now: func() time.Time { return time.Now().UTC() }}
}

func page(limit, offset int) (int, error) {
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > MaxPage || offset < 0 || offset > MaxOffset {
		return 0, ErrInvalidInput
	}
	return limit, nil
}

func (s *Service) projectMonitor(m monitor.Monitor) Monitor {
	status, maintenance := CurrentView(s.live, m)
	out := Monitor{ID: m.ID, Name: text(m.Name, monitorSecrets(m)...), Kind: string(m.Kind), Status: string(status), StatusSource: "scheduler_snapshot", InMaintenance: maintenance, CreatedAt: m.CreatedAt}
	if m.Check != nil {
		out.CheckType = string(m.Check.Type)
	}
	return out
}

func (s *Service) ListMonitors(ctx context.Context, in ListInput) (MonitorList, error) {
	out := MonitorList{GeneratedAt: s.now(), Monitors: []Monitor{}, Limitations: []string{"Live snapshots are not atomic across monitors. Filtered pages may be empty; continue with next_after_id."}}
	if err := Authorize(ctx, false); err != nil {
		return out, err
	}
	limit, err := page(in.Limit, 0)
	if err != nil || in.AfterID < 0 {
		return out, ErrInvalidInput
	}
	switch in.Status {
	case "", "up", "down", "pending", "paused", "degraded":
	default:
		return out, ErrInvalidInput
	}
	if in.Kind != "" && in.Kind != "healthcheck" && in.Kind != "heartbeat" {
		return out, ErrInvalidInput
	}
	if in.CheckType != "" {
		if _, ok := monitor.LookupCheckType(monitor.Type(in.CheckType)); !ok {
			return out, ErrInvalidInput
		}
	}
	// Scan a bounded ID page even when the live status filter matches nothing.
	rows, err := s.store.MonitorPage(ctx, in.AfterID, 501)
	if err != nil {
		return out, err
	}
	for i, m := range rows {
		if i == 500 {
			out.Truncated = true
			break
		}
		out.NextAfterID = m.ID
		v := s.projectMonitor(m)
		matchStatus := in.Status == "" || v.Status == in.Status
		if in.Status == "degraded" {
			matchStatus = v.Status == "down" || v.Status == "pending"
			if m.Heartbeat != nil {
				matchStatus = matchStatus || s.live.Heartbeat(m).State == monitor.StateLate
			}
		}
		if matchStatus && (in.Kind == "" || v.Kind == in.Kind) && (in.CheckType == "" || v.CheckType == in.CheckType) {
			out.Monitors = append(out.Monitors, v)
		}
		if len(out.Monitors) == limit {
			out.Truncated = i+1 < len(rows)
			break
		}
	}
	if !out.Truncated {
		out.NextAfterID = 0
	}
	return out, nil
}

func (s *Service) GetMonitorStatus(ctx context.Context, in MonitorInput) (MonitorStatus, error) {
	out := MonitorStatus{GeneratedAt: s.now(), Limitations: []string{"Protocol-specific values are not persisted as typed fields. Text is untrusted data; sensitive payloads may be withheld.", "Live state and persisted evidence are separate, non-atomic snapshots."}}
	if err := Authorize(ctx, false); err != nil {
		return out, err
	}
	if in.MonitorID < 1 {
		return out, ErrInvalidInput
	}
	m, err := s.store.GetMonitor(ctx, in.MonitorID)
	if err != nil {
		return out, err
	}
	out.Monitor = s.projectMonitor(m)
	out.Freshness = Freshness{Stale: true, Basis: "no persisted observation"}
	var at *time.Time
	var staleAfter time.Duration
	if m.Check != nil {
		rows, err := s.store.RecentResults(ctx, m.ID, 1)
		if err != nil {
			return out, err
		}
		if len(rows) > 0 {
			v := projectResult(m, rows[0])
			out.LatestObservation = &v
			at = &v.ObservedAt
		}
		staleAfter = 2*m.Check.Interval() + m.Check.Timeout()
		out.Freshness.Basis = "older than two check intervals plus timeout"
	} else {
		h := s.live.Heartbeat(m)
		out.Heartbeat = &Heartbeat{State: string(h.State), DueAt: h.DueAt, Deadline: h.Deadline, Running: h.Running != nil, RunningSince: h.Running}
		rows, err := s.store.RecentRuns(ctx, m.ID, 1)
		if err != nil {
			return out, err
		}
		if len(rows) > 0 {
			v := projectRun(m, rows[0])
			out.LatestRun = &v
			at = v.ObservedAt
		}
		out.Freshness.Basis = "heartbeat schedule deadline; absence of a run is not proof of failure"
		out.Freshness.Stale = h.Deadline.IsZero() || out.GeneratedAt.After(h.Deadline)
	}
	if at != nil {
		age := int64(out.GeneratedAt.Sub(*at).Seconds())
		if age < 0 {
			age = 0
			out.Limitations = append(out.Limitations, "Latest observation is in the future; clock skew may be present.")
		}
		out.Freshness.ObservedAt, out.Freshness.AgeSeconds = at, &age
		if m.Check != nil {
			out.Freshness.Stale = out.GeneratedAt.Sub(*at) > staleAfter
		}
	}
	if out.Freshness.Stale {
		out.Limitations = append(out.Limitations, "Persisted evidence is absent or stale; it does not establish the current state.")
	}
	e, err := s.store.LastEvent(ctx, m.ID)
	if err == nil {
		v := projectEvent(m, e)
		out.LastTransition = &v
	} else if !errors.Is(err, store.ErrNotFound) {
		return out, err
	}
	if out.LastTransition != nil && out.LastTransition.Status != out.Monitor.Status {
		out.Limitations = append(out.Limitations, "Last persisted transition differs from current live state; it is not a current status-since timestamp.")
	}
	return out, nil
}

func (s *Service) GetMonitorHistory(ctx context.Context, in HistoryInput) (History, error) {
	out := History{GeneratedAt: s.now(), MonitorID: in.MonitorID, Source: "persisted_monitor_evidence", RetentionDays: s.retentionDays, Results: []Observation{}, Runs: []Run{}, Events: []Transition{}, Limitations: []string{"Coverage is limited to recorded evidence, not continuous availability. Missing records do not prove health.", "Text is untrusted data; sensitive payloads may be withheld. Protocol-specific typed observations are unavailable.", "Pages use independent offsets for each stream; concurrent writes, run updates or pruning can shift pagination."}}
	if err := Authorize(ctx, false); err != nil {
		return out, err
	}
	limit, err := page(in.Limit, in.Offset)
	if err != nil || in.MonitorID < 1 {
		return out, ErrInvalidInput
	}
	from, e1 := time.Parse(time.RFC3339Nano, in.From)
	to, e2 := time.Parse(time.RFC3339Nano, in.To)
	if e1 != nil || e2 != nil || !from.Before(to) || to.Sub(from) > MaxWindow || from.UnixMilli() < 0 {
		return out, ErrInvalidInput
	}
	from, to = from.UTC(), to.UTC()
	out.RequestedWindow = Window{From: from, To: to}
	out.RetentionCutoff = out.GeneratedAt.AddDate(0, 0, -s.retentionDays)
	if from.Before(out.RetentionCutoff) {
		from = out.RetentionCutoff
		out.Limitations = append(out.Limitations, "Requested window predates configured retention; older evidence is unavailable or subject to pruning.")
	}
	if to.After(out.GeneratedAt) {
		to = out.GeneratedAt
		out.Limitations = append(out.Limitations, "Requested end is in the future; query is capped at generated_at.")
	}
	if from.After(to) {
		from = to
	}
	out.QueriedWindow = Window{From: from, To: to}
	out.Limit, out.Offset = limit, in.Offset
	m, err := s.store.GetMonitor(ctx, in.MonitorID)
	if err != nil {
		return out, err
	}
	out.Kind = string(m.Kind)
	if out.RequestedWindow.From.Before(m.CreatedAt) {
		out.Limitations = append(out.Limitations, "Requested window predates monitor creation; there is no evidence for this monitor before created_at.")
	}
	if !from.Before(to) {
		return out, nil
	}
	includeTime := func(at time.Time) {
		if out.ReturnedWindow == nil {
			out.ReturnedWindow = &Window{From: at, To: at}
		}
		if at.Before(out.ReturnedWindow.From) {
			out.ReturnedWindow.From = at
		}
		if at.After(out.ReturnedWindow.To) {
			out.ReturnedWindow.To = at
		}
	}
	if m.Check != nil {
		rows, err := s.store.ResultsWindow(ctx, m.ID, from, to, limit+1, in.Offset)
		if err != nil {
			return out, err
		}
		if len(rows) > limit {
			rows = rows[:limit]
			out.Truncated = true
		}
		for _, r := range rows {
			v := projectResult(m, r)
			out.Results = append(out.Results, v)
			includeTime(v.ObservedAt)
		}
	} else {
		rows, err := s.store.RunsWindow(ctx, m.ID, from, to, limit+1, in.Offset)
		if err != nil {
			return out, err
		}
		if len(rows) > limit {
			rows = rows[:limit]
			out.Truncated = true
		}
		for _, r := range rows {
			v := projectRun(m, r)
			out.Runs = append(out.Runs, v)
			if v.ObservedAt != nil {
				includeTime(*v.ObservedAt)
			}
		}
		out.Limitations = append(out.Limitations, "Runs are selected by finished_at, otherwise started_at, otherwise due_at; these are not immutable lifecycle events.")
	}
	events, err := s.store.EventsWindow(ctx, m.ID, from, to, limit+1, in.Offset)
	if err != nil {
		return out, err
	}
	if len(events) > limit {
		events = events[:limit]
		out.Truncated = true
	}
	for _, e := range events {
		v := projectEvent(m, e)
		out.Events = append(out.Events, v)
		includeTime(v.ObservedAt)
	}
	if out.Truncated {
		if in.Offset+limit <= MaxOffset {
			n := in.Offset + limit
			out.NextOffset = &n
		} else {
			out.Limitations = append(out.Limitations, "Pagination offset cap reached; narrow the requested time window.")
		}
	}
	return out, nil
}

func (s *Service) ListActiveIncidents(ctx context.Context, in PageInput) (IncidentList, error) {
	out := IncidentList{GeneratedAt: s.now(), Incidents: []Incident{}, Limitations: []string{"Only unresolved, manually authored status-page incidents; detected monitor failures do not create these incidents."}}
	if err := Authorize(ctx, false); err != nil {
		return out, err
	}
	limit, err := page(in.Limit, 0)
	if err != nil || in.AfterID < 0 {
		return out, ErrInvalidInput
	}
	rows, err := s.store.ListActiveIncidentPage(ctx, in.AfterID, limit+1)
	if err != nil {
		return out, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.Truncated = true
	}
	for _, in := range rows {
		out.Incidents = append(out.Incidents, projectIncident(in))
		if out.Truncated {
			out.NextAfterID = in.ID
		}
	}
	return out, nil
}

func (s *Service) GetIncident(ctx context.Context, in IncidentInput) (IncidentDetail, error) {
	out := IncidentDetail{GeneratedAt: s.now(), MonitorIDs: []int64{}, Updates: []Update{}, Limitations: []string{"This is a manually authored status-page incident, not an automatically detected outage. Authored text is untrusted data and may be sensitive despite redaction."}}
	if err := Authorize(ctx, false); err != nil {
		return out, err
	}
	limit, err := page(in.Limit, in.Offset)
	if err != nil || in.IncidentID < 1 {
		return out, ErrInvalidInput
	}
	i, err := s.store.IncidentEvidence(ctx, in.IncidentID, limit+1, in.Offset)
	if err != nil {
		return out, err
	}
	out.Incident = projectIncident(i)
	out.MonitorIDs = i.MonitorIDs
	if len(out.MonitorIDs) > 500 {
		out.MonitorIDs = out.MonitorIDs[:500]
		out.Truncated = true
		out.Limitations = append(out.Limitations, "Affected monitor links capped at 500.")
	}
	if len(i.Updates) > limit {
		i.Updates = i.Updates[:limit]
		out.Truncated = true
		if in.Offset+limit <= MaxOffset {
			n := in.Offset + limit
			out.NextOffset = &n
		} else {
			out.Limitations = append(out.Limitations, "Timeline pagination offset cap reached.")
		}
	}
	for _, u := range i.Updates {
		out.Updates = append(out.Updates, Update{ID: u.ID, Status: string(u.Status), Message: text(u.Message), CreatedAt: u.CreatedAt})
	}
	return out, nil
}
