package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var downAlert = Alert{
	MonitorID: 7, MonitorName: "Checkout API", Kind: "healthcheck", Target: "https://shop.example/health",
	Status: "down", Message: "HTTP 503", Time: time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC),
}

func testSender() *Sender { return NewSender(slog.New(slog.NewTextHandler(io.Discard, nil))) }

// captureHTTP records the JSON bodies and headers posted to it.
type captureHTTP struct {
	*httptest.Server
	mu      sync.Mutex
	bodies  []map[string]any
	headers []http.Header
}

func newCaptureHTTP(t *testing.T) *captureHTTP {
	c := &captureHTTP{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		c.mu.Lock()
		c.bodies, c.headers = append(c.bodies, body), append(c.headers, r.Header)
		c.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(c.Close)
	return c
}

func send(t *testing.T, n Notifier, a Alert) {
	t.Helper()
	if err := n.Normalize(); err != nil {
		t.Fatalf("normalize %s: %v", n.Type, err)
	}
	if err := testSender().Send(context.Background(), n, a); err != nil {
		t.Fatalf("send %s: %v", n.Type, err)
	}
}

func TestNtfy(t *testing.T) {
	srv := newCaptureHTTP(t)
	send(t, Notifier{Name: "phone", Type: "ntfy", Config: Config{URL: srv.URL + "/", Topic: "acme-alerts", Token: "tk_secret"}}, downAlert)
	body := srv.bodies[0]
	if body["topic"] != "acme-alerts" || body["title"] != "Checkout API is down" || body["priority"] != float64(5) ||
		!strings.Contains(body["message"].(string), "HTTP 503") {
		t.Fatalf("body: %v", body)
	}
	if srv.headers[0].Get("Authorization") != "Bearer tk_secret" {
		t.Fatalf("auth: %v", srv.headers[0])
	}

	n := Notifier{Name: "x", Type: "ntfy", Config: Config{Topic: "ok"}}
	if err := n.Normalize(); err != nil || n.Config.URL != defaultNtfyServer {
		t.Fatalf("default server: %+v %v", n.Config, err)
	}
	if err := (&Notifier{Name: "x", Type: "ntfy", Config: Config{Topic: "has spaces"}}).Normalize(); err == nil {
		t.Fatal("bad topic accepted")
	}
}

func TestTeams(t *testing.T) {
	srv := newCaptureHTTP(t)
	send(t, Notifier{Name: "ops", Type: "teams", Config: Config{URL: srv.URL}}, downAlert)
	raw, _ := json.Marshal(srv.bodies[0])
	for _, want := range []string{`"application/vnd.microsoft.card.adaptive"`, `Checkout API is down`, `"Attention"`, `HTTP 503`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("card lacks %s: %s", want, raw)
		}
	}
}

func TestPagerDuty(t *testing.T) {
	srv := newCaptureHTTP(t)
	defer func(u string) { pagerDutyURL = u }(pagerDutyURL)
	pagerDutyURL = srv.URL

	n := Notifier{Name: "on-call", Type: "pagerduty", Config: Config{RoutingKey: strings.Repeat("a", 32)}}
	send(t, n, downAlert)
	up := downAlert
	up.Status = "up"
	send(t, n, up)
	if len(srv.bodies) != 2 || srv.bodies[0]["event_action"] != "trigger" || srv.bodies[1]["event_action"] != "resolve" ||
		srv.bodies[0]["dedup_key"] != "uptimy-agent/7" || srv.bodies[1]["dedup_key"] != "uptimy-agent/7" {
		t.Fatalf("events: %v", srv.bodies)
	}

	// A test opens an incident and resolves it right away.
	test := downAlert
	test.Test = true
	send(t, n, test)
	if len(srv.bodies) != 4 || srv.bodies[2]["event_action"] != "trigger" || srv.bodies[3]["event_action"] != "resolve" {
		t.Fatalf("test events: %v", srv.bodies[2:])
	}
	if err := (&Notifier{Name: "x", Type: "pagerduty", Config: Config{RoutingKey: "short"}}).Normalize(); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestEmail(t *testing.T) {
	smtpSrv := newFakeSMTP(t)
	n := Notifier{Name: "ops mail", Type: "email", Config: Config{
		SMTPHost: "127.0.0.1", SMTPPort: smtpSrv.port, SMTPSecurity: "none",
		SMTPFrom: "Uptimy Agent <agent@example.com>", SMTPTo: "oncall@example.com, Ops <ops@example.com>",
	}}
	send(t, n, downAlert)

	got := smtpSrv.message()
	if got.from != "agent@example.com" || strings.Join(got.to, ",") != "oncall@example.com,ops@example.com" {
		t.Fatalf("envelope: %+v", got)
	}
	subject := header(got.data, "Subject")
	if decoded, err := new(mime.WordDecoder).DecodeHeader(subject); err != nil || decoded != "🔴 Checkout API is down" {
		t.Fatalf("subject %q: %q %v", subject, decoded, err)
	}
	if !strings.Contains(got.data, "HTTP 503") || header(got.data, "Auto-Submitted") != "auto-generated" {
		t.Fatalf("message:\n%s", got.data)
	}

	// STARTTLS is the default, and a server without it is refused rather
	// than silently sending in the clear.
	n.Config.SMTPSecurity = ""
	if err := n.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := testSender().Send(context.Background(), n, downAlert); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("plain server with starttls: %v", err)
	}

	for name, cfg := range map[string]Config{
		"no host":     {SMTPFrom: "a@example.com", SMTPTo: "b@example.com"},
		"bad from":    {SMTPHost: "smtp.example.com", SMTPFrom: "nobody", SMTPTo: "b@example.com"},
		"no to":       {SMTPHost: "smtp.example.com", SMTPFrom: "a@example.com"},
		"bad port":    {SMTPHost: "smtp.example.com", SMTPPort: 70000, SMTPFrom: "a@example.com", SMTPTo: "b@example.com"},
		"bad setting": {SMTPHost: "smtp.example.com", SMTPSecurity: "ssl3", SMTPFrom: "a@example.com", SMTPTo: "b@example.com"},
	} {
		if err := (&Notifier{Name: "x", Type: "email", Config: cfg}).Normalize(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNormalizeDropsOtherChannelsSettings(t *testing.T) {
	n := Notifier{Name: "x", Type: "slack", Config: Config{URL: "https://hooks.slack.com/x", BotToken: "left over", SMTPHost: "smtp"}}
	if err := n.Normalize(); err != nil {
		t.Fatal(err)
	}
	if n.Config != (Config{URL: "https://hooks.slack.com/x"}) {
		t.Fatalf("kept: %+v", n.Config)
	}
}

func header(msg, name string) string {
	for _, line := range strings.Split(msg, "\r\n") {
		if v, ok := strings.CutPrefix(line, name+": "); ok {
			return v
		}
	}
	return ""
}

// fakeSMTP is just enough of an SMTP server to receive one message.
type fakeSMTP struct {
	port int
	got  chan smtpMessage
}

type smtpMessage struct {
	from string
	to   []string
	data string
}

// It never offers STARTTLS.
func newFakeSMTP(t *testing.T) *fakeSMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f := &fakeSMTP{port: ln.Addr().(*net.TCPAddr).Port, got: make(chan smtpMessage, 4)}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(s string) { io.WriteString(conn, s+"\r\n") }
	reply("220 fake ESMTP")
	var m smtpMessage
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			reply("250 fake")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			m.from = strings.Trim(strings.TrimSpace(line)[10:], "<>")
			reply("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			m.to = append(m.to, strings.Trim(strings.TrimSpace(line)[8:], "<>"))
			reply("250 ok")
		case cmd == "DATA":
			reply("354 go ahead")
			var data strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				data.WriteString(l)
			}
			m.data = data.String()
			f.got <- m
			reply("250 queued")
		case cmd == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

func (f *fakeSMTP) message() smtpMessage {
	select {
	case m := <-f.got:
		return m
	case <-time.After(5 * time.Second):
		return smtpMessage{data: "timed out waiting for " + strconv.Itoa(f.port)}
	}
}

func TestShoutrrr(t *testing.T) {
	srv := newCaptureHTTP(t)
	host := strings.TrimPrefix(srv.URL, "http://")
	// Shoutrrr's generic service posts JSON to any URL.
	send(t, Notifier{Name: "s", Type: "shoutrrr", Config: Config{URL: "generic://" + host + "/hook?disabletls=yes&template=json"}}, downAlert)
	if len(srv.bodies) != 1 {
		t.Fatalf("got %d requests", len(srv.bodies))
	}
	b := srv.bodies[0]
	if b["title"] != "Uptimy Agent" || !strings.HasPrefix(b["message"].(string), "🔴 Checkout API is down\nHTTP 503\n") {
		t.Fatalf("unexpected body %v", b)
	}

	for url, want := range map[string]string{
		"":                       "enter a Shoutrrr URL",
		"nonsense":               "enter a Shoutrrr URL",
		"carrierpigeon://x":      `doesn't support "carrierpigeon"`,
		"pushover://shoutrrr:@u": "invalid Shoutrrr URL",
	} {
		n := Notifier{Name: "s", Type: "shoutrrr", Config: Config{URL: url}}
		if err := n.Normalize(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", url, err, want)
		}
	}
}

// A failed send's error never repeats the URL, which holds credentials.
func TestShoutrrrErrorHidesURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	// A token the service rejects as malformed, and one the server refuses.
	for _, secret := range []string{"s3cr3t-app-token", "AbCdEfGhIjKlMnO"} {
		n := Notifier{Name: "s", Type: "shoutrrr", Config: Config{URL: "gotify://" + strings.TrimPrefix(srv.URL, "http://") + "/" + secret + "?disabletls=yes"}}
		if err := n.Normalize(); err != nil {
			t.Fatal(err)
		}
		err := testSender().Send(context.Background(), n, downAlert)
		if err == nil {
			t.Fatalf("%s: expected an error", secret)
		}
		t.Logf("%s: %v", secret, err)
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks the token: %v", err)
		}
	}
}
