package checks

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/monitor"
	"golang.org/x/net/dns/dnsmessage"
)

// dnsAnswer is what a fake DNS server replies to every query.
type dnsAnswer struct {
	rcode   dnsmessage.RCode
	answers []dnsmessage.Resource
	// truncateUDP sends a truncated reply over UDP, so the client has to
	// ask again over TCP to get the answers.
	truncateUDP bool
}

// serveDNS answers queries with a, over UDP and TCP on one loopback port,
// and returns its host:port.
func serveDNS(t *testing.T, a dnsAnswer) string {
	t.Helper()
	var udp net.PacketConn
	var tcp net.Listener
	for try := 0; ; try++ {
		var err error
		if udp, err = net.ListenPacket("udp", "127.0.0.1:0"); err != nil {
			t.Fatal(err)
		}
		if tcp, err = net.Listen("tcp", udp.LocalAddr().String()); err == nil {
			break
		}
		udp.Close()
		if try == 10 {
			t.Fatalf("no port free for both UDP and TCP: %v", err)
		}
	}
	t.Cleanup(func() { udp.Close(); tcp.Close() })

	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			if reply := packReply(buf[:n], a, a.truncateUDP); reply != nil {
				_, _ = udp.WriteTo(reply, from)
			}
		}
	}()
	go func() {
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var size [2]byte
				if _, err := io.ReadFull(conn, size[:]); err != nil {
					return
				}
				query := make([]byte, binary.BigEndian.Uint16(size[:]))
				if _, err := io.ReadFull(conn, query); err != nil {
					return
				}
				reply := packReply(query, a, false)
				_, _ = conn.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(reply))), reply...))
			}()
		}
	}()
	return udp.LocalAddr().String()
}

func packReply(query []byte, a dnsAnswer, truncated bool) []byte {
	var q dnsmessage.Message
	if err := q.Unpack(query); err != nil {
		return nil
	}
	m := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: q.ID, Response: true, RecursionAvailable: true, RCode: a.rcode, Truncated: truncated},
		Questions: q.Questions,
	}
	if !truncated {
		m.Answers = a.answers
	}
	reply, err := m.Pack()
	if err != nil {
		return nil
	}
	return reply
}

func cnameRR(owner, target string) dnsmessage.Resource {
	return dnsmessage.Resource{
		Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(owner), Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET, TTL: 60},
		Body:   &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName(target)},
	}
}

func aRR(owner string) dnsmessage.Resource {
	return dnsmessage.Resource{
		Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName(owner), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60},
		Body:   &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}},
	}
}

// closedUDPPort is a loopback address nothing listens on.
func closedUDPPort(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := conn.LocalAddr().String()
	conn.Close()
	return addr
}

// runCNAME runs a CNAME check on domain, asking resolver.
func runCNAME(t *testing.T, domain, expected, resolver string) Outcome {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := normalized(t, monitor.Check{Type: monitor.TypeDNS, Target: domain, Config: monitor.Config{RecordType: "CNAME", Resolver: resolver, Expected: expected}})
	return New(nil).Run(ctx, m)
}

func TestCNAMECheck(t *testing.T) {
	single := serveDNS(t, dnsAnswer{answers: []dnsmessage.Resource{cnameRR("www.shop.test.", "shop.cdn.test.")}})
	// The chain's later link listed first, and the owner in other case: only
	// the queried name's own record counts.
	chain := serveDNS(t, dnsAnswer{answers: []dnsmessage.Resource{
		cnameRR("shop.cdn.test.", "shops.cdn.test."),
		cnameRR("WWW.Shop.test.", "shop.cdn.test."),
	}})

	t.Run("its own record", func(t *testing.T) {
		if out := runCNAME(t, "www.shop.test", "shop.cdn.test", single); !out.OK || out.Message != "shop.cdn.test" {
			t.Fatalf("want ok with shop.cdn.test: %+v", out)
		}
	})

	t.Run("first link of a chain", func(t *testing.T) {
		if out := runCNAME(t, "www.shop.test", "shop.cdn.test", chain); !out.OK || out.Message != "shop.cdn.test" {
			t.Fatalf("want ok with shop.cdn.test: %+v", out)
		}
		if out := runCNAME(t, "www.shop.test", "shops.cdn.test", chain); out.OK || out.ErrorClass != classAssertion {
			t.Fatalf("the end of the chain isn't the record: want an assertion failure: %+v", out)
		}
	})

	t.Run("no expected value", func(t *testing.T) {
		if out := runCNAME(t, "www.shop.test", "", single); !out.OK {
			t.Fatalf("want ok: %+v", out)
		}
	})

	for name, a := range map[string]dnsAnswer{
		"no records":     {},
		"only A records": {answers: []dnsmessage.Resource{aRR("www.shop.test.")}},
		"someone else's": {answers: []dnsmessage.Resource{cnameRR("other.shop.test.", "shop.cdn.test.")}},
	} {
		t.Run(name, func(t *testing.T) {
			out := runCNAME(t, "www.shop.test", "", serveDNS(t, a))
			if out.OK || out.ErrorClass != classAssertion || out.Message != "www.shop.test has no CNAME record" {
				t.Fatalf("want an assertion failure: %+v", out)
			}
		})
	}

	t.Run("NXDOMAIN", func(t *testing.T) {
		out := runCNAME(t, "nope.shop.test", "", serveDNS(t, dnsAnswer{rcode: dnsmessage.RCodeNameError}))
		if out.OK || out.ErrorClass != classDNS || out.Message != "lookup nope.shop.test: no such domain (NXDOMAIN)" {
			t.Fatalf("want a dns failure naming NXDOMAIN: %+v", out)
		}
	})

	t.Run("truncated over UDP", func(t *testing.T) {
		server := serveDNS(t, dnsAnswer{truncateUDP: true, answers: []dnsmessage.Resource{cnameRR("www.shop.test.", "shop.cdn.test.")}})
		if out := runCNAME(t, "www.shop.test", "shop.cdn.test", server); !out.OK {
			t.Fatalf("want ok over TCP: %+v", out)
		}
	})

	t.Run("SERVFAIL", func(t *testing.T) {
		out := runCNAME(t, "www.shop.test", "", serveDNS(t, dnsAnswer{rcode: dnsmessage.RCodeServerFailure}))
		if out.OK || out.ErrorClass != classDNS || !strings.Contains(out.Message, "SERVFAIL") {
			t.Fatalf("want a dns failure naming SERVFAIL: %+v", out)
		}
	})
}

// Where there's no resolv.conf (Windows), LookupCNAME's answer is read
// instead: the domain itself means it has no CNAME.
func TestCNAMEFromCanonical(t *testing.T) {
	for _, c := range []struct{ domain, canonical, want string }{
		{"www.shop.test", "shop.cdn.test.", "shop.cdn.test"},
		{"www.shop.test", "www.shop.test.", ""},
		{"WWW.shop.test.", "www.Shop.test.", ""},
		{" www.shop.test ", "www.shop.test", ""},
	} {
		got, err := cnameFromCanonical(c.domain, c.canonical)
		if c.want == "" {
			if !errors.Is(err, errNoCNAME) {
				t.Errorf("%q → %q: got %q, %v; want errNoCNAME", c.domain, c.canonical, got, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q → %q: got %q, %v; want %q", c.domain, c.canonical, got, err, c.want)
		}
	}
}

// SERVFAIL and a server that refuses move on to the next one.
func TestLookupCNAMERecordNextServer(t *testing.T) {
	servfail := serveDNS(t, dnsAnswer{rcode: dnsmessage.RCodeServerFailure})
	good := serveDNS(t, dnsAnswer{answers: []dnsmessage.Resource{cnameRR("www.shop.test.", "shop.cdn.test.")}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	target, err := lookupCNAMERecord(ctx, []string{servfail, closedUDPPort(t), good}, "www.shop.test")
	if err != nil || target != "shop.cdn.test" {
		t.Fatalf("got %q, %v; want shop.cdn.test", target, err)
	}
}

// A server that never answers gets only its share of the timeout: the
// second attempt still has time.
func TestLookupCNAMERecordSplitsTheTimeout(t *testing.T) {
	silent, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()
	good := serveDNS(t, dnsAnswer{answers: []dnsmessage.Resource{cnameRR("www.shop.test.", "shop.cdn.test.")}})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	target, err := lookupCNAMERecord(ctx, []string{silent.LocalAddr().String(), good}, "www.shop.test")
	if err != nil || target != "shop.cdn.test" {
		t.Fatalf("got %q, %v; want shop.cdn.test", target, err)
	}
}

func TestLookupCNAMERecordErrors(t *testing.T) {
	if _, err := lookupCNAMERecord(context.Background(), nil, "www.shop.test"); err == nil {
		t.Error("no servers: want an error")
	}
	if _, err := lookupCNAMERecord(context.Background(), []string{"127.0.0.1:53"}, strings.Repeat("a.", 200)); err == nil {
		t.Error("a name over 255 bytes: want an error")
	}
	_, err := lookupCNAMERecord(context.Background(), []string{serveDNS(t, dnsAnswer{})}, "www.shop.test.")
	if !errors.Is(err, errNoCNAME) {
		t.Errorf("a trailing dot is fine: got %v, want errNoCNAME", err)
	}
}

func TestNameservers(t *testing.T) {
	conf := `# Generated by the host
; old-style comment
search internal
nameserver fdaa::3
nameserver 10.0.0.2
nameserver fe80::1%eth0
nameserver not-an-ip
nameserver
options ndots:0
`
	got := strings.Join(nameservers(strings.NewReader(conf)), " ")
	want := "[fdaa::3]:53 10.0.0.2:53 [fe80::1%eth0]:53"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := nameservers(strings.NewReader("")); len(got) != 0 {
		t.Fatalf("empty file: got %v", got)
	}
}

func TestRcodeName(t *testing.T) {
	for code, want := range map[dnsmessage.RCode]string{
		dnsmessage.RCodeServerFailure: "SERVFAIL",
		dnsmessage.RCodeRefused:       "REFUSED",
		dnsmessage.RCode(9):           "rcode " + strconv.Itoa(9),
	} {
		if got := rcodeName(code); got != want {
			t.Errorf("%d: %q, want %q", code, got, want)
		}
	}
}
