package alerting

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

// headerSafe removes line breaks, which in a header would start a new one.
func headerSafe(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(s)), " ")
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// buildMessage writes a complete RFC 5322 message. Headers are encoded so
// any monitor name survives; both parts are quoted-printable, which keeps
// every line under SMTP's 998-character limit.
func buildMessage(from, to string, e Email, now time.Time) []byte {
	domain := "argus.local"
	fromHeader := headerSafe(from)
	if a, err := mail.ParseAddress(from); err == nil {
		fromHeader = a.String() // encodes a non-ASCII display name
		if at := strings.LastIndexByte(a.Address, '@'); at >= 0 && at < len(a.Address)-1 {
			domain = a.Address[at+1:]
		}
	}
	var b bytes.Buffer
	header := func(k, v string) { fmt.Fprintf(&b, "%s: %s\r\n", k, v) }
	header("From", fromHeader)
	header("To", headerSafe(to))
	header("Subject", mime.QEncoding.Encode("utf-8", headerSafe(e.Subject)))
	header("Date", now.UTC().Format(time.RFC1123Z))
	header("Message-ID", fmt.Sprintf("<%s@%s>", randomToken(16), domain))
	header("MIME-Version", "1.0")
	for k, v := range e.Headers {
		if k = headerSafe(k); k != "" && !strings.ContainsAny(k, ": ") {
			header(k, headerSafe(v))
		}
	}

	part := func(contentType, body string) {
		fmt.Fprintf(&b, "Content-Type: %s; charset=UTF-8\r\n", contentType)
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		w := quotedprintable.NewWriter(&b)
		_, _ = w.Write([]byte(strings.ReplaceAll(body, "\r\n", "\n")))
		_ = w.Close()
		b.WriteString("\r\n")
	}
	if e.Text == "" {
		part("text/html", e.HTML)
		return b.Bytes()
	}
	boundary := "argus-" + randomToken(12)
	header("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary))
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	part("text/plain", e.Text)
	fmt.Fprintf(&b, "--%s\r\n", boundary)
	part("text/html", e.HTML)
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}
