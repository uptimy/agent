package checks

import (
	"context"
	"errors"
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
	var servers []string // who CNAME checks ask: the check's resolver, else resolv.conf's
	if m.Config.Resolver != "" {
		server := m.Config.Resolver
		if !strings.Contains(server, ":") {
			server += ":53"
		}
		servers = []string{server}
		r = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, server)
		}}
	} else {
		servers = systemNameservers()
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
		// The domain's own record, not the end of its chain (cname.go).
		var cname string
		if len(servers) > 0 {
			cname, err = lookupCNAMERecord(ctx, servers, m.Target)
		} else if cname, err = r.LookupCNAME(ctx, m.Target); err == nil {
			cname, err = cnameFromCanonical(m.Target, cname)
		}
		switch {
		case errors.Is(err, errNoCNAME):
			return Outcome{Message: m.Target + " has no CNAME record", ErrorClass: classAssertion}
		case err != nil:
			err = fmt.Errorf("lookup %s: %w", m.Target, err)
		default:
			answers = append(answers, cname)
		}
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
