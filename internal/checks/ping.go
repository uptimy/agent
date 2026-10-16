package checks

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"

	"github.com/uptimy/agent/internal/monitor"
)

// A ping sends a few ICMP echo requests and is up if any reply arrives.
const (
	pingCount   = 3
	pingSpacing = 200 * time.Millisecond
	pingWait    = 2 * time.Second // per request, within the check's timeout
)

func init() {
	register(monitor.TypePing, func(ctx context.Context, _ *Checker, m monitor.Check) Outcome { return ping(ctx, m.Target) })
}

func ping(ctx context.Context, host string) Outcome {
	ip, err := resolveOne(ctx, host)
	if err != nil {
		return Outcome{Message: trimErr(err), ErrorClass: classDNS}
	}
	conn, err := listenICMP(ip)
	if err != nil {
		return Outcome{Message: err.Error()}
	}
	defer conn.Close()

	var (
		received int
		total    time.Duration
		lastErr  error
	)
	for seq := 1; seq <= pingCount; seq++ {
		if seq > 1 {
			select {
			case <-ctx.Done():
				return pingOutcome(received, seq-1, total, ctx.Err())
			case <-time.After(pingSpacing):
			}
		}
		rtt, err := conn.echo(ctx, seq)
		if err != nil {
			lastErr = err
			continue
		}
		received++
		total += rtt
	}
	return pingOutcome(received, pingCount, total, lastErr)
}

func pingOutcome(received, sent int, total time.Duration, lastErr error) Outcome {
	if received == 0 {
		msg := "no reply"
		if sent > 1 {
			msg = fmt.Sprintf("no reply to %d pings", sent)
		}
		if lastErr != nil && !errors.Is(lastErr, os.ErrDeadlineExceeded) && !errors.Is(lastErr, context.DeadlineExceeded) {
			msg += ": " + trimErr(lastErr)
		}
		// No reply in time is a timeout; a send that failed says why.
		class := classTimeout
		if lastErr != nil && !errors.Is(lastErr, os.ErrDeadlineExceeded) && !errors.Is(lastErr, context.DeadlineExceeded) {
			class = classify(lastErr)
		}
		return Outcome{Message: msg, ErrorClass: class}
	}
	avg := total / time.Duration(received)
	msg := fmt.Sprintf("%d/%d replies, %s average", received, sent, avg.Round(100*time.Microsecond))
	return Outcome{OK: true, Message: msg, Latency: avg}
}

// resolveOne returns the host's address, preferring IPv4.
func resolveOne(ctx context.Context, host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return ip, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if a.IP.To4() != nil {
			return a.IP, nil
		}
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%s has no addresses", host)
	}
	return addrs[0].IP, nil
}

type icmpConn struct {
	*icmp.PacketConn
	dst     net.Addr
	v6      bool
	id      int
	payload []byte
	readBuf []byte
}

// listenICMP opens an unprivileged ICMP socket (Linux with
// net.ipv4.ping_group_range, macOS), or a raw one if the process may.
func listenICMP(ip net.IP) (*icmpConn, error) {
	v6 := ip.To4() == nil
	dgram, raw, rawAddr := "udp4", "ip4:icmp", "0.0.0.0"
	if v6 {
		dgram, raw, rawAddr = "udp6", "ip6:ipv6-icmp", "::"
	}
	payload := make([]byte, 16)
	_, _ = rand.Read(payload) // matches replies to this check
	c := &icmpConn{v6: v6, payload: payload, readBuf: make([]byte, 1500), id: int(payload[0])<<8 | int(payload[1])}

	if pc, err := icmp.ListenPacket(dgram, ""); err == nil {
		c.PacketConn, c.dst = pc, &net.UDPAddr{IP: ip}
		return c, nil
	}
	pc, err := icmp.ListenPacket(raw, rawAddr)
	if err != nil {
		return nil, errors.New("ICMP isn't allowed for the agent's user: allow it with the sysctl net.ipv4.ping_group_range " +
			`(e.g. "0 2147483647"; the Helm chart sets it) or grant the NET_RAW capability`)
	}
	c.PacketConn, c.dst = pc, &net.IPAddr{IP: ip}
	return c, nil
}

// echo sends one echo request and waits for its reply.
func (c *icmpConn) echo(ctx context.Context, seq int) (time.Duration, error) {
	var typ icmp.Type = ipv4.ICMPTypeEcho
	proto := ipv4.ICMPTypeEcho.Protocol()
	if c.v6 {
		typ, proto = ipv6.ICMPTypeEchoRequest, ipv6.ICMPTypeEchoRequest.Protocol()
	}
	msg, err := (&icmp.Message{Type: typ, Body: &icmp.Echo{ID: c.id, Seq: seq, Data: c.payload}}).Marshal(nil)
	if err != nil {
		return 0, err
	}
	deadline := time.Now().Add(pingWait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.SetDeadline(deadline); err != nil {
		return 0, err
	}
	start := time.Now()
	if _, err := c.WriteTo(msg, c.dst); err != nil {
		return 0, err
	}
	for {
		n, _, err := c.ReadFrom(c.readBuf)
		if err != nil {
			return 0, err
		}
		reply, err := icmp.ParseMessage(proto, c.readBuf[:n])
		if err != nil {
			continue
		}
		// A raw socket sees every ICMP packet on the host; an unprivileged
		// one has its ID rewritten by the kernel. Match on seq and payload.
		body, ok := reply.Body.(*icmp.Echo)
		if (reply.Type == ipv4.ICMPTypeEchoReply || reply.Type == ipv6.ICMPTypeEchoReply) &&
			ok && body.Seq == seq && bytes.Equal(body.Data, c.payload) {
			return time.Since(start), nil
		}
	}
}
