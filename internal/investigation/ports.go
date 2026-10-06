package investigation

import (
	"context"
	"time"

	"github.com/uptimy/agent/internal/incident"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/scheduler"
)

type Repository interface {
	GetMonitor(context.Context, int64) (monitor.Monitor, error)
	MonitorPage(context.Context, int64, int) ([]monitor.Monitor, error)
	RecentResults(context.Context, int64, int) ([]monitor.Result, error)
	RecentRuns(context.Context, int64, int) ([]monitor.Run, error)
	LastEvent(context.Context, int64) (monitor.Event, error)
	ResultsWindow(context.Context, int64, time.Time, time.Time, int, int) ([]monitor.Result, error)
	RunsWindow(context.Context, int64, time.Time, time.Time, int, int) ([]monitor.Run, error)
	EventsWindow(context.Context, int64, time.Time, time.Time, int, int) ([]monitor.Event, error)
	ListActiveIncidentPage(context.Context, int64, int) ([]incident.Incident, error)
	IncidentEvidence(context.Context, int64, int, int) (incident.Incident, error)
}

type LiveState interface {
	Status(monitor.Monitor) monitor.Status
	Heartbeat(monitor.Monitor) scheduler.HeartbeatStatus
	InMaintenance(int64) bool
}

// CurrentView is shared by REST summaries and evidence tools. It never derives
// live status from persisted probe success, which ignores failure thresholds.
func CurrentView(live LiveState, m monitor.Monitor) (monitor.Status, bool) {
	return live.Status(m), live.InMaintenance(m.ID)
}
