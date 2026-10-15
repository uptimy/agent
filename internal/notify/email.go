package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/uptimy/agent/internal/schema"
)

// SMTP security modes.
const (
	smtpStartTLS = "starttls" // plain connection upgraded with STARTTLS; usually port 587
	smtpTLS      = "tls"      // TLS from the start; usually port 465
	smtpNone     = "none"     // no encryption, for a relay on the same network
)

func init() {
	Register(Channel{
		Type:      "email",
		Reminders: true,
		Label:     "Email",
		Help: "Send alerts through any SMTP server: your mail provider, or a service like Amazon SES, " +
			"Postmark or Resend. Use an app password where your provider supports them.",
		Order: 5,
		Fields: []schema.Field{
			{Key: "smtp_host", Label: "SMTP server", Input: schema.Text, Placeholder: "smtp.example.com", Required: true},
			{Key: "smtp_port", Label: "Port", Input: schema.Number, Placeholder: "587", Default: 587},
			{Key: "smtp_security", Label: "Security", Input: schema.Select, Options: []string{smtpStartTLS, smtpTLS, smtpNone}, Default: smtpStartTLS,
				Hint: "starttls for port 587, tls for port 465"},
			{Key: "smtp_username", Label: "Username", Input: schema.Text},
			{Key: "smtp_password", Label: "Password", Input: schema.Password},
			{Key: "smtp_from", Label: "From", Input: schema.Text, Placeholder: "Uptimy Agent <alerts@example.com>", Required: true, Wide: true},
			{Key: "smtp_to", Label: "To", Input: schema.Text, Placeholder: "oncall@example.com, ops@example.com", Required: true, Wide: true,
				Hint: "Separate addresses with commas"},
		},
		Validate: validateEmail,
		Send:     sendEmail,
	})
}

func validateEmail(c *Config) error {
	c.SMTPHost = strings.TrimSpace(c.SMTPHost)
	c.SMTPSecurity = strings.ToLower(strings.TrimSpace(c.SMTPSecurity))
	if c.SMTPSecurity == "" {
		c.SMTPSecurity = smtpStartTLS
	}
	if c.SMTPPort == 0 {
		c.SMTPPort = 587
		if c.SMTPSecurity == smtpTLS {
			c.SMTPPort = 465
		}
	}
	switch {
	case c.SMTPHost == "" || strings.ContainsAny(c.SMTPHost, " /:"):
		return errors.New("the SMTP server is a hostname, e.g. smtp.example.com")
	case c.SMTPPort < 1 || c.SMTPPort > 65535:
		return errors.New("the port must be between 1 and 65535")
	case c.SMTPSecurity != smtpStartTLS && c.SMTPSecurity != smtpTLS && c.SMTPSecurity != smtpNone:
		return errors.New("security must be starttls, tls or none")
	}
	if _, err := mail.ParseAddress(c.SMTPFrom); err != nil {
		return errors.New("the From address isn't valid")
	}
	to, err := mail.ParseAddressList(c.SMTPTo)
	if err != nil || len(to) == 0 {
		return errors.New("enter at least one valid To address, separated by commas")
	}
	return nil
}

func sendEmail(ctx context.Context, _ *Sender, c Config, a Alert) error {
	from, err := mail.ParseAddress(c.SMTPFrom)
	if err != nil {
		return err
	}
	to, err := mail.ParseAddressList(c.SMTPTo)
	if err != nil {
		return err
	}
	msg, err := emailMessage(from, to, a)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(c.SMTPHost, strconv.Itoa(c.SMTPPort))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if c.SMTPSecurity == smtpTLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: c.SMTPHost, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", addr, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)

	client, err := smtp.NewClient(conn, c.SMTPHost)
	if err != nil {
		conn.Close()
		return err
	}
	defer client.Close()
	if c.SMTPSecurity == smtpStartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("the server doesn't offer STARTTLS; choose tls for port 465, or none for an internal relay")
		}
		if err := client.StartTLS(&tls.Config{ServerName: c.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if c.SMTPUsername != "" {
		// PlainAuth refuses to send the password over an unencrypted
		// connection, except to localhost.
		if err := client.Auth(smtp.PlainAuth("", c.SMTPUsername, c.SMTPPassword, c.SMTPHost)); err != nil {
			return fmt.Errorf("signing in: %w", err)
		}
	}
	if err := client.Mail(from.Address); err != nil {
		return err
	}
	for _, r := range to {
		if err := client.Rcpt(r.Address); err != nil {
			return fmt.Errorf("recipient %s: %w", r.Address, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// emailMessage renders the alert as a plain-text email.
func emailMessage(from *mail.Address, to []*mail.Address, a Alert) ([]byte, error) {
	subject := a.Icon() + " " + a.Title()
	if a.Test {
		subject = "[Test] " + subject
	}
	recipients := make([]string, len(to))
	for i, r := range to {
		recipients[i] = r.String()
	}

	var body bytes.Buffer
	qp := quotedprintable.NewWriter(&body)
	fmt.Fprintf(qp, "%s\r\n\r\n%s\r\n", a.Title(), strings.ReplaceAll(alertDetails(a), "\n", "\r\n"))
	if a.Test {
		fmt.Fprint(qp, "\r\nThis is a test from Uptimy Agent. Alerts will look like this.\r\n")
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}

	var m bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&m, "%s: %s\r\n", k, v) }
	header("From", from.String())
	header("To", strings.Join(recipients, ", "))
	// mime.QEncoding also keeps a monitor name from smuggling in headers.
	header("Subject", mime.QEncoding.Encode("utf-8", strings.NewReplacer("\r", " ", "\n", " ").Replace(subject)))
	header("Date", a.Time.Format(time.RFC1123Z))
	header("MIME-Version", "1.0")
	header("Content-Type", `text/plain; charset="utf-8"`)
	header("Content-Transfer-Encoding", "quoted-printable")
	header("Auto-Submitted", "auto-generated")
	m.WriteString("\r\n")
	m.Write(body.Bytes())
	return m.Bytes(), nil
}
