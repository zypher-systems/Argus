package alerting

import (
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

// parse reads a built message back the way a mail client would.
func parse(t *testing.T, raw []byte) (*mail.Message, map[string]string) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("not a valid message: %v\n%s", err, raw)
	}
	parts := map[string]string{}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("want multipart, got %s", mediaType)
	}
	r := multipart.NewReader(msg.Body, params["boundary"])
	for {
		p, err := r.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(p) // multipart decodes quoted-printable
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		parts[ct] = string(body)
	}
	return msg, parts
}

func TestMessageIsWellFormed(t *testing.T) {
	e := Email{Subject: "[Argus] DOWN: Café & Co", HTML: "<p>" + strings.Repeat("x", 3000) + "</p>", Text: "down"}
	raw := buildMessage("Argus Alerts <alerts@example.com>", "ops@example.com", e, time.Unix(1_700_000_000, 0))
	for i, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line %d is %d characters; SMTP allows 998", i, len(line))
		}
	}
	msg, parts := parse(t, raw)
	dec := new(mime.WordDecoder)
	subject, err := dec.DecodeHeader(msg.Header.Get("Subject"))
	if err != nil || subject != "[Argus] DOWN: Café & Co" {
		t.Fatalf("subject %q %v", subject, err)
	}
	for _, h := range []string{"Date", "Message-ID"} {
		if msg.Header.Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
	if !strings.HasSuffix(msg.Header.Get("Message-ID"), "@example.com>") {
		t.Errorf("Message-ID should use the sender's domain: %s", msg.Header.Get("Message-ID"))
	}
	if parts["text/plain"] != "down" || !strings.Contains(parts["text/html"], strings.Repeat("x", 3000)) {
		t.Fatalf("parts did not survive encoding: %v", parts)
	}
}

func TestMessageHeadersCannotBeInjected(t *testing.T) {
	e := Email{Subject: "Shop\r\nBcc: attacker@example.com", HTML: "x", Text: "x",
		Headers: map[string]string{"X-Note": "a\r\nBcc: attacker@example.com", "Bad: Name": "v"}}
	raw := string(buildMessage("alerts@example.com", "ops@example.com", e, time.Now()))
	msg, _ := parse(t, []byte(raw))
	if msg.Header.Get("Bcc") != "" {
		t.Fatal("a line break in a value must not start a new header")
	}
	if strings.Contains(raw, "Bad: Name") {
		t.Fatal("a header name with a colon or space is dropped")
	}
}

func TestAlertEmail(t *testing.T) {
	p := Payload{
		Monitor: store.Monitor{ID: 7, Name: "R&D <API>", Target: "https://rd.example.com"},
		Type:    "down", At: time.Date(2026, 9, 23, 4, 5, 6, 0, time.UTC),
		Detail:  "down from 2 of 3 locations (Frankfurt, Virginia): status 503",
		BaseURL: "https://argus.example.com",
	}
	e := buildEmail(p)
	if e.Subject != "[Argus] DOWN: R&D <API>" {
		t.Fatalf("subjects are plain text, not HTML: %q", e.Subject)
	}
	if strings.Contains(e.HTML, "<API>") || !strings.Contains(e.HTML, "R&amp;D &lt;API&gt;") {
		t.Fatal("names are escaped in the HTML")
	}
	for _, want := range []string{"R&D <API> is down", "Target: https://rd.example.com", "Frankfurt, Virginia", "https://argus.example.com/monitors/7"} {
		if !strings.Contains(e.Text, want) {
			t.Errorf("text part missing %q:\n%s", want, e.Text)
		}
	}
	// Without a base URL there is nowhere to link to.
	p.BaseURL = ""
	if strings.Contains(buildEmail(p).HTML, "/monitors/7") {
		t.Fatal("no link without a base URL")
	}
	// A configured product name replaces Argus.
	p.Brand = "Acme Watch"
	if e := buildEmail(p); !strings.HasPrefix(e.Subject, "[Acme Watch] DOWN") || !strings.Contains(e.Text, "Acme Watch") {
		t.Fatalf("brand not applied: %q", e.Subject)
	}
}

func TestEveryAlertTypeHasCopy(t *testing.T) {
	for _, kind := range []string{"down", "recovery", "escalation", "resend", "slow", "cert", "domain", "probe_down", "probe_up", "test"} {
		tone, eyebrow, title, tag := alertCopy(Payload{Type: kind, Monitor: store.Monitor{Name: "Shop"}})
		if tone == "" || eyebrow == "" || title == "" || tag == "" || alertLead(Payload{Type: kind}) == "" {
			t.Errorf("%s is missing copy", kind)
		}
	}
}

func TestIncidentEmailCanBeUnsubscribed(t *testing.T) {
	inc := store.Incident{Title: "Checkout errors", Status: "identified", Impact: "major", MonitorName: "Checkout",
		Updates: []store.IncidentUpdate{{Message: "A bad deploy; rolling back."}}}
	e := incidentEmail(inc, "Acme status", "https://status.acme.test/status/acme", "https://status.acme.test", "unsub-token-123")
	if e.Subject != "[Acme status] Identified: Checkout errors" {
		t.Fatalf("subject %q", e.Subject)
	}
	if e.Headers["List-Unsubscribe"] != "<https://status.acme.test/api/subscribe/unsubscribe?token=unsub-token-123>" ||
		e.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
		t.Fatalf("one-click headers: %v", e.Headers)
	}
	if !strings.Contains(e.HTML, `href="https://status.acme.test/subscribe/unsubscribe?token=unsub-token-123"`) ||
		!strings.Contains(e.Text, "Unsubscribe: https://status.acme.test/subscribe/unsubscribe?token=unsub-token-123") {
		t.Fatal("a visible unsubscribe link belongs in both parts")
	}
	if !strings.Contains(e.Text, "A bad deploy; rolling back.") || !strings.Contains(e.Text, "Affects: Checkout") {
		t.Fatalf("the update and what it affects should be in the email:\n%s", e.Text)
	}
	// Without a known address, no half-made links.
	if e := incidentEmail(inc, "Acme status", "/status/acme", "", "unsub-token-123"); e.Headers != nil || strings.Contains(e.HTML, "unsubscribe") {
		t.Fatal("no unsubscribe link can be made without an absolute address")
	}
}
