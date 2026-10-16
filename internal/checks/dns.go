package checks

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/uptimy/agent/internal/monitor"
)

func init() {
	register(monitor.TypeDNS, func(ctx context.Context, _ *Checker, m monitor.Check) Outcome { return dns(ctx, m) })
}

func dns(ctx context.Context, m monitor.Check) Outcome {
	r := net.DefaultResolver
	if m.Config.Resolver != "" {
		server := m.Config.Resolver
		if !strings.Contains(server, ":") {
			server += ":53"
		}
		r = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, server)
		}}
	}

	var answers []string
	var err error
	switch m.Config.RecordType {
	case "A", "AAAA":
		network := "ip4"
		if m.Config.RecordType == "AAAA" {
			network = "ip6"
		}
		var ips []net.IP
		ips, err = r.LookupIP(ctx, network, m.Target)
		for _, ip := range ips {
			answers = append(answers, ip.String())
		}
	case "CNAME":
		var cname string
		cname, err = r.LookupCNAME(ctx, m.Target)
		answers = append(answers, cname)
	case "MX":
		var mxs []*net.MX
		mxs, err = r.LookupMX(ctx, m.Target)
		for _, mx := range mxs {
			answers = append(answers, mx.Host)
		}
	case "TXT":
		answers, err = r.LookupTXT(ctx, m.Target)
	case "NS":
		var nss []*net.NS
		nss, err = r.LookupNS(ctx, m.Target)
		for _, ns := range nss {
			answers = append(answers, ns.Host)
		}
	}
	if err != nil {
		return Outcome{Message: trimErr(err), ErrorClass: classDNS} // the lookup is the check
	}
	if len(answers) == 0 {
		return Outcome{Message: "no records returned", ErrorClass: classAssertion}
	}
	joined := strings.Join(answers, ", ")
	if m.Config.Expected != "" && !strings.Contains(joined, m.Config.Expected) {
		return Outcome{Message: fmt.Sprintf("expected %q, got %s", m.Config.Expected, joined), ErrorClass: classAssertion}
	}
	return Outcome{OK: true, Message: joined}
}
