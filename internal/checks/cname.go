package checks

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// errNoCNAME means the name exists but has no CNAME record of its own (it
// may have A records or others).
var errNoCNAME = errors.New("no CNAME record")

// errNoSuchDomain is an NXDOMAIN answer: the name doesn't exist.
var errNoSuchDomain = errors.New("no such domain (NXDOMAIN)")

// errNotOurReply is a UDP packet that doesn't answer our query, such as a
// late answer to an earlier one.
var errNotOurReply = errors.New("reply doesn't match the query")

// resolvConf lists the system's DNS servers.
const resolvConf = "/etc/resolv.conf"

// lookupCNAMERecord asks servers for domain's own CNAME record and returns
// its target, without the trailing dot. net.Resolver.LookupCNAME isn't
// that: it returns the domain itself when the domain has no CNAME, so a CNAME
// check on a domain with only A records passed, and depending on the
// resolver (the system's, Windows') it returns the end of the chain rather
// than the domain's own record.
//
// Servers are tried in turn, twice over, each attempt getting an equal share
// of what's left of ctx's deadline, so a dead server or a lost UDP packet
// doesn't use up the check's whole timeout. NXDOMAIN is final; SERVFAIL,
// REFUSED and network errors move on to the next attempt.
func lookupCNAMERecord(ctx context.Context, servers []string, domain string) (string, error) {
	name, err := dnsmessage.NewName(fqdn(domain))
	if err != nil {
		return "", fmt.Errorf("invalid domain %q: %w", domain, err)
	}
	if len(servers) == 0 {
		return "", errors.New("no DNS servers to ask")
	}

	const rounds = 2
	attempts := rounds * len(servers)
	var lastErr error
	for i := 0; i < attempts && ctx.Err() == nil; i++ {
		server := servers[i%len(servers)]
		attemptCtx, cancel := attemptContext(ctx, attempts-i)
		reply, err := exchangeCNAME(attemptCtx, server, name)
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", server, err)
			continue
		}
		switch reply.RCode {
		case dnsmessage.RCodeSuccess:
			return cnameOf(reply, name)
		case dnsmessage.RCodeNameError:
			return "", errNoSuchDomain
		default:
			lastErr = fmt.Errorf("%s answered %s", server, rcodeName(reply.RCode))
		}
	}
	if lastErr == nil {
		lastErr = ctx.Err()
	}
	return "", lastErr
}

// attemptContext gives one of the attempts left an equal share of ctx's
// remaining time (5 seconds, Go's own default, when ctx has no deadline).
func attemptContext(ctx context.Context, attemptsLeft int) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithTimeout(ctx, 5*time.Second)
	}
	return context.WithTimeout(ctx, time.Until(deadline)/time.Duration(attemptsLeft))
}

// exchangeCNAME sends one CNAME query to server over UDP, and again over
// TCP when the UDP reply comes back truncated.
func exchangeCNAME(ctx context.Context, server string, name dnsmessage.Name) (*dnsmessage.Message, error) {
	// An unpredictable ID makes a spoofed reply harder to slip in.
	var idBytes [2]byte
	_, _ = rand.Read(idBytes[:])
	id := binary.BigEndian.Uint16(idBytes[:])
	query, err := (&dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: name, Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET}},
	}).Pack()
	if err != nil {
		return nil, err
	}
	reply, err := exchange(ctx, "udp", server, query, id)
	if err == nil && reply.Truncated {
		reply, err = exchange(ctx, "tcp", server, query, id)
	}
	return reply, err
}

func exchange(ctx context.Context, network, server string, query []byte, id uint16) (*dnsmessage.Message, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, network, server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	if network == "tcp" {
		// Over TCP every message is preceded by its length.
		msg := binary.BigEndian.AppendUint16(make([]byte, 0, 2+len(query)), uint16(len(query)))
		if _, err := conn.Write(append(msg, query...)); err != nil {
			return nil, err
		}
		var size [2]byte
		if _, err := io.ReadFull(conn, size[:]); err != nil {
			return nil, err
		}
		buf := make([]byte, binary.BigEndian.Uint16(size[:]))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return nil, err
		}
		return parseReply(buf, id)
	}

	if _, err := conn.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, 65535)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return nil, err
		}
		reply, err := parseReply(buf[:n], id)
		if errors.Is(err, errNotOurReply) {
			continue
		}
		return reply, err
	}
}

// parseReply reads a reply to query id. A truncated reply is returned with
// its header only, since its records may be cut off.
func parseReply(buf []byte, id uint16) (*dnsmessage.Message, error) {
	var p dnsmessage.Parser
	h, err := p.Start(buf)
	if err != nil {
		return nil, err
	}
	if h.ID != id || !h.Response {
		return nil, errNotOurReply
	}
	if h.Truncated {
		return &dnsmessage.Message{Header: h}, nil
	}
	var m dnsmessage.Message
	if err := m.Unpack(buf); err != nil {
		return nil, err
	}
	return &m, nil
}

// cnameOf is the target of the CNAME record owned by name. A reply can list
// a chain; only the queried name's own record counts.
func cnameOf(reply *dnsmessage.Message, name dnsmessage.Name) (string, error) {
	for _, rr := range reply.Answers {
		cname, ok := rr.Body.(*dnsmessage.CNAMEResource)
		if ok && strings.EqualFold(rr.Header.Name.String(), name.String()) {
			return strings.TrimSuffix(cname.CNAME.String(), "."), nil
		}
	}
	return "", errNoCNAME
}

func fqdn(domain string) string {
	domain = strings.TrimSpace(domain)
	if !strings.HasSuffix(domain, ".") {
		domain += "."
	}
	return domain
}

func rcodeName(code dnsmessage.RCode) string {
	switch code {
	case dnsmessage.RCodeFormatError:
		return "FORMERR"
	case dnsmessage.RCodeServerFailure:
		return "SERVFAIL"
	case dnsmessage.RCodeNotImplemented:
		return "NOTIMP"
	case dnsmessage.RCodeRefused:
		return "REFUSED"
	}
	return fmt.Sprintf("rcode %d", code)
}

// nameservers lists a resolv.conf's servers as host:port, in order.
func nameservers(r io.Reader) []string {
	var servers []string
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		// A link-local IPv6 server can carry a zone: fe80::1%eth0.
		if host, _, _ := strings.Cut(fields[1], "%"); net.ParseIP(host) == nil {
			continue
		}
		servers = append(servers, net.JoinHostPort(fields[1], "53"))
	}
	return servers
}

// systemNameservers are the servers in /etc/resolv.conf; none where there's
// no such file (Windows).
func systemNameservers() []string {
	f, err := os.Open(resolvConf)
	if err != nil {
		return nil
	}
	defer f.Close()
	return nameservers(f)
}

// cnameFromCanonical reads net.Resolver.LookupCNAME's answer where the
// record can't be asked for directly (no resolv.conf, as on Windows). It
// returns the end of the chain there, and the domain itself when the domain
// has no CNAME.
func cnameFromCanonical(domain, canonical string) (string, error) {
	canonical = strings.TrimSuffix(canonical, ".")
	if strings.EqualFold(canonical, strings.TrimSuffix(strings.TrimSpace(domain), ".")) {
		return "", errNoCNAME
	}
	return canonical, nil
}
