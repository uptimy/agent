package checks

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/uptimy/agent/internal/monitor"
)

func init() {
	register(monitor.TypeRedis, func(ctx context.Context, _ *Checker, m monitor.Check) Outcome { return redis(ctx, m) })
}

// redis connects, authenticates, selects the database and sends PING. The
// protocol for that is a few lines, so it's spoken directly rather than
// pulling in a client library.
func redis(ctx context.Context, m monitor.Check) Outcome {
	target, err := m.ResolvedTarget()
	if err != nil {
		return Outcome{Message: err.Error()}
	}
	u, err := url.Parse(target)
	if err != nil {
		return dbFailure(target, err)
	}
	addr := u.Host
	if u.Port() == "" {
		addr = net.JoinHostPort(u.Hostname(), "6379")
	}

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return dbFailure(target, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return dbFailure(target, err)
		}
	}
	if u.Scheme == "rediss" {
		tc := tls.Client(conn, &tls.Config{ServerName: u.Hostname(), InsecureSkipVerify: m.Config.IgnoreTLS}) //nolint:gosec // opt-in per monitor
		if err := tc.HandshakeContext(ctx); err != nil {
			return dbFailure(target, err)
		}
		conn = tc
	}

	rc := respConn{w: conn, r: bufio.NewReader(conn)}
	if password, ok := u.User.Password(); ok && password != "" {
		args := []string{"AUTH", password}
		if user := u.User.Username(); user != "" {
			args = []string{"AUTH", user, password}
		}
		if _, err := rc.do(args...); err != nil {
			return dbFailure(target, fmt.Errorf("auth: %w", err))
		}
	}
	if db := strings.Trim(u.Path, "/"); db != "" && db != "0" {
		if _, err := rc.do("SELECT", db); err != nil {
			return dbFailure(target, fmt.Errorf("select %s: %w", db, err))
		}
	}
	reply, err := rc.do("PING")
	if err != nil {
		return dbFailure(target, err)
	}
	if reply != "PONG" {
		return Outcome{Message: fmt.Sprintf("unexpected reply to PING: %q", truncate(reply, 100)), ErrorClass: classAssertion}
	}
	return Outcome{OK: true, Message: "PONG"}
}

type respConn struct {
	w net.Conn
	r *bufio.Reader
}

// do sends a command and reads a simple-string, error, integer or bulk reply.
func (c respConn) do(args ...string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := c.w.Write([]byte(b.String())); err != nil {
		return "", err
	}
	line, err := c.readLine()
	if err != nil {
		return "", err
	}
	if line == "" {
		return "", errors.New("empty reply")
	}
	switch line[0] {
	case '+', ':':
		return line[1:], nil
	case '-':
		return "", errors.New(line[1:])
	case '$':
		n, err := strconv.Atoi(line[1:])
		if err != nil || n > 1<<20 {
			return "", fmt.Errorf("bad reply %q", truncate(line, 40))
		}
		if n < 0 {
			return "", nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return "", err
		}
		return string(buf[:n]), nil
	default:
		return "", fmt.Errorf("not a Redis server (reply %q)", truncate(line, 40))
	}
}

func (c respConn) readLine() (string, error) {
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
