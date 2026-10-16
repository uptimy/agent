package kuma

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/notify"
)

// convertMonitor turns a Kuma monitor row into a monitor, or says why it
// can't be.
func convertMonitor(r row) (m monitor.Monitor, notes []string, reason string) {
	typ := r.str("type")
	m = monitor.Monitor{Name: strings.TrimSpace(r.str("name")), Paused: !r.bool("active"), Source: monitor.SourceUI}
	if r.bool("upside_down") {
		return m, nil, "upside-down mode (up when the check fails) isn't supported"
	}
	if typ == "push" {
		return convertPush(r, m)
	}

	c := &monitor.Check{
		IntervalSeconds:  int(max(10, r.int("interval"))),
		FailureThreshold: int(min(10, max(1, r.int("maxretries")+1))), // Kuma retries, then marks down
	}
	if t := r.int("timeout"); t > 0 {
		c.TimeoutSeconds = int(min(t, int64(c.IntervalSeconds)))
	}
	host := r.str("hostname")
	switch typ {
	case "http", "keyword":
		c.Type, c.Target = monitor.TypeHTTP, r.str("url")
		c.Config.Method = strings.ToUpper(r.str("method"))
		c.Config.IgnoreTLS = r.bool("ignore_tls")
		if codes := statusCodes(r.str("accepted_statuscodes_json")); codes != "" {
			c.Config.ExpectedStatus = codes
		}
		if typ == "keyword" {
			if r.bool("invert_keyword") {
				return m, nil, "inverted keyword checks (keyword must be absent) aren't supported"
			}
			c.Config.Keyword = r.str("keyword")
		}
		headers, why := httpHeaders(r)
		if why != "" {
			return m, nil, why
		}
		c.Config.Headers = headers
		if strings.TrimSpace(r.str("body")) != "" {
			notes = append(notes, "the request body wasn't imported")
		}
	case "port":
		c.Type, c.Target = monitor.TypeTCP, net.JoinHostPort(host, strconv.FormatInt(r.int("port"), 10))
	case "ping":
		c.Type, c.Target = monitor.TypePing, host
	case "dns":
		c.Type, c.Target = monitor.TypeDNS, host
		c.Config.RecordType = r.str("dns_resolve_type")
		if !slices.Contains([]string{"", "A", "AAAA", "CNAME", "MX", "TXT", "NS"}, c.Config.RecordType) {
			return m, nil, c.Config.RecordType + " records aren't supported yet"
		}
		if server := r.str("dns_resolve_server"); server != "" {
			c.Config.Resolver = server
			if p := r.int("port"); p != 0 && p != 53 {
				c.Config.Resolver = net.JoinHostPort(server, strconv.FormatInt(p, 10))
			}
		}
	case "postgres", "mysql", "redis":
		c.Type, c.Target = monitor.Type(typ), r.str("database_connection_string")
		if q := strings.TrimSpace(r.str("database_query")); q != "" && typ != "redis" {
			c.Config.Query = q
		}
	case "tls":
		c.Type, c.Target = monitor.TypeTLS, host
		if p := r.int("port"); p != 0 && p != 443 {
			c.Target = net.JoinHostPort(host, strconv.FormatInt(p, 10))
		}
	case "group":
		return m, nil, "groups aren't imported; the monitors in them are"
	default:
		return m, nil, fmt.Sprintf("%s monitors aren't supported yet", kumaTypeName(typ))
	}
	if n := r.int("resend_interval"); n > 0 {
		// Kuma repeats per monitor, every n checks; the agent per channel.
		notes = append(notes, fmt.Sprintf("Kuma re-sent its alert every %d checks while down: turn on \"Remind while down\" on its channels", n))
	}
	m.Kind, m.Check = monitor.KindHealthcheck, c
	return m, notes, ""
}

// convertPush turns a Kuma push monitor into a heartbeat on an interval. Kuma
// marks it down once a push is overdue and its retries are used up; the grace
// period covers the retries.
func convertPush(r row, m monitor.Monitor) (monitor.Monitor, []string, string) {
	notes := []string{"has a new ping URL: point the job at it after importing"}
	every := r.int("interval")
	if every < 60 {
		every = 60
		notes = append(notes, "now expects a ping every minute (heartbeats can't be more frequent)")
	}
	grace := max(60, r.int("maxretries")*max(r.int("retry_interval"), every))
	grace = min(grace, 7*24*3600)
	m.Kind = monitor.KindHeartbeat
	m.Heartbeat = &monitor.Heartbeat{Token: monitor.NewToken(), EverySeconds: int(every), GraceSeconds: int(grace)}
	return m, notes, ""
}

// httpHeaders carries over Kuma's custom headers and basic auth.
func httpHeaders(r row) (map[string]string, string) {
	headers := map[string]string{}
	if raw := strings.TrimSpace(r.str("headers")); raw != "" {
		var h map[string]any
		if err := json.Unmarshal([]byte(raw), &h); err != nil {
			return nil, "its custom headers couldn't be read"
		}
		for k, v := range h {
			headers[k] = fmt.Sprint(v)
		}
	}
	basic := func() {
		if user := r.str("basic_auth_user"); user != "" {
			headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+r.str("basic_auth_pass")))
		}
	}
	_, hasMethod := r["auth_method"]
	switch method := r.str("auth_method"); method {
	case "":
		// Kuma before auth_method existed sent basic auth whenever it was set.
		if !hasMethod {
			basic()
		}
	case "basic":
		basic()
	case "bearer":
		if token := r.str("bearer_token"); token != "" {
			headers["Authorization"] = "Bearer " + token
		}
	default:
		return nil, method + " authentication isn't supported"
	}
	if len(headers) == 0 {
		return nil, ""
	}
	return headers, ""
}

// statusCodes turns Kuma's ["200-299","301"] into "200-299,301".
func statusCodes(raw string) string {
	var codes []string
	if json.Unmarshal([]byte(raw), &codes) != nil {
		return ""
	}
	return strings.Join(codes, ",")
}

func kumaTypeName(t string) string {
	names := map[string]string{
		"json-query": "JSON query", "docker": "Docker container", "grpc-keyword": "gRPC",
		"real-browser": "Browser", "mongodb": "MongoDB", "sqlserver": "SQL Server", "mqtt": "MQTT",
		"kafka-producer": "Kafka", "gamedig": "Game server", "steam": "Steam", "radius": "RADIUS",
		"tailscale-ping": "Tailscale ping", "snmp": "SNMP", "rabbitmq": "RabbitMQ", "smtp": "SMTP",
	}
	if n, ok := names[t]; ok {
		return n
	}
	return t
}

// convertNotification turns a Kuma notification's settings into a notifier.
func convertNotification(cfg row) (n notify.Notifier, notes []string, reason string) {
	switch typ := cfg.str("type"); typ {
	case "slack":
		n.Type, n.Config.URL = "slack", cfg.str("slackwebhookURL")
	case "discord":
		n.Type, n.Config.URL = "discord", cfg.str("discordWebhookUrl")
	case "teams":
		n.Type, n.Config.URL = "teams", cfg.str("webhookUrl")
	case "telegram":
		n.Type, n.Config.BotToken, n.Config.ChatID = "telegram", cfg.str("telegramBotToken"), cfg.str("telegramChatID")
		if s := cfg.str("telegramServerUrl"); s != "" && s != "https://api.telegram.org" {
			notes = append(notes, "now sends through api.telegram.org, not "+s)
		}
	case "webhook":
		n.Type, n.Config.URL = "webhook", cfg.str("webhookURL")
		notes = append(notes, "now posts the agent's JSON (monitor_name, status, message, time), not Kuma's")
	case "ntfy":
		n.Type, n.Config.URL, n.Config.Topic = "ntfy", cfg.str("ntfyserverurl"), cfg.str("ntfytopic")
		switch cfg.str("ntfyAuthenticationMethod") {
		case "accessToken":
			n.Config.Token = cfg.str("ntfyaccesstoken")
		case "usernamePassword":
			return n, nil, "ntfy with a username and password isn't supported; use an access token"
		}
	case "smtp":
		n.Type = "email"
		n.Config.SMTPHost, n.Config.SMTPPort = cfg.str("smtpHost"), int(cfg.int("smtpPort"))
		n.Config.SMTPUsername, n.Config.SMTPPassword = cfg.str("smtpUsername"), cfg.str("smtpPassword")
		n.Config.SMTPFrom = cfg.str("smtpFrom")
		switch {
		case cfg.bool("smtpSecure"):
			n.Config.SMTPSecurity = "tls"
		case cfg.bool("smtpIgnoreSTARTTLS"):
			n.Config.SMTPSecurity = "none"
		default:
			n.Config.SMTPSecurity = "starttls"
		}
		to := []string{cfg.str("smtpTo"), cfg.str("smtpCC"), cfg.str("smtpBCC")}
		n.Config.SMTPTo = strings.Trim(strings.Join(to, ","), ", ")
		if cfg.str("smtpCC") != "" || cfg.str("smtpBCC") != "" {
			notes = append(notes, "CC and BCC recipients were added to To")
		}
	case "PagerDuty":
		n.Type, n.Config.RoutingKey = "pagerduty", cfg.str("pagerdutyIntegrationKey")
	case "pushover", "gotify", "matrix", "mattermost", "GoogleChat", "rocket.chat", "Opsgenie":
		u, reason := shoutrrrURL(typ, cfg)
		if reason != "" {
			return n, nil, reason
		}
		n.Type, n.Config.URL = "shoutrrr", u
		notes = append(notes, "sends through Shoutrrr (the \"More services\" channel)")
	default:
		return n, nil, fmt.Sprintf("%s notifications aren't supported yet", typ)
	}
	if n.Type == "ntfy" && n.Config.URL != "" && !validURL(n.Config.URL) {
		return n, nil, "its ntfy server URL isn't valid"
	}
	return n, notes, ""
}

// shoutrrrURL builds the Shoutrrr URL for a Kuma notification the agent sends
// through its "More services (Shoutrrr)" channel. Formats:
// https://shoutrrr.nickfedor.com/latest/services/overview/
func shoutrrrURL(typ string, cfg row) (string, string) {
	esc := url.PathEscape
	switch typ {
	case "pushover":
		q := url.Values{}
		if d := cfg.str("pushoverdevice"); d != "" {
			q.Set("devices", d)
		}
		if p := cfg.str("pushoverpriority"); p != "" {
			q.Set("priority", p)
		}
		return withQuery("pushover://shoutrrr:"+esc(cfg.str("pushoverapptoken"))+"@"+esc(cfg.str("pushoveruserkey"))+"/", q), ""
	case "gotify":
		server, err := url.Parse(cfg.str("gotifyserverurl"))
		if err != nil || server.Host == "" {
			return "", "its Gotify server URL isn't valid"
		}
		q := url.Values{}
		if server.Scheme == "http" {
			q.Set("disabletls", "yes")
		}
		if p := cfg.str("gotifyPriority"); p != "" {
			q.Set("priority", p)
		}
		return withQuery("gotify://"+server.Host+strings.TrimSuffix(server.Path, "/")+"/"+esc(cfg.str("gotifyapplicationToken")), q), ""
	case "matrix":
		server, err := url.Parse(cfg.str("homeserverUrl"))
		if err != nil || server.Host == "" {
			return "", "its Matrix homeserver URL isn't valid"
		}
		// No user: Shoutrrr uses the password as an access token.
		q := url.Values{"rooms": {cfg.str("internalRoomId")}}
		if server.Scheme == "http" {
			q.Set("disableTLS", "yes")
		}
		return withQuery("matrix://:"+esc(cfg.str("accessToken"))+"@"+server.Host+"/", q), ""
	case "mattermost":
		hook, err := url.Parse(cfg.str("mattermostWebhookUrl"))
		path, ok := strings.CutPrefix(hookPath(hook), "/hooks/")
		if err != nil || hook.Host == "" || !ok {
			return "", "its Mattermost webhook URL isn't valid"
		}
		user := ""
		if u := cfg.str("mattermostusername"); u != "" {
			user = esc(u) + "@"
		}
		u := "mattermost://" + user + hook.Host + "/" + path
		if c := strings.TrimPrefix(cfg.str("mattermostchannel"), "#"); c != "" {
			u += "/" + esc(c)
		}
		return u, ""
	case "GoogleChat":
		hook, err := url.Parse(cfg.str("googleChatWebhookURL"))
		if err != nil || hook.Host == "" {
			return "", "its Google Chat webhook URL isn't valid"
		}
		return "googlechat://" + hook.Host + hook.Path + "?" + hook.RawQuery, ""
	case "rocket.chat":
		hook, err := url.Parse(cfg.str("rocketwebhookURL"))
		path, ok := strings.CutPrefix(hookPath(hook), "/hooks/")
		if err != nil || hook.Host == "" || !ok {
			return "", "its Rocket.Chat webhook URL isn't valid"
		}
		user := ""
		if u := cfg.str("rocketusername"); u != "" {
			user = esc(u) + "@"
		}
		u := "rocketchat://" + user + hook.Host + "/" + path
		if c := cfg.str("rocketchannel"); c != "" {
			u += "/" + esc(c)
		}
		return u, ""
	case "Opsgenie":
		host := "api.opsgenie.com"
		if strings.EqualFold(cfg.str("opsgenieRegion"), "eu") {
			host = "api.eu.opsgenie.com"
		}
		return "opsgenie://" + host + "/" + esc(cfg.str("opsgenieApiKey")), ""
	}
	return "", typ + " notifications aren't supported yet"
}

func hookPath(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Path
}

func withQuery(u string, q url.Values) string {
	if len(q) == 0 {
		return u
	}
	return u + "?" + q.Encode()
}
