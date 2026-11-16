package render

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/railway"
)

const testKey = "rnd_test"

// fakeRender answers like Render's API for a workspace in Oregon with a web
// service, a free web service, a private service, a worker, a cron job, a
// static site, a suspended service, the agent itself and a web service in
// another region.
func fakeRender(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("limit") != "100" {
			t.Errorf("limit %q on %s", r.URL.Query().Get("limit"), r.URL.Path)
		}
		switch r.URL.Path {
		case "/owners":
			w.Write([]byte(`[{"owner":{"id":"tea-1","name":"Acme","type":"team"},"cursor":"c1"}]`))
		case "/services":
			if r.URL.Query().Get("includePreviews") != "false" {
				t.Errorf("previews included")
			}
			w.Write([]byte(`[
				{"service":{"id":"srv-api","name":"API","slug":"api-x1y2","ownerId":"tea-1","type":"web_service","suspended":"not_suspended",
					"serviceDetails":{"url":"https://api-x1y2.onrender.com","region":"oregon","plan":"starter","healthCheckPath":"/healthz","openPorts":[{"port":3000,"protocol":"TCP"}]}}},
				{"service":{"id":"srv-free","name":"Docs","slug":"docs-a1","ownerId":"tea-1","type":"web_service","suspended":"not_suspended",
					"serviceDetails":{"url":"https://docs-a1.onrender.com","region":"oregon","plan":"free"}}},
				{"service":{"id":"srv-priv","name":"Search","slug":"search-q9","ownerId":"tea-1","type":"private_service","suspended":"not_suspended",
					"serviceDetails":{"url":"search-q9:9200","region":"oregon","plan":"starter","openPorts":[{"port":9200,"protocol":"TCP"}]}}},
				{"service":{"id":"srv-worker","name":"Queue Worker","slug":"worker-z","ownerId":"tea-1","type":"background_worker","suspended":"not_suspended","serviceDetails":{"region":"oregon"}}},
				{"service":{"id":"crn-backup","name":"Nightly backup","slug":"backup","ownerId":"tea-1","type":"cron_job","suspended":"not_suspended","serviceDetails":{"schedule":"0 3 * * *","region":"oregon"}}},
				{"service":{"id":"srv-site","name":"Site","slug":"site","ownerId":"tea-1","type":"static_site","suspended":"not_suspended","serviceDetails":{"url":"https://site.onrender.com"}}},
				{"service":{"id":"srv-old","name":"Old","slug":"old","ownerId":"tea-1","type":"web_service","suspended":"suspended","serviceDetails":{"url":"https://old.onrender.com","region":"oregon"}}},
				{"service":{"id":"srv-agent","name":"uptimy-agent","slug":"agent","ownerId":"tea-1","type":"web_service","suspended":"not_suspended","serviceDetails":{"url":"https://agent.onrender.com","region":"oregon","plan":"starter"}}},
				{"service":{"id":"srv-eu","name":"EU API","slug":"eu-api","ownerId":"tea-1","type":"web_service","suspended":"not_suspended","serviceDetails":{"url":"https://eu-api.onrender.com","region":"frankfurt","plan":"starter"}}}
			]`))
		case "/services/srv-api/custom-domains":
			w.Write([]byte(`[{"customDomain":{"name":"api.acme.com","verificationStatus":"verified"},"cursor":"d1"},{"customDomain":{"name":"new.acme.com","verificationStatus":"unverified"},"cursor":"d2"}]`))
		default:
			if strings.HasSuffix(r.URL.Path, "/custom-domains") {
				w.Write([]byte(`[]`))
				return
			}
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func plan(t *testing.T, self Self) (Plan, map[string]railway.Service, map[string]string) {
	t.Helper()
	p, err := Read(context.Background(), NewClient(fakeRender(t).URL, testKey), self)
	if err != nil {
		t.Fatal(err)
	}
	byName, skipped := map[string]railway.Service{}, map[string]string{}
	for _, s := range p.Services {
		byName[s.Monitor.Name] = s
	}
	for _, s := range p.Skipped {
		skipped[s.Name] = s.Reason
	}
	return p, byName, skipped
}

func TestReadOnRender(t *testing.T) {
	p, byName, skipped := plan(t, Self{ServiceID: "srv-agent"})
	if p.ProjectName != "Acme" || !p.SameProject {
		t.Fatalf("plan: %+v", p)
	}
	if skipped["uptimy-agent"] != "this agent" || skipped["Old"] != "suspended" || !strings.Contains(skipped["Queue Worker"], "background worker") {
		t.Fatalf("skipped: %v", skipped)
	}

	// Public by default, at the verified custom domain; private offered.
	api := byName["API"]
	if api.Address != "public" || api.Monitor.Check.Target != "https://api.acme.com/healthz" || api.PrivateHost != "api-x1y2" ||
		api.Port != 3000 || api.Monitor.SourceRef != "render:srv-api" || api.Monitor.Source != monitor.SourceUI {
		t.Fatalf("api: %+v %+v", api, api.Monitor.Check)
	}
	// Free web services can't receive private traffic.
	if docs := byName["Docs"]; docs.PrivateHost != "" || docs.Monitor.Check.Target != "https://docs-a1.onrender.com/" {
		t.Fatalf("docs: %+v", docs)
	}
	search := byName["Search"]
	if search.Address != "private" || search.Monitor.Check.Target != "http://search-q9:9200/" || len(search.Notes) != 1 {
		t.Fatalf("search: %+v", search)
	}
	backup := byName["Nightly backup"].Monitor
	if backup.Kind != monitor.KindHeartbeat || backup.Heartbeat.Cron != "0 3 * * *" || backup.Heartbeat.Timezone != "UTC" {
		t.Fatalf("cron: %+v", backup.Heartbeat)
	}
	if site := byName["Site"]; site.Monitor.Check.Target != "https://site.onrender.com/" || site.PrivateHost != "" {
		t.Fatalf("site: %+v", site)
	}
	// Another region is another private network.
	if eu := byName["EU API"]; eu.PrivateHost != "" || eu.Address != "public" {
		t.Fatalf("eu: %+v", eu)
	}
}

func TestReadElsewhere(t *testing.T) {
	p, byName, skipped := plan(t, Self{})
	if p.SameProject {
		t.Fatalf("plan: %+v", p)
	}
	if api := byName["API"]; api.PrivateHost != "" || api.Monitor.Check.Target != "https://api.acme.com/healthz" {
		t.Fatalf("api: %+v", api)
	}
	if !strings.Contains(skipped["Search"], "private service") {
		t.Fatalf("search skipped for %q", skipped["Search"])
	}
	if _, ok := byName["uptimy-agent"]; !ok {
		t.Fatalf("an agent elsewhere monitors the Render one too: %v", skipped)
	}
}

func TestReadRejectsKey(t *testing.T) {
	_, err := Read(context.Background(), NewClient(fakeRender(t).URL, "wrong"), Self{})
	if !errors.Is(err, ErrToken) {
		t.Fatalf("err: %v", err)
	}
}
