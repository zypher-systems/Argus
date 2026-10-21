package alerting

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"argus/internal/store"
)

type Mailer struct {
	store *store.Store
}

func NewMailer(st *store.Store) *Mailer {
	return &Mailer{store: st}
}

func (m *Mailer) Configured(ctx context.Context) (bool, error) {
	smtpCfg, err := m.store.GetSMTPSettings(ctx)
	if err != nil {
		return false, err
	}
	return smtpCfg.Host != "", nil
}

// Email is one message, as HTML and as plain text. The text part is what
// pagers, email-to-SMS gateways, and text-only clients show, and its absence
// counts against a message with spam filters.
type Email struct {
	Subject string
	HTML    string
	Text    string
	// Headers are extra headers, such as List-Unsubscribe.
	Headers map[string]string
}

// Send delivers an HTML-only message. Prefer SendEmail with a text part.
func (m *Mailer) Send(ctx context.Context, subject, htmlBody string, recipients []string) error {
	return m.SendEmail(ctx, Email{Subject: subject, HTML: htmlBody}, recipients)
}

func (m *Mailer) SendEmail(ctx context.Context, e Email, recipients []string) error {
	cfg, err := m.store.GetSMTPSettings(ctx)
	if err != nil {
		return err
	}
	if cfg.Host == "" {
		return fmt.Errorf("smtp is not configured")
	}
	if len(recipients) == 0 {
		return fmt.Errorf("no recipients")
	}
	from := cfg.From
	if from == "" {
		from = cfg.Username
	}

	var conn net.Conn
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	switch cfg.TLSMode {
	case "ssl":
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: cfg.Host})
	default:
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	defer func() { _ = conn.Close() }()

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if cfg.TLSMode == "starttls" {
		if err := client.StartTLS(&tls.Config{ServerName: cfg.Host}); err != nil {
			return fmt.Errorf("starttls: %w", err)
		}
	}
	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}
	// The envelope takes a bare address; From may carry a display name.
	envelope := from
	if a, err := mail.ParseAddress(from); err == nil {
		envelope = a.Address
	}
	if err := client.Mail(envelope); err != nil {
		return err
	}
	// One address the server refuses (a typo, someone who has left) must not
	// cost everyone else on the channel their alert.
	accepted := 0
	var refused []string
	var refusal error
	for _, r := range recipients {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if err := client.Rcpt(r); err != nil {
			refused, refusal = append(refused, r), err
			continue
		}
		accepted++
	}
	if accepted == 0 {
		if refusal != nil {
			return fmt.Errorf("every recipient was refused: %w", refusal)
		}
		return fmt.Errorf("no recipients")
	}
	if len(refused) > 0 {
		slog.Warn("mail server refused some recipients; sent to the rest", "refused", strings.Join(refused, ", "), "error", refusal)
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	msg := buildMessage(from, strings.Join(recipients, ", "), e, time.Now())
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func SplitRecipients(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func ValidEmailDest(raw string) (string, error) {
	recips := SplitRecipients(raw)
	if len(recips) == 0 {
		return "", fmt.Errorf("at least one recipient is required")
	}
	for _, r := range recips {
		at := strings.IndexByte(r, '@')
		if at <= 0 || at == len(r)-1 || strings.ContainsAny(r, " <>") {
			return "", fmt.Errorf("invalid recipient")
		}
	}
	return strings.Join(recips, ", "), nil
}
