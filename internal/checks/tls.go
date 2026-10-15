package checks

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/uptimy/agent/internal/monitor"
)

func init() {
	register(monitor.TypeTLS, func(ctx context.Context, _ *Checker, m monitor.Check) Outcome { return tlsExpiry(ctx, m) })
}

func tlsExpiry(ctx context.Context, m monitor.Check) Outcome {
	addr := m.Target
	addr = strings.TrimPrefix(addr, "https://")
	addr, _, _ = strings.Cut(addr, "/")
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	} else {
		addr = net.JoinHostPort(addr, "443")
	}
	d := tls.Dialer{Config: &tls.Config{ServerName: host}}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		out := failure(err)
		if out.ErrorClass == classOther {
			out.ErrorClass = classTLS // a failed handshake
		}
		return out
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return Outcome{Message: "no certificate presented", ErrorClass: classTLS}
	}
	days := int(time.Until(certs[0].NotAfter).Hours() / 24)
	msg := fmt.Sprintf("certificate expires in %d days (%s)", days, certs[0].NotAfter.Format("2006-01-02"))
	if days < m.Config.MinDaysRemaining {
		return Outcome{Message: msg, ErrorClass: classTLS}
	}
	return Outcome{OK: true, Message: msg}
}
