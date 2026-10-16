package railway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uptimy/agent/internal/monitor"
)

const testToken = "11111111-2222-3333-4444-555555555555"

// fakeRailway answers the importer's queries like Railway's API, for a
// project with an API, a worker, a cron job, Postgres, the agent itself and
// a service in another environment. endpoints are the private DNS names by
// service ID; nil makes the private network lookups fail.
func fakeRailway(t *testing.T, endpoints map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		if strings.Contains(req.Query, "variables") {
			t.Errorf("queried variables: %s", req.Query)
		}
		answer := func(data string) { w.Write([]byte(`{"data":` + data + `}`)) }
		fail := func(msg string) { w.Write([]byte(`{"data":null,"errors":[{"message":"` + msg + `"}]}`)) }
		if r.Header.Get("Project-Access-Token") != testToken {
			fail("Project Token not found")
			return
		}
		switch {
		case strings.Contains(req.Query, "projectToken"):
			answer(`{"projectToken":{"projectId":"proj","environmentId":"prod"}}`)
		case strings.Contains(req.Query, "serviceInstances"):
			if req.Variables["project"] != "proj" || req.Variables["environment"] != "prod" {
				t.Errorf("services for %v", req.Variables)
			}
			answer(`{"project":{"name":"Shop"},"environment":{"name":"production","serviceInstances":{"edges":[
				{"node":{"serviceId":"api","serviceName":"API","environmentId":"prod","healthcheckPath":"/healthz","source":{"repo":"acme/api"},
					"domains":{"serviceDomains":[{"domain":"api-production.up.railway.app","targetPort":8080}],"customDomains":[{"domain":"api.acme.com","targetPort":null}]}}},
				{"node":{"serviceId":"worker","serviceName":"Queue Worker","environmentId":"prod","source":{"repo":"acme/worker"},"domains":{"serviceDomains":[],"customDomains":[]}}},
				{"node":{"serviceId":"cron","serviceName":"Nightly backup","environmentId":"prod","cronSchedule":"0 3 * * *","source":{"image":"alpine"},"domains":{}}},
				{"node":{"serviceId":"pg","serviceName":"Postgres","environmentId":"prod","source":{"image":"ghcr.io/railwayapp-templates/postgres-ssl:16"},"domains":{}}},
				{"node":{"serviceId":"agent","serviceName":"uptimy-agent","environmentId":"prod","source":{"image":"ghcr.io/uptimy/agent:latest"},
					"domains":{"serviceDomains":[{"domain":"agent.up.railway.app","targetPort":8080}]}}},
				{"node":{"serviceId":"staging-api","serviceName":"Staging API","environmentId":"staging","domains":{}}}
			]}}}`)
		case strings.Contains(req.Query, "privateNetworks"):
			if endpoints == nil {
				fail("Not Authorized")
				return
			}
			answer(`{"privateNetworks":[{"publicId":"net-old","dnsName":"old.internal","deletedAt":"2025-01-01T00:00:00Z"},{"publicId":"net","dnsName":"railway.internal","deletedAt":null}]}`)
		case strings.Contains(req.Query, "privateNetworkEndpoint"):
			if req.Variables["network"] != "net" {
				t.Errorf("endpoint on network %q", req.Variables["network"])
			}
			dns, ok := endpoints[req.Variables["service"]]
			if !ok {
				answer(`{"privateNetworkEndpoint":null}`)
				return
			}
			answer(`{"privateNetworkEndpoint":{"dnsName":"` + dns + `","deletedAt":null}}`)
		default:
			t.Errorf("unexpected query: %s", req.Query)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func plan(t *testing.T, endpoints map[string]string, self Self) (Plan, map[string]Service, map[string]string) {
	t.Helper()
	p, err := Read(context.Background(), NewClient(fakeRailway(t, endpoints).URL, testToken), self)
	if err != nil {
		t.Fatal(err)
	}
	byName, skipped := map[string]Service{}, map[string]string{}
	for _, s := range p.Services {
		byName[s.Monitor.Name] = s
	}
	for _, s := range p.Skipped {
		skipped[s.Name] = s.Reason
	}
	return p, byName, skipped
}

var inProject = Self{ProjectID: "proj", EnvironmentID: "prod", ServiceID: "agent"}

func TestReadInProject(t *testing.T) {
	p, byName, skipped := plan(t, map[string]string{"api": "api"}, inProject)
	if p.ProjectName != "Shop" || p.EnvironmentName != "production" || !p.SameProject {
		t.Fatalf("plan: %+v", p)
	}
	if len(p.Services) != 4 || len(p.Skipped) != 1 || skipped["uptimy-agent"] != "this agent" {
		t.Fatalf("services %+v, skipped %+v", p.Services, p.Skipped)
	}

	api := byName["API"]
	if api.Address != "private" || api.Monitor.Check.Target != "http://api.railway.internal:8080/healthz" ||
		api.PublicHost != "api.acme.com" || api.Monitor.SourceRef != "railway:prod/api" || api.Monitor.Source != monitor.SourceUI ||
		api.Monitor.Check.IntervalSeconds != 60 || len(api.Notes) != 0 {
		t.Fatalf("api: %+v %+v", api, api.Monitor.Check)
	}
	// No domain, so no port, and no private endpoint: the name is used.
	worker := byName["Queue Worker"]
	if worker.Address != "private" || worker.Port != 0 || worker.Monitor.Check.Target != "" || worker.Path != "/" ||
		worker.PrivateHost != "queue-worker.railway.internal" || len(worker.Notes) != 2 {
		t.Fatalf("worker: %+v", worker)
	}
	backup := byName["Nightly backup"].Monitor
	if backup.Kind != monitor.KindHeartbeat || backup.Heartbeat.Cron != "0 3 * * *" || backup.Heartbeat.Timezone != "UTC" ||
		backup.Heartbeat.GraceSeconds != 300 || !strings.Contains(byName["Nightly backup"].Notes[0], "curl -fsS") {
		t.Fatalf("cron: %+v", backup.Heartbeat)
	}
	pg := byName["Postgres"]
	if pg.Monitor.Check.Type != monitor.TypePostgres || pg.Monitor.Check.Target != "${POSTGRES_URL}" ||
		pg.Notes[0] != "add the variable POSTGRES_URL=${{Postgres.DATABASE_URL}} to the agent" {
		t.Fatalf("postgres: %+v %v", pg.Monitor.Check, pg.Notes)
	}
}

func TestReadElsewhere(t *testing.T) {
	// The agent runs outside the project: public domains only.
	p, byName, skipped := plan(t, nil, Self{})
	if p.SameProject || len(p.Services) != 4 {
		t.Fatalf("plan: %+v", p)
	}
	if api := byName["API"]; api.Address != "public" || api.Monitor.Check.Target != "https://api.acme.com/healthz" || api.PrivateHost != "" {
		t.Fatalf("api: %+v", api)
	}
	if agent := byName["uptimy-agent"]; agent.Monitor.Check.Target != "https://agent.up.railway.app/" {
		t.Fatalf("agent: %+v", agent)
	}
	if !strings.Contains(skipped["Queue Worker"], "no public domain") {
		t.Fatalf("worker skipped for %q", skipped["Queue Worker"])
	}
	if note := byName["Postgres"].Notes[0]; !strings.Contains(note, "DATABASE_PUBLIC_URL") {
		t.Fatalf("postgres note: %q", note)
	}
}

func TestPrivateHosts(t *testing.T) {
	// A full hostname is kept; a failed lookup falls back to the name.
	_, byName, _ := plan(t, map[string]string{"api": "backend.railway.internal", "worker": "jobs"}, inProject)
	if h := byName["API"].PrivateHost; h != "backend.railway.internal" {
		t.Fatalf("api host %q", h)
	}
	if w := byName["Queue Worker"]; w.PrivateHost != "jobs.railway.internal" || len(w.Notes) != 1 {
		t.Fatalf("worker: %+v", w)
	}
	_, byName, _ = plan(t, nil, inProject)
	if h := byName["API"].PrivateHost; h != "api.railway.internal" || !strings.Contains(byName["API"].Notes[0], "guessed") {
		t.Fatalf("fallback: %+v", byName["API"])
	}
}

func TestReadRejectsToken(t *testing.T) {
	_, err := Read(context.Background(), NewClient(fakeRailway(t, nil).URL, "wrong"), Self{})
	if !errors.Is(err, ErrToken) {
		t.Fatalf("err: %v", err)
	}
}

func TestEnvName(t *testing.T) {
	for in, want := range map[string]string{"Postgres": "POSTGRES", "my-db (main)": "MY_DB_MAIN", "1db": "DB_1DB", "redis": "REDIS"} {
		if got := envName(in); got != want {
			t.Errorf("envName(%q) = %q, want %q", in, got, want)
		}
	}
}
