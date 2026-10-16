package store

import (
	"context"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/monitor"
)

func TestResultsKeepStatusCodeAndErrorClass(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	m := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: "api", Check: &monitor.Check{Type: monitor.TypeHTTP, Target: "https://api.example.com"}}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	m, err := st.CreateMonitor(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	want := []monitor.Result{
		{MonitorID: m.ID, Time: at, OK: true, LatencyMS: 12, Message: "200 OK", StatusCode: 200},
		{MonitorID: m.ID, Time: at.Add(time.Second), LatencyMS: 30, Message: "unexpected status 503", StatusCode: 503, ErrorClass: "http_5xx"},
		{MonitorID: m.ID, Time: at.Add(2 * time.Second), LatencyMS: 5000, Message: "timed out after 5s", ErrorClass: "timeout"},
	}
	for _, r := range want {
		if err := st.InsertResult(ctx, r); err != nil {
			t.Fatal(err)
		}
	}

	recent, err := st.RecentResults(ctx, m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	since, err := st.ResultsSince(ctx, m.ID, at)
	if err != nil {
		t.Fatal(err)
	}
	after, err := st.ResultsAfter(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string][]monitor.Result{"recent": recent, "since": since, "after": resultsOf(after)} {
		if len(got) != len(want) {
			t.Fatalf("%s: %d results, want %d", name, len(got), len(want))
		}
		for i := range want {
			if !got[i].Time.Equal(want[i].Time) || got[i].StatusCode != want[i].StatusCode || got[i].ErrorClass != want[i].ErrorClass || got[i].OK != want[i].OK {
				t.Fatalf("%s[%d] = %+v, want %+v", name, i, got[i], want[i])
			}
		}
	}
}

func resultsOf(rows []ResultRow) []monitor.Result {
	out := make([]monitor.Result, len(rows))
	for i, r := range rows {
		out[i] = r.Result
	}
	return out
}
