package railway

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/uptimy/agent/internal/monitor"
)

// Plan is what a Railway environment's services become. Nothing is created
// until it's applied, so people can review it first.
type Plan struct {
	ProjectName     string `json:"project_name"`
	EnvironmentName string `json:"environment_name"`
	// SameProject: the agent runs in this project and environment, so it can
	// reach services over private networking.
	SameProject bool      `json:"same_project"`
	Services    []Service `json:"services"`
	Skipped     []Skipped `json:"skipped"`
}

// Service is a Railway service as a monitor.
type Service struct {
	ServiceID string          `json:"service_id"`
	Monitor   monitor.Monitor `json:"monitor"`
	Notes     []string        `json:"notes"`
	// HTTP checks only: the two ways to reach the service, and which the
	// target uses. The review can switch between them; Port is empty when
	// Railway doesn't know it, and must be entered for a private target.
	Address     string `json:"address,omitempty"` // "private" or "public"
	PrivateHost string `json:"private_host,omitempty"`
	PublicHost  string `json:"public_host,omitempty"`
	Port        int    `json:"port,omitempty"`
	Path        string `json:"path,omitempty"`
}

// Skipped is a service that won't be monitored, and why. It has the shape of
// the Kuma importer's, so the UI lists both the same way.
type Skipped struct {
	What   string `json:"what"` // "service"
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// SourceRef identifies an imported service's monitor, so importing again
// skips it.
func SourceRef(environmentID, serviceID string) string {
	return "railway:" + environmentID + "/" + serviceID
}

// Read plans monitors for the services in the token's environment. self is
// where the agent runs (SelfFromEnv).
func Read(ctx context.Context, c *Client, self Self) (Plan, error) {
	s, err := c.scope(ctx)
	if err != nil {
		return Plan{}, err
	}
	project, environment, instances, err := c.services(ctx, s)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{
		ProjectName: project, EnvironmentName: environment,
		SameProject: self.ProjectID == s.ProjectID && self.EnvironmentID == s.EnvironmentID,
		Services:    []Service{}, Skipped: []Skipped{},
	}
	var current, web []instance
	for _, in := range instances {
		if in.EnvironmentID != s.EnvironmentID || in.DeletedAt != nil {
			continue
		}
		current = append(current, in)
		if in.CronSchedule == "" && databaseOf(in) == nil && in.ServiceID != self.ServiceID {
			web = append(web, in)
		}
	}
	var hosts map[string]string
	if plan.SameProject && len(web) > 0 {
		ids := make([]string, len(web))
		for i, in := range web {
			ids[i] = in.ServiceID
		}
		hosts = c.privateHosts(ctx, s.EnvironmentID, ids)
	}
	for _, in := range current {
		switch {
		case self.ServiceID != "" && in.ServiceID == self.ServiceID:
			plan.Skipped = append(plan.Skipped, Skipped{What: "service", Name: in.ServiceName, Reason: "this agent"})
		case in.CronSchedule != "":
			plan.add(cronJob(in))
		case databaseOf(in) != nil:
			plan.add(database(in, plan.SameProject))
		default:
			svc, reason := httpCheck(in, plan.SameProject, hosts[in.ServiceID])
			if reason != "" {
				plan.Skipped = append(plan.Skipped, Skipped{What: "service", Name: in.ServiceName, Reason: reason})
				continue
			}
			plan.add(svc)
		}
	}
	for i := range plan.Services {
		plan.Services[i].Monitor.SourceRef = SourceRef(s.EnvironmentID, plan.Services[i].ServiceID)
	}
	return plan, nil
}

// add adds a service once its monitor is valid. A private HTTP check without
// a port isn't yet: it's validated when it's imported.
func (p *Plan) add(svc Service) {
	if svc.Monitor.Check == nil || svc.Monitor.Check.Target != "" {
		if err := svc.Monitor.Normalize(); err != nil {
			p.Skipped = append(p.Skipped, Skipped{What: "service", Name: svc.Monitor.Name, Reason: err.Error()})
			return
		}
	}
	p.Services = append(p.Services, svc)
}

func newMonitor(in instance) monitor.Monitor {
	return monitor.Monitor{Name: strings.TrimSpace(in.ServiceName), Source: monitor.SourceUI}
}

// cronJob is a heartbeat on the service's cron schedule, which Railway runs
// in UTC.
func cronJob(in instance) Service {
	m := newMonitor(in)
	m.Kind = monitor.KindHeartbeat
	m.Heartbeat = &monitor.Heartbeat{Token: monitor.NewToken(), Cron: in.CronSchedule, Timezone: "UTC"}
	return Service{ServiceID: in.ServiceID, Monitor: m, Notes: []string{"add curl -fsS <ping URL> at the end of the job; the URL is shown after importing"}}
}

// dbKind is a database Railway's templates deploy, and the variables that
// hold its connection URL.
type dbKind struct {
	check           monitor.Type
	private, public string
	imageSubstrings []string
}

var databases = []dbKind{
	{monitor.TypePostgres, "DATABASE_URL", "DATABASE_PUBLIC_URL", []string{"postgres", "postgis", "pgvector", "timescaledb"}},
	{monitor.TypeMySQL, "MARIADB_PRIVATE_URL", "MARIADB_PUBLIC_URL", []string{"mariadb"}},
	{monitor.TypeMySQL, "MYSQL_URL", "MYSQL_PUBLIC_URL", []string{"mysql"}},
	{monitor.TypeRedis, "REDIS_URL", "REDIS_PUBLIC_URL", []string{"redis"}},
}

func databaseOf(in instance) *dbKind {
	if in.Source == nil || in.Source.Image == "" {
		return nil
	}
	image, _, _ := strings.Cut(strings.ToLower(in.Source.Image), ":")
	for i, db := range databases {
		for _, s := range db.imageSubstrings {
			if strings.Contains(image, s) {
				return &databases[i]
			}
		}
	}
	return nil
}

// database checks the service through a variable on the agent, so its
// password stays in Railway: POSTGRES_URL for a service named Postgres.
func database(in instance, sameProject bool) Service {
	db := databaseOf(in)
	name := envName(in.ServiceName) + "_URL"
	m := newMonitor(in)
	m.Kind = monitor.KindHealthcheck
	m.Check = &monitor.Check{Type: db.check, Target: "${" + name + "}"}
	note := fmt.Sprintf("add the variable %s=${{%s.%s}} to the agent", name, in.ServiceName, db.private)
	if !sameProject {
		note = fmt.Sprintf("set the variable %s on the agent to %s's %s", name, in.ServiceName, db.public)
	}
	return Service{ServiceID: in.ServiceID, Monitor: m, Notes: []string{note}}
}

// envName is a service name as a variable name: "my-db" is MY_DB.
func envName(service string) string {
	var b strings.Builder
	for _, c := range strings.ToUpper(service) {
		switch {
		case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "_"):
			b.WriteByte('_')
		}
	}
	name := strings.TrimSuffix(b.String(), "_")
	if name == "" || name[0] >= '0' && name[0] <= '9' {
		name = "DB_" + name
	}
	return name
}

// httpCheck checks the service privately when the agent is in the same
// project, otherwise at its public domain.
func httpCheck(in instance, sameProject bool, privateHost string) (Service, string) {
	domains := slices.Concat(in.Domains.CustomDomains, in.Domains.ServiceDomains)
	svc := Service{ServiceID: in.ServiceID, Path: in.HealthcheckPath}
	if !strings.HasPrefix(svc.Path, "/") {
		svc.Path = "/" + svc.Path
	}
	if len(domains) > 0 {
		svc.PublicHost = domains[0].Domain
	}
	for _, d := range domains {
		if d.TargetPort > 0 {
			svc.Port = d.TargetPort
			break
		}
	}
	if sameProject {
		svc.PrivateHost = privateHost
		if svc.PrivateHost == "" {
			svc.PrivateHost = guessHost(in.ServiceName)
			svc.Notes = append(svc.Notes, "private address guessed from the name; check it in the service's Networking settings")
		}
		svc.Address = "private" // without a port, the review asks for one
	} else {
		if svc.PublicHost == "" {
			return svc, "no public domain; deploy the agent in this project to check it privately"
		}
		svc.Address = "public"
	}
	svc.Monitor = newMonitor(in)
	svc.Monitor.Kind = monitor.KindHealthcheck
	svc.Monitor.Check = &monitor.Check{Type: monitor.TypeHTTP, Target: svc.target()}
	return svc, ""
}

// target is the HTTP check's URL for the chosen address; empty while a
// private one lacks its port.
func (s Service) target() string {
	if s.Address == "public" {
		return "https://" + s.PublicHost + s.Path
	}
	if s.Port == 0 {
		return ""
	}
	return "http://" + s.PrivateHost + ":" + strconv.Itoa(s.Port) + s.Path
}

// guessHost is Railway's default private hostname for a service name.
func guessHost(service string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(service) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String() + ".railway.internal"
}
