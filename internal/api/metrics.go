package api

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/uptimy/agent/internal/monitor"
)

// metrics serves Prometheus metrics in the text exposition format: each
// monitor's status and check results, heartbeat runs, discovery and the
// agent itself. It's behind the same auth as the API, so Prometheus sends
// an API token (a read-only one is enough). Targets aren't exported: they
// can hold credentials.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.Store.ListMonitors(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	sort.Slice(monitors, func(i, j int) bool { return monitors[i].ID < monitors[j].ID })
	stats := s.Scheduler.Stats()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var b strings.Builder
	m := &promWriter{w: &b}

	m.family("uptimy_agent_info", "gauge", "The agent's version.")
	m.sample("uptimy_agent_info", labels{"version", s.Version}, 1)

	m.family("uptimy_agent_monitor_up", "gauge", "1 if the monitor is up, 0 if down. Not set while pending or paused.")
	for _, mon := range monitors {
		switch s.Scheduler.Status(mon) {
		case monitor.StatusUp:
			m.sample("uptimy_agent_monitor_up", monitorLabels(mon), 1)
		case monitor.StatusDown:
			m.sample("uptimy_agent_monitor_up", monitorLabels(mon), 0)
		}
	}

	statuses := []monitor.Status{monitor.StatusUp, monitor.StatusDown, monitor.StatusPending, monitor.StatusPaused}
	m.family("uptimy_agent_monitor_status", "gauge", "The monitor's status: 1 for the current one of up, down, pending and paused.")
	for _, mon := range monitors {
		cur := s.Scheduler.Status(mon)
		for _, st := range statuses {
			m.sample("uptimy_agent_monitor_status", append(monitorLabels(mon), "status", string(st)), boolValue(cur == st))
		}
	}

	m.family("uptimy_agent_monitor_in_maintenance", "gauge", "1 while a maintenance window covers the monitor.")
	for _, mon := range monitors {
		m.sample("uptimy_agent_monitor_in_maintenance", monitorLabels(mon), boolValue(s.Scheduler.InMaintenance(mon.ID)))
	}

	m.family("uptimy_agent_checks_total", "counter", "Healthcheck probes since the agent started, by result.")
	for _, mon := range monitors {
		if mon.Kind != monitor.KindHealthcheck {
			continue
		}
		st := stats[mon.ID]
		m.sample("uptimy_agent_checks_total", append(monitorLabels(mon), "result", "success"), float64(st.Successes))
		m.sample("uptimy_agent_checks_total", append(monitorLabels(mon), "result", "failure"), float64(st.Failures))
	}

	m.family("uptimy_agent_check_duration_seconds", "gauge", "How long the last healthcheck probe took.")
	m.family("uptimy_agent_check_last_timestamp_seconds", "gauge", "When the last healthcheck probe ran, as a Unix timestamp.")
	for _, mon := range monitors {
		if st, ok := stats[mon.ID]; ok && mon.Kind == monitor.KindHealthcheck && !st.LastCheck.IsZero() {
			m.sample("uptimy_agent_check_duration_seconds", monitorLabels(mon), st.LastLatency.Seconds())
			m.sample("uptimy_agent_check_last_timestamp_seconds", monitorLabels(mon), unix(st.LastCheck))
		}
	}

	m.family("uptimy_agent_heartbeat_runs_total", "counter", "Heartbeat runs recorded since the agent started, by outcome.")
	m.family("uptimy_agent_heartbeat_next_due_timestamp_seconds", "gauge", "When the heartbeat's next run is due, as a Unix timestamp.")
	m.family("uptimy_agent_heartbeat_running", "gauge", "1 while a run that reported its start hasn't finished.")
	for _, mon := range monitors {
		if mon.Kind != monitor.KindHeartbeat {
			continue
		}
		st := stats[mon.ID]
		for _, o := range []monitor.RunOutcome{monitor.OutcomeSuccess, monitor.OutcomeFailure, monitor.OutcomeMissed} {
			m.sample("uptimy_agent_heartbeat_runs_total", append(monitorLabels(mon), "outcome", string(o)), float64(st.Runs[o]))
		}
		hb := s.Scheduler.Heartbeat(mon)
		if !hb.DueAt.IsZero() {
			m.sample("uptimy_agent_heartbeat_next_due_timestamp_seconds", monitorLabels(mon), unix(hb.DueAt))
		}
		m.sample("uptimy_agent_heartbeat_running", monitorLabels(mon), boolValue(hb.Running != nil))
	}

	if s.Discovery != nil {
		d := s.Discovery.Status()
		m.family("uptimy_agent_discovery_monitors", "gauge", "Monitors Kubernetes discovery found in its last good scan.")
		m.sample("uptimy_agent_discovery_monitors", nil, float64(d.Monitors))
		m.family("uptimy_agent_discovery_problems", "gauge", "Problems the last scan found (bad labels or annotations, missing RBAC, a failed scan).")
		problems := len(d.Warnings)
		if d.Error != "" {
			problems++
		}
		m.sample("uptimy_agent_discovery_problems", nil, float64(problems))
		if !d.LastScan.IsZero() {
			m.family("uptimy_agent_discovery_last_scan_timestamp_seconds", "gauge", "When Kubernetes discovery last scanned, as a Unix timestamp.")
			m.sample("uptimy_agent_discovery_last_scan_timestamp_seconds", nil, unix(d.LastScan))
		}
	}

	if s.Alerts != nil {
		m.family("uptimy_agent_alert_deliveries_total", "counter",
			"Alert delivery attempts since the agent started, by channel type and result (sent, retry, failed, expired, suppressed).")
		stats := s.Alerts.Stats()
		sort.Slice(stats, func(i, j int) bool {
			if stats[i].ChannelType != stats[j].ChannelType {
				return stats[i].ChannelType < stats[j].ChannelType
			}
			return stats[i].Result < stats[j].Result
		})
		for _, st := range stats {
			m.sample("uptimy_agent_alert_deliveries_total", labels{"channel_type", st.ChannelType, "result", st.Result}, float64(st.Count))
		}
		health, err := s.Store.ChannelHealth(r.Context(), time.Now())
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		pending := 0
		for _, h := range health {
			pending += h.Pending
		}
		m.family("uptimy_agent_alert_deliveries_pending", "gauge", "Alert deliveries waiting to be sent, retries included.")
		m.sample("uptimy_agent_alert_deliveries_pending", nil, float64(pending))
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	m.family("uptimy_agent_go_goroutines", "gauge", "Goroutines in the agent.")
	m.sample("uptimy_agent_go_goroutines", nil, float64(runtime.NumGoroutine()))
	m.family("uptimy_agent_go_memory_heap_bytes", "gauge", "Heap memory in use.")
	m.sample("uptimy_agent_go_memory_heap_bytes", nil, float64(mem.HeapAlloc))
	m.family("uptimy_agent_go_memory_sys_bytes", "gauge", "Memory obtained from the OS.")
	m.sample("uptimy_agent_go_memory_sys_bytes", nil, float64(mem.Sys))

	_, _ = io.WriteString(w, b.String())
}

// labels is a flat name/value list: {"name", "API", "kind", "healthcheck"}.
type labels []string

func monitorLabels(m monitor.Monitor) labels {
	typ := "heartbeat"
	if m.Check != nil {
		typ = string(m.Check.Type)
	}
	return labels{"id", strconv.FormatInt(m.ID, 10), "name", m.Name, "kind", string(m.Kind), "type", typ, "source", m.Source}
}

type promWriter struct{ w *strings.Builder }

func (p *promWriter) family(name, kind, help string) {
	fmt.Fprintf(p.w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}

func (p *promWriter) sample(name string, l labels, v float64) {
	p.w.WriteString(name)
	if len(l) > 0 {
		p.w.WriteByte('{')
		for i := 0; i+1 < len(l); i += 2 {
			if i > 0 {
				p.w.WriteByte(',')
			}
			p.w.WriteString(l[i])
			p.w.WriteString(`="`)
			p.w.WriteString(escapeLabel(l[i+1]))
			p.w.WriteByte('"')
		}
		p.w.WriteByte('}')
	}
	p.w.WriteByte(' ')
	p.w.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	p.w.WriteByte('\n')
}

// escapeLabel escapes a label value: backslash, double quote and newline.
func escapeLabel(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func unix(t time.Time) float64 { return float64(t.UnixMilli()) / 1000 }
