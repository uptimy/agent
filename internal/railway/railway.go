// Package railway reads the services of a Railway project environment
// through Railway's API, with a project token, and plans a monitor for each:
// a heartbeat for cron jobs, a database check for database images, an HTTP
// check for the rest. What can't be monitored is listed with the reason.
package railway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/uptimy/agent/internal/version"
)

// APIURL is Railway's public GraphQL API.
const APIURL = "https://backboard.railway.com/graphql/v2"

// ErrToken is a project token Railway doesn't accept.
var ErrToken = errors.New("Railway didn't accept this project token")

// Client queries Railway's API with a project token.
type Client struct {
	url   string
	token string
	http  *http.Client
}

// NewClient returns a client for the API at url (APIURL when empty).
func NewClient(url, token string) *Client {
	if url == "" {
		url = APIURL
	}
	return &Client{url: url, token: token, http: &http.Client{Timeout: 15 * time.Second}}
}

// apiError is an error Railway's API answered with.
type apiError struct{ message string }

func (e *apiError) Error() string { return "Railway: " + e.message }

func (c *Client) query(ctx context.Context, q string, vars map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Project-Access-Token", c.token)
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach Railway: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrToken
	}
	var res struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&res); err != nil {
		return fmt.Errorf("Railway answered %s", resp.Status)
	}
	if len(res.Errors) > 0 {
		return &apiError{res.Errors[0].Message}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Railway answered %s", resp.Status)
	}
	return json.Unmarshal(res.Data, out)
}

// Self is where the agent runs, from the variables Railway sets on every
// deployment; empty outside Railway.
type Self struct {
	ProjectID     string
	EnvironmentID string
	ServiceID     string
}

// SelfFromEnv reads Self from the environment.
func SelfFromEnv() Self {
	return Self{
		ProjectID:     os.Getenv("RAILWAY_PROJECT_ID"),
		EnvironmentID: os.Getenv("RAILWAY_ENVIRONMENT_ID"),
		ServiceID:     os.Getenv("RAILWAY_SERVICE_ID"),
	}
}

type scope struct {
	ProjectID     string `json:"projectId"`
	EnvironmentID string `json:"environmentId"`
}

type domain struct {
	Domain     string `json:"domain"`
	TargetPort int    `json:"targetPort"`
}

type instance struct {
	ServiceID       string  `json:"serviceId"`
	ServiceName     string  `json:"serviceName"`
	EnvironmentID   string  `json:"environmentId"`
	HealthcheckPath string  `json:"healthcheckPath"`
	CronSchedule    string  `json:"cronSchedule"`
	DeletedAt       *string `json:"deletedAt"`
	Source          *struct {
		Image string `json:"image"`
		Repo  string `json:"repo"`
	} `json:"source"`
	Domains struct {
		ServiceDomains []domain `json:"serviceDomains"`
		CustomDomains  []domain `json:"customDomains"`
	} `json:"domains"`
}

const scopeQuery = `query { projectToken { projectId environmentId } }`

// Service instances are read through the environment: Service has no
// instances field. Variables are never queried, since they hold secrets.
const servicesQuery = `query ($project: String!, $environment: String!) {
  project(id: $project) { name }
  environment(id: $environment) {
    name
    serviceInstances {
      edges {
        node {
          serviceId serviceName environmentId healthcheckPath cronSchedule deletedAt
          source { image repo }
          domains { serviceDomains { domain targetPort } customDomains { domain targetPort } }
        }
      }
    }
  }
}`

const networksQuery = `query ($environment: String!) {
  privateNetworks(environmentId: $environment) { publicId dnsName deletedAt }
}`

const endpointQuery = `query ($environment: String!, $network: String!, $service: String!) {
  privateNetworkEndpoint(environmentId: $environment, privateNetworkId: $network, serviceId: $service) { dnsName deletedAt }
}`

func (c *Client) scope(ctx context.Context) (scope, error) {
	var out struct {
		ProjectToken scope `json:"projectToken"`
	}
	if err := c.query(ctx, scopeQuery, nil, &out); err != nil {
		// Railway answers an unknown token with "Project Token not found".
		if _, ok := errors.AsType[*apiError](err); ok {
			return scope{}, ErrToken
		}
		return scope{}, err
	}
	if out.ProjectToken.ProjectID == "" || out.ProjectToken.EnvironmentID == "" {
		return scope{}, ErrToken
	}
	return out.ProjectToken, nil
}

func (c *Client) services(ctx context.Context, s scope) (project, environment string, instances []instance, err error) {
	var out struct {
		Project struct {
			Name string `json:"name"`
		} `json:"project"`
		Environment struct {
			Name             string `json:"name"`
			ServiceInstances struct {
				Edges []struct {
					Node instance `json:"node"`
				} `json:"edges"`
			} `json:"serviceInstances"`
		} `json:"environment"`
	}
	err = c.query(ctx, servicesQuery, map[string]any{"project": s.ProjectID, "environment": s.EnvironmentID}, &out)
	if err != nil {
		return "", "", nil, err
	}
	for _, e := range out.Environment.ServiceInstances.Edges {
		instances = append(instances, e.Node)
	}
	return out.Project.Name, out.Environment.Name, instances, nil
}

// privateHosts looks up each service's private hostname. A service missing
// from the result (the lookup failed, or the project has no private network)
// gets a guessed one.
func (c *Client) privateHosts(ctx context.Context, environmentID string, serviceIDs []string) map[string]string {
	hosts := map[string]string{}
	var nets struct {
		PrivateNetworks []struct {
			PublicID  string  `json:"publicId"`
			DNSName   string  `json:"dnsName"`
			DeletedAt *string `json:"deletedAt"`
		} `json:"privateNetworks"`
	}
	if err := c.query(ctx, networksQuery, map[string]any{"environment": environmentID}, &nets); err != nil {
		return hosts
	}
	var network, suffix string
	for _, n := range nets.PrivateNetworks {
		if n.DeletedAt == nil && n.PublicID != "" {
			network, suffix = n.PublicID, n.DNSName
			break
		}
	}
	if network == "" {
		return hosts
	}
	if !strings.Contains(suffix, ".") {
		suffix = "railway.internal"
	}
	for _, id := range serviceIDs {
		var out struct {
			Endpoint *struct {
				DNSName   string  `json:"dnsName"`
				DeletedAt *string `json:"deletedAt"`
			} `json:"privateNetworkEndpoint"`
		}
		err := c.query(ctx, endpointQuery, map[string]any{"environment": environmentID, "network": network, "service": id}, &out)
		if err != nil || out.Endpoint == nil || out.Endpoint.DeletedAt != nil || out.Endpoint.DNSName == "" {
			continue
		}
		// The endpoint's name is a label ("api") on the network's domain, or
		// already a full hostname.
		if strings.Contains(out.Endpoint.DNSName, ".") {
			hosts[id] = out.Endpoint.DNSName
		} else {
			hosts[id] = out.Endpoint.DNSName + "." + suffix
		}
	}
	return hosts
}
