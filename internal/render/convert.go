package render

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/railway"
)

// webPort is the port Render routes a web service's private traffic to when
// it doesn't list its open ports.
const webPort = 10000

// Plan is what the services become. Nothing is created until it's applied,
// so people can review it first. It has the shape of railway.Plan, so the
// UI reviews both the same way.
type Plan struct {
	// ProjectName is the Render workspaces the services come from.
	ProjectName     string `json:"project_name"`
	EnvironmentName string `json:"environment_name"`
	// SameProject: the agent runs on Render in the services' workspace and
	// region, so it can reach them over the private network.
	SameProject bool              `json:"same_project"`
	Services    []railway.Service `json:"services"`
	Skipped     []railway.Skipped `json:"skipped"`
}

// SourceRef identifies an imported service's monitor, so importing again
// skips it.
func SourceRef(serviceID string) string {
	return "render:" + serviceID
}

// Read plans monitors for the services the key can see. self is where the
// agent runs (SelfFromEnv).
func Read(ctx context.Context, c *Client, self Self) (Plan, error) {
	owners, err := c.owners(ctx)
	if err != nil {
		return Plan{}, err
	}
	services, err := c.services(ctx)
	if err != nil {
		return Plan{}, err
	}
	names := make([]string, len(owners))
	for i, o := range owners {
		names[i] = o.Name
	}
	plan := Plan{ProjectName: strings.Join(names, ", "), Services: []railway.Service{}, Skipped: []railway.Skipped{}}

	// The private network spans one workspace in one region
	var network struct{ owner, region string }
	for _, s := range services {
		if self.ServiceID != "" && s.ID == self.ServiceID {
			network.owner, network.region = s.OwnerID, s.Details.Region
			plan.SameProject = true
		}
	}
	private := func(s service) bool {
		return plan.SameProject && s.OwnerID == network.owner && s.Details.Region == network.region
	}

	for _, s := range services {
		skip := func(reason string) {
			plan.Skipped = append(plan.Skipped, railway.Skipped{What: "service", Name: s.Name, Reason: reason})
		}
		switch {
		case s.ID == self.ServiceID:
			skip("this agent")
		case s.Suspended == "suspended":
			skip("suspended")
		case s.Type == "background_worker":
			skip("a background worker has no address to check")
		case s.Type == "cron_job":
			if s.Details.Schedule == "" {
				skip("no schedule")
				continue
			}
			plan.add(cronJob(s))
		default:
			svc, reason := httpCheck(s, private(s), c.customDomains(ctx, s.ID))
			if reason != "" {
				skip(reason)
				continue
			}
			plan.add(svc)
		}
	}
	for i := range plan.Services {
		plan.Services[i].Monitor.SourceRef = SourceRef(plan.Services[i].ServiceID)
	}
	return plan, nil
}

// add adds a service once its monitor is valid. A private HTTP check without
// a port isn't yet: it's validated when it's imported.
func (p *Plan) add(svc railway.Service) {
	if svc.Monitor.Check == nil || svc.Monitor.Check.Target != "" {
		if err := svc.Monitor.Normalize(); err != nil {
			p.Skipped = append(p.Skipped, railway.Skipped{What: "service", Name: svc.Monitor.Name, Reason: err.Error()})
			return
		}
	}
	p.Services = append(p.Services, svc)
}

func newMonitor(s service) monitor.Monitor {
	return monitor.Monitor{Name: strings.TrimSpace(s.Name), Source: monitor.SourceUI}
}

// cronJob is a heartbeat on the job's schedule, which Render runs in UTC.
func cronJob(s service) railway.Service {
	m := newMonitor(s)
	m.Kind = monitor.KindHeartbeat
	m.Heartbeat = &monitor.Heartbeat{Token: monitor.NewToken(), Cron: s.Details.Schedule, Timezone: "UTC"}
	return railway.Service{ServiceID: s.ID, Monitor: m, Notes: []string{"add curl -fsS <ping URL> at the end of the job; the URL is shown after importing"}}
}

// httpCheck checks a web service or static site at its public address, and
// offers the private one when the agent shares its network. Private services
// only have the private one. Free web services can't receive private traffic.
func httpCheck(s service, sameNetwork bool, customDomains []string) (railway.Service, string) {
	svc := railway.Service{ServiceID: s.ID, Path: s.Details.HealthCheckPath}
	if !strings.HasPrefix(svc.Path, "/") {
		svc.Path = "/" + svc.Path
	}
	if s.Type != "private_service" {
		if len(customDomains) > 0 {
			svc.PublicHost = customDomains[0]
		} else if u, err := url.Parse(s.Details.URL); err == nil {
			svc.PublicHost = u.Host
		}
	}
	privateOK := sameNetwork && s.Type != "static_site" && s.Details.Plan != "free"
	if privateOK {
		svc.PrivateHost = s.Slug
		svc.Port = webPort
		for _, p := range s.Details.OpenPorts {
			if p.Protocol == "" || strings.EqualFold(p.Protocol, "TCP") {
				svc.Port = p.Port
				break
			}
		}
		if s.Type == "private_service" && len(s.Details.OpenPorts) == 0 {
			svc.Port = 0 // the review asks for it
		}
	}
	switch {
	case svc.PublicHost != "":
		svc.Address = "public"
	case privateOK:
		svc.Address = "private"
		svc.Notes = append(svc.Notes, "private address from the service's slug; check it under Connect → Internal")
	case s.Type == "private_service":
		return svc, "private service; deploy the agent on Render in the same workspace and region to check it"
	default:
		return svc, "no public address"
	}
	svc.Monitor = newMonitor(s)
	svc.Monitor.Kind = monitor.KindHealthcheck
	svc.Monitor.Check = &monitor.Check{Type: monitor.TypeHTTP, Target: target(svc)}
	return svc, ""
}

// target is the HTTP check's URL for the chosen address; empty while a
// private one lacks its port.
func target(s railway.Service) string {
	if s.Address == "public" {
		return "https://" + s.PublicHost + s.Path
	}
	if s.Port == 0 {
		return ""
	}
	return "http://" + s.PrivateHost + ":" + strconv.Itoa(s.Port) + s.Path
}
