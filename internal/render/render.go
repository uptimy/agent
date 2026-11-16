// Package render reads the services of the Render workspaces an API key can
// see, through Render's REST API, and plans a monitor for each: a heartbeat
// for cron jobs and an HTTP check for web, private and static services.
// What can't be monitored is listed with the reason.
package render

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/uptimy/agent/internal/version"
)

// APIURL is Render's public REST API.
const APIURL = "https://api.render.com/v1"

const (
	pageLimit = 100
	// Accounts with more services than this are cut off rather than paged forever.
	maxPages = 20
)

// ErrToken is an API key Render doesn't accept.
var ErrToken = errors.New("Render didn't accept this API key")

// Client queries Render's API with an API key.
type Client struct {
	url  string
	key  string
	http *http.Client
}

// NewClient returns a client for the API at url (APIURL when empty).
func NewClient(url, key string) *Client {
	if url == "" {
		url = APIURL
	}
	return &Client{url: url, key: key, http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+path+"?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't reach Render: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrToken
	}
	if resp.StatusCode != http.StatusOK {
		var body struct {
			Message string `json:"message"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body) == nil && body.Message != "" {
			return fmt.Errorf("Render: %s", body.Message)
		}
		return fmt.Errorf("Render answered %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out)
}

// list follows Render's cursor pagination: each page is an array of
// {<item>, cursor}, and the next page starts after the last cursor.
func list[T any](ctx context.Context, c *Client, path string, query url.Values) ([]T, error) {
	var all []T
	cursor := ""
	for range maxPages {
		q := url.Values{}
		for k, v := range query {
			q[k] = v
		}
		q.Set("limit", fmt.Sprint(pageLimit))
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page []json.RawMessage
		if err := c.get(ctx, path, q, &page); err != nil {
			return nil, err
		}
		for _, raw := range page {
			var item T
			if err := json.Unmarshal(raw, &item); err != nil {
				return nil, err
			}
			all = append(all, item)
		}
		if len(page) < pageLimit {
			break
		}
		var last struct {
			Cursor string `json:"cursor"`
		}
		if err := json.Unmarshal(page[len(page)-1], &last); err != nil {
			return nil, err
		}
		if last.Cursor == "" {
			break
		}
		cursor = last.Cursor
	}
	return all, nil
}

// Self is where the agent runs, from the variable Render sets on every
// service; empty outside Render.
type Self struct {
	ServiceID string
}

// SelfFromEnv reads Self from the environment.
func SelfFromEnv() Self {
	return Self{ServiceID: os.Getenv("RENDER_SERVICE_ID")}
}

type owner struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type port struct {
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

type service struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	OwnerID   string `json:"ownerId"`
	Type      string `json:"type"`
	Suspended string `json:"suspended"`
	Details   struct {
		URL             string `json:"url"`
		Region          string `json:"region"`
		Plan            string `json:"plan"`
		HealthCheckPath string `json:"healthCheckPath"`
		Schedule        string `json:"schedule"`
		OpenPorts       []port `json:"openPorts"`
	} `json:"serviceDetails"`
}

func (c *Client) owners(ctx context.Context) ([]owner, error) {
	rows, err := list[struct {
		Owner owner `json:"owner"`
	}](ctx, c, "/owners", nil)
	if err != nil {
		return nil, err
	}
	out := make([]owner, len(rows))
	for i, r := range rows {
		out[i] = r.Owner
	}
	return out, nil
}

func (c *Client) services(ctx context.Context) ([]service, error) {
	rows, err := list[struct {
		Service service `json:"service"`
	}](ctx, c, "/services", url.Values{"includePreviews": {"false"}})
	if err != nil {
		return nil, err
	}
	out := make([]service, len(rows))
	for i, r := range rows {
		out[i] = r.Service
	}
	return out, nil
}

// customDomains are the service's verified custom domains.
func (c *Client) customDomains(ctx context.Context, serviceID string) []string {
	rows, err := list[struct {
		Domain struct {
			Name               string `json:"name"`
			VerificationStatus string `json:"verificationStatus"`
		} `json:"customDomain"`
	}](ctx, c, "/services/"+url.PathEscape(serviceID)+"/custom-domains", nil)
	if err != nil {
		return nil
	}
	var names []string
	for _, r := range rows {
		if r.Domain.VerificationStatus == "verified" {
			names = append(names, r.Domain.Name)
		}
	}
	return names
}
