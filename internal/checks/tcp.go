package checks

import (
	"context"
	"net"

	"github.com/uptimy/agent/internal/monitor"
)

func init() {
	register(monitor.TypeTCP, func(ctx context.Context, _ *Checker, m monitor.Check) Outcome { return tcp(ctx, m.Target) })
}

func tcp(ctx context.Context, target string) Outcome {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return failure(err)
	}
	conn.Close()
	return Outcome{OK: true, Message: "connected"}
}
