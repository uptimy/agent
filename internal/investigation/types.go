package investigation

import "time"

type PageInput struct {
	Limit   int   `json:"limit,omitempty" jsonschema:"Page size, default 100, maximum 200"`
	AfterID int64 `json:"after_id,omitempty" jsonschema:"Exclusive ID cursor, default zero"`
}

type ListInput struct {
	Status    string `json:"status,omitempty" jsonschema:"up, down, pending, paused or degraded (down or pending or late heartbeat); omitted means all"`
	Kind      string `json:"kind,omitempty" jsonschema:"healthcheck or heartbeat; omitted means all"`
	CheckType string `json:"check_type,omitempty" jsonschema:"Healthcheck type, for example http or postgres"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Page size, default 100, maximum 200"`
	AfterID   int64  `json:"after_id,omitempty" jsonschema:"Exclusive ID cursor from next_after_id"`
}

type MonitorInput struct {
	MonitorID int64 `json:"monitor_id" jsonschema:"Existing monitor ID"`
}
type IncidentInput struct {
	IncidentID int64 `json:"incident_id" jsonschema:"Manually authored incident ID"`
	Limit      int   `json:"limit,omitempty" jsonschema:"Timeline page size, default 100, maximum 200"`
	Offset     int   `json:"offset,omitempty" jsonschema:"Timeline offset, maximum 10000"`
}
type HistoryInput struct {
	MonitorID int64  `json:"monitor_id" jsonschema:"Existing monitor ID; no manual incident is required"`
	From      string `json:"from" jsonschema:"Inclusive RFC3339 timestamp with timezone"`
	To        string `json:"to" jsonschema:"Exclusive RFC3339 timestamp with timezone; window maximum 31 days"`
	Limit     int    `json:"limit,omitempty" jsonschema:"Page size per evidence stream, default 100, maximum 200"`
	Offset    int    `json:"offset,omitempty" jsonschema:"Offset applied independently to results, runs and events; maximum 10000"`
}

type Text struct {
	Value          string `json:"value"`
	Classification string `json:"classification"`
	Redacted       bool   `json:"redacted"`
	Truncated      bool   `json:"truncated"`
}
type Monitor struct {
	ID            int64     `json:"id"`
	Name          Text      `json:"name"`
	Kind          string    `json:"kind"`
	CheckType     string    `json:"check_type,omitempty"`
	Status        string    `json:"status"`
	StatusSource  string    `json:"status_source"`
	InMaintenance bool      `json:"in_maintenance"`
	CreatedAt     time.Time `json:"created_at"`
}
type MonitorList struct {
	GeneratedAt time.Time `json:"generated_at"`
	Monitors    []Monitor `json:"monitors"`
	NextAfterID int64     `json:"next_after_id,omitempty"`
	Truncated   bool      `json:"truncated"`
	Limitations []string  `json:"limitations"`
}
type Observation struct {
	ObservedAt time.Time `json:"observed_at"`
	Source     string    `json:"source"`
	OK         bool      `json:"ok"`
	LatencyMS  int64     `json:"latency_ms"`
	Message    Text      `json:"message"`
}
type Run struct {
	ID         int64      `json:"id"`
	ObservedAt *time.Time `json:"observed_at"`
	Source     string     `json:"source"`
	DueAt      *time.Time `json:"due_at"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Outcome    string     `json:"outcome"`
	OnTime     bool       `json:"on_time"`
	DurationMS *int64     `json:"duration_ms"`
	Message    Text       `json:"message"`
}
type Transition struct {
	ID         int64     `json:"id"`
	ObservedAt time.Time `json:"observed_at"`
	Source     string    `json:"source"`
	Status     string    `json:"status"`
	Message    Text      `json:"message"`
}
type Heartbeat struct {
	State        string     `json:"state"`
	DueAt        time.Time  `json:"due_at"`
	Deadline     time.Time  `json:"deadline"`
	Running      bool       `json:"running"`
	RunningSince *time.Time `json:"running_since"`
}
type Freshness struct {
	ObservedAt *time.Time `json:"observed_at"`
	AgeSeconds *int64     `json:"age_seconds"`
	Stale      bool       `json:"stale"`
	Basis      string     `json:"basis"`
}
type MonitorStatus struct {
	GeneratedAt       time.Time    `json:"generated_at"`
	Monitor           Monitor      `json:"monitor"`
	LatestObservation *Observation `json:"latest_observation"`
	LatestRun         *Run         `json:"latest_run"`
	Heartbeat         *Heartbeat   `json:"heartbeat"`
	LastTransition    *Transition  `json:"last_transition"`
	Freshness         Freshness    `json:"freshness"`
	Limitations       []string     `json:"limitations"`
}
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}
type History struct {
	GeneratedAt     time.Time     `json:"generated_at"`
	MonitorID       int64         `json:"monitor_id"`
	Kind            string        `json:"kind"`
	Source          string        `json:"source"`
	RequestedWindow Window        `json:"requested_window"`
	QueriedWindow   Window        `json:"queried_window"`
	ReturnedWindow  *Window       `json:"returned_window"`
	RetentionDays   int           `json:"retention_days"`
	RetentionCutoff time.Time     `json:"retention_cutoff"`
	Results         []Observation `json:"results"`
	Runs            []Run         `json:"runs"`
	Events          []Transition  `json:"events"`
	Limit           int           `json:"limit_per_stream"`
	Offset          int           `json:"offset"`
	NextOffset      *int          `json:"next_offset"`
	Truncated       bool          `json:"truncated"`
	Limitations     []string      `json:"limitations"`
}
type Incident struct {
	ID         int64      `json:"id"`
	Title      Text       `json:"title"`
	Severity   string     `json:"severity"`
	Status     string     `json:"status"`
	Source     string     `json:"source"`
	CreatedAt  time.Time  `json:"created_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
}
type IncidentList struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Incidents   []Incident `json:"incidents"`
	NextAfterID int64      `json:"next_after_id,omitempty"`
	Truncated   bool       `json:"truncated"`
	Limitations []string   `json:"limitations"`
}
type Update struct {
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	Message   Text      `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}
type IncidentDetail struct {
	GeneratedAt time.Time `json:"generated_at"`
	Incident    Incident  `json:"incident"`
	MonitorIDs  []int64   `json:"monitor_ids"`
	Updates     []Update  `json:"updates"`
	NextOffset  *int      `json:"next_offset"`
	Truncated   bool      `json:"truncated"`
	Limitations []string  `json:"limitations"`
}
