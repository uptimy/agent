package checks

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/uptimy/agent/internal/monitor"
)

func init() {
	register(monitor.TypeHTTP, func(ctx context.Context, c *Checker, m monitor.Check) Outcome { return c.http(ctx, m) })
}

func (c *Checker) http(ctx context.Context, m monitor.Check) Outcome {
	req, err := http.NewRequestWithContext(ctx, m.Config.Method, m.Target, nil)
	if err != nil {
		return Outcome{Message: err.Error()}
	}
	req.Header.Set("User-Agent", "uptimy-agent")
	for k, v := range m.Config.Headers {
		req.Header.Set(k, v)
	}
	client := c.secure
	if m.Config.IgnoreTLS {
		client = c.insecure
	}
	resp, err := client.Do(req)
	if err != nil {
		return failure(err)
	}
	defer resp.Body.Close()

	ranges, _ := monitor.ParseStatusRanges(m.Config.ExpectedStatus) // validated on save
	if !slices.ContainsFunc(ranges, func(r monitor.StatusRange) bool {
		return resp.StatusCode >= r.Min && resp.StatusCode <= r.Max
	}) {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return Outcome{Message: fmt.Sprintf("unexpected status %d", resp.StatusCode), StatusCode: resp.StatusCode, ErrorClass: classForStatus(resp.StatusCode)}
	}
	if m.Config.Keyword != "" {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return Outcome{Message: "reading body: " + trimErr(err), StatusCode: resp.StatusCode, ErrorClass: classify(err)}
		}
		if !strings.Contains(string(body), m.Config.Keyword) {
			return Outcome{Message: fmt.Sprintf("keyword %q not found", m.Config.Keyword), StatusCode: resp.StatusCode, ErrorClass: classAssertion}
		}
	} else {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	}
	return Outcome{OK: true, Message: fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode)), StatusCode: resp.StatusCode}
}
