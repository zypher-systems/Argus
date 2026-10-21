package probe

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	"argus/internal/store"
)

// TLS modes for mail monitors.
const (
	MailTLSNone     = "none"
	MailTLSStartTLS = "starttls"
	MailTLSImplicit = "tls"
)

// mailProtocol is what one mail protocol needs for a connect-and-greet check.
type mailProtocol struct {
	defaultPort string
	// okGreeting reports whether the server's first line means "ready".
	okGreeting func(line string) bool
	// startTLS upgrades an open connection, returning the raw commands to run.
	startTLS func(rw *bufio.ReadWriter) error
	quit     string
}

var mailProtocols = map[string]mailProtocol{
	"smtp": {
		defaultPort: "25",
		okGreeting:  func(l string) bool { return strings.HasPrefix(l, "220") },
		startTLS:    smtpStartTLS,
		quit:        "QUIT\r\n",
	},
	"imap": {
		defaultPort: "143",
		okGreeting:  func(l string) bool { return strings.HasPrefix(l, "* OK") },
		startTLS:    imapStartTLS,
		quit:        "a2 LOGOUT\r\n",
	},
	"pop3": {
		defaultPort: "110",
		okGreeting:  func(l string) bool { return strings.HasPrefix(l, "+OK") },
		startTLS:    pop3StartTLS,
		quit:        "QUIT\r\n",
	},
}

// MailTypes are the mail monitor types.
var MailTypes = []string{"smtp", "imap", "pop3"}

// IsMailType reports whether kind is a mail protocol monitor.
func IsMailType(kind string) bool {
	_, ok := mailProtocols[strings.ToLower(strings.TrimSpace(kind))]
	return ok
}

// MailDefaultPort is the port used when the target names only a host.
func MailDefaultPort(kind, tlsMode string) string {
	switch strings.ToLower(kind) {
	case "smtp":
		if tlsMode == MailTLSImplicit {
			return "465"
		}
		return "25"
	case "imap":
		if tlsMode == MailTLSImplicit {
			return "993"
		}
		return "143"
	case "pop3":
		if tlsMode == MailTLSImplicit {
			return "995"
		}
		return "110"
	}
	return ""
}

// checkMail connects to a mail server, reads its greeting, optionally upgrades
// with STARTTLS, and records the certificate expiry when TLS is involved.
func checkMail(ctx context.Context, m store.Monitor) (CheckOutcome, error) {
	kind := strings.ToLower(m.Type)
	proto, ok := mailProtocols[kind]
	if !ok {
		return CheckOutcome{}, fmt.Errorf("unsupported mail protocol %q", m.Type)
	}
	mode := strings.ToLower(strings.TrimSpace(m.Config.TLSMode))
	if mode == "" {
		mode = MailTLSNone
	}
	timeout := time.Duration(timeoutSeconds(m, 10)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	host, port, err := net.SplitHostPort(strings.TrimSpace(m.Target))
	if err != nil {
		host = strings.TrimSpace(m.Target)
		port = MailDefaultPort(kind, mode)
		if port == "" {
			port = proto.defaultPort
		}
	}
	addr := net.JoinHostPort(host, port)

	var out CheckOutcome
	start := time.Now()
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
		return out, err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	tlsConf := &tls.Config{ServerName: host, InsecureSkipVerify: m.Config.InsecureSkipVerify} //nolint:gosec // operator's choice, same as HTTP checks
	if mode == MailTLSImplicit {
		tlsConn := tls.Client(conn, tlsConf)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
			out.Evidence.TLSError = classifyTLSError(err)
			return out, err
		}
		conn = tlsConn
		out.TLSExpiresAt = certExpiry(tlsConn)
	}

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	greeting, err := rw.ReadString('\n')
	if err != nil {
		out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
		return out, fmt.Errorf("no greeting: %w", err)
	}
	greeting = strings.TrimRight(greeting, "\r\n")
	out.Evidence.BodySnippet = snippetBody([]byte(greeting), 200)
	if !proto.okGreeting(greeting) {
		out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
		return out, fmt.Errorf("unexpected greeting: %s", greeting)
	}

	if mode == MailTLSStartTLS {
		if err := proto.startTLS(rw); err != nil {
			out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
			return out, err
		}
		tlsConn := tls.Client(conn, tlsConf)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
			out.Evidence.TLSError = classifyTLSError(err)
			return out, err
		}
		out.TLSExpiresAt = certExpiry(tlsConn)
		conn = tlsConn
		rw = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	}

	out.LatencyMs = float64(time.Since(start).Microseconds()) / 1000

	// An expected keyword is matched against the greeting, which is where
	// servers put their name and version.
	if kw := strings.TrimSpace(m.Config.Keyword); kw != "" {
		found := strings.Contains(strings.ToLower(greeting), strings.ToLower(kw))
		if found == m.Config.KeywordInvert {
			if m.Config.KeywordInvert {
				return out, fmt.Errorf("greeting contains %q", kw)
			}
			return out, fmt.Errorf("greeting does not contain %q", kw)
		}
	}

	_, _ = rw.WriteString(proto.quit)
	_ = rw.Flush()
	return out, nil
}

func certExpiry(c *tls.Conn) *time.Time {
	certs := c.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil
	}
	t := certs[0].NotAfter.UTC()
	return &t
}

func smtpStartTLS(rw *bufio.ReadWriter) error {
	if _, err := rw.WriteString("EHLO argus\r\n"); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}
	// EHLO answers with one line per capability; the last has a space after the code.
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return fmt.Errorf("EHLO failed: %w", err)
		}
		if len(line) >= 4 && line[3] == ' ' {
			break
		}
	}
	if _, err := rw.WriteString("STARTTLS\r\n"); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}
	line, err := rw.ReadString('\n')
	if err != nil {
		return fmt.Errorf("STARTTLS failed: %w", err)
	}
	if !strings.HasPrefix(line, "220") {
		return fmt.Errorf("server refused STARTTLS: %s", strings.TrimRight(line, "\r\n"))
	}
	return nil
}

func imapStartTLS(rw *bufio.ReadWriter) error {
	if _, err := rw.WriteString("a1 STARTTLS\r\n"); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}
	line, err := rw.ReadString('\n')
	if err != nil {
		return fmt.Errorf("STARTTLS failed: %w", err)
	}
	if !strings.HasPrefix(line, "a1 OK") {
		return fmt.Errorf("server refused STARTTLS: %s", strings.TrimRight(line, "\r\n"))
	}
	return nil
}

func pop3StartTLS(rw *bufio.ReadWriter) error {
	if _, err := rw.WriteString("STLS\r\n"); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}
	line, err := rw.ReadString('\n')
	if err != nil {
		return fmt.Errorf("STLS failed: %w", err)
	}
	if !strings.HasPrefix(line, "+OK") {
		return fmt.Errorf("server refused STLS: %s", strings.TrimRight(line, "\r\n"))
	}
	return nil
}
