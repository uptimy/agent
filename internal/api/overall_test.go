package api

import (
	"testing"
	"time"

	"github.com/uptimy/agent/internal/incident"
)

func TestOverallStatus(t *testing.T) {
	now := time.Now()
	open := func(sev incident.Severity) publicIncident { return publicIncident{Severity: sev} }
	resolved := publicIncident{Severity: incident.Critical, ResolvedAt: &now}
	active := []publicMaintenance{{Active: true}}

	cases := []struct {
		name        string
		anyDown     bool
		incidents   []publicIncident
		maintenance []publicMaintenance
		want        string
	}{
		{"nothing", false, nil, nil, "operational"},
		{"resolved incident only", false, []publicIncident{resolved}, nil, "operational"},
		{"monitor down", true, nil, nil, "outage"},
		{"low incident", false, []publicIncident{open(incident.Low)}, nil, "degraded"},
		{"high incident", false, []publicIncident{open(incident.High)}, nil, "outage"},
		{"maintenance", false, nil, active, "maintenance"},
		{"low incident during maintenance", false, []publicIncident{open(incident.Low)}, active, "maintenance"},
		{"critical incident during maintenance", false, []publicIncident{open(incident.Critical)}, active, "outage"},
		{"monitor down outside maintenance", true, nil, active, "outage"},
	}
	for _, c := range cases {
		if got := overallStatus(c.anyDown, c.incidents, c.maintenance); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
