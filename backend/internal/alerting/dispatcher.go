package alerting

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"argus/internal/store"
)

type Dispatcher struct {
	store  *store.Store
	mailer *Mailer
	// One drain runs at a time in a process; see DeliverDue. wake tells it
	// that an alert has been queued since it last looked.
	drainMu  sync.Mutex
	draining bool
	wake     chan struct{}
	// now and sendTimeout are fixed except in tests.
	now         func() time.Time
	sendTimeout time.Duration
}

func NewDispatcher(st *store.Store, mailer *Mailer) *Dispatcher {
	return &Dispatcher{
		store: st, mailer: mailer, wake: make(chan struct{}, 1),
		now: time.Now, sendTimeout: 45 * time.Second,
	}
}

type Payload struct {
	Monitor store.Monitor
	Type    string
	// Of is what a recovery recovers from when that is not an outage:
	// "slow" for a monitor back under its latency threshold.
	Of string
	// Key names the alert for PagerDuty and Opsgenie when it is not about a
	// monitor: one probe node's silence must not be folded into another's.
	Key          string
	At           time.Time
	Detail       string
	ExtraLatency *float64
	// BaseURL and Brand shape the email: a link back to the monitor when
	// Argus knows its own address, and the product name from branding.
	BaseURL string
	Brand   string
}

// Decorate fills in what an email needs from settings: the public base URL
func (d *Dispatcher) Decorate(ctx context.Context, p *Payload) {
	all, err := d.store.LoadSettings(ctx)
	if err != nil {
		return
	}
	d.decorate(ctx, p, all)
}

func (d *Dispatcher) decorate(_ context.Context, p *Payload, all store.AllSettings) {
	p.BaseURL = strings.TrimRight(strings.TrimSpace(all.General.BaseURL), "/")
	name := strings.TrimSpace(all.Branding.ProductName)
	if name == "" || name == "Argus" {
		return
	}
	p.Brand = name
}

// DispatchAsync queues an alert for every channel it should reach and
// starts delivering. Queuing is done before it returns: once it has, the
// alert is in the database and will be delivered by this process or the
// next one, so a restart or a provider outage no longer loses it.
func (d *Dispatcher) DispatchAsync(p Payload) {
	d.Dispatch(p, nil)
}

// Dispatch queues an alert if claim, the right to send it, is ours to take,
// and reports whether it was. The claim and the queued alert are one
// transaction: either the alert is on its way and marked as sent for, or
// neither, and whoever looks next will send it.
func (d *Dispatcher) Dispatch(p Payload, claim store.AlertClaim) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	claimed, err := d.enqueue(ctx, p, claim)
	if err != nil {
		slog.Error("alert could not be queued", "monitor", p.Monitor.Name, "type", p.Type, "error", err)
		return false
	}
	if claimed {
		go d.DeliverDue(context.Background())
	}
	return claimed
}

// recoverSend keeps a panic while sending one notification from ending the
// process that every other check and alert runs in.
func recoverSend(what, name string) {
	if r := recover(); r != nil {
		slog.Error("sending "+what+" panicked", "monitor", name, "panic", r, "stack", string(debug.Stack()))
	}
}

func (d *Dispatcher) escalationExtras(p Payload) []string {
	if p.Type != "escalation" {
		return nil
	}
	return SplitRecipients(p.Monitor.EscalateRecipients)
}

// EmailContent is the email an alert sends.
func EmailContent(p Payload) Email {
	return buildEmail(p)
}

// alertCopy is what each kind of alert says: its tone, a label, a headline,
// and the tag in the subject line (kept stable for people's mail filters).
func alertCopy(p Payload) (tone, eyebrow, title, tag string) {
	name := p.Monitor.Name
	switch p.Type {
	case "down":
		return "down", "Monitor down", name + " is down", "DOWN"
	case "recovery":
		return "up", "Recovered", name + " is back up", "RECOVERED"
	case "escalation":
		return "down", "Still down", fmt.Sprintf("%s has been down for %d minutes", name, p.Monitor.EscalateMinutes),
			fmt.Sprintf("STILL DOWN (%d min)", p.Monitor.EscalateMinutes)
	case "resend":
		return "down", "Still down", name + " is still down", "STILL DOWN"
	case "slow":
		return "warn", "Slow", name + " is slow", "SLOW"
	case "cert":
		return "warn", "Certificate expiring", "The certificate for " + name + " expires soon", "CERT EXPIRING"
	case "domain":
		return "warn", "Domain expiring", "The domain registration for " + name + " expires soon", "DOMAIN EXPIRING"
	case "probe_down":
		if p.Key != "" { // one remote probe node, not the probe itself
			return "down", "Probe node silent", name + " stopped reporting", "PROBE NODE DOWN"
		}
		return "down", "Checks have stopped", "The probe stopped reporting", "PROBE DOWN"
	case "probe_up":
		if p.Key != "" {
			return "up", "Probe node back", name + " is reporting again", "PROBE NODE RECOVERED"
		}
		return "up", "Checks resumed", "The probe is reporting again", "PROBE RECOVERED"
	case "test":
		return "info", "Test alert", "This channel works", "Test email"
	default:
		return "info", strings.ToUpper(p.Type), name, strings.ToUpper(p.Type)
	}
}

func alertLead(p Payload) string {
	switch p.Type {
	case "down":
		return "Argus confirmed a failure and will tell you when it recovers."
	case "recovery":
		return "Checks are passing again."
	case "escalation", "resend":
		return "Nobody has acknowledged it yet."
	case "slow":
		return "Responses are over the latency threshold."
	case "cert", "domain":
		return "Renew it before it lapses; Argus warns once for each expiry date."
	case "probe_down":
		if p.Key != "" {
			return "Monitors checked only from this node keep their last status, so an outage there would go unnoticed until it reports again."
		}
		return "No checks are running, so an outage would go unnoticed until this recovers."
	case "probe_up":
		if p.Key != "" {
			return "Its monitors are being checked from there again."
		}
		return "Checks are running again."
	case "test":
		return "Alerts sent to this channel will arrive like this one."
	}
	return ""
}

func buildEmail(p Payload) Email {
	tone, eyebrow, title, tag := alertCopy(p)
	brand := p.Brand
	if brand == "" {
		brand = "Argus"
	}
	subject := fmt.Sprintf("[%s] %s: %s", brand, tag, p.Monitor.Name)
	switch p.Type {
	case "probe_down":
		subject = fmt.Sprintf("[%s] PROBE DOWN: checks have stopped", brand)
		if p.Key != "" {
			subject = fmt.Sprintf("[%s] %s: %s", brand, tag, p.Monitor.Name)
		}
	case "probe_up":
		subject = fmt.Sprintf("[%s] PROBE RECOVERED", brand)
		if p.Key != "" {
			subject = fmt.Sprintf("[%s] %s: %s", brand, tag, p.Monitor.Name)
		}
	case "test":
		subject = fmt.Sprintf("[%s] Test email", brand)
	}

	var rows []EmailRow
	if !strings.HasPrefix(p.Type, "probe_") && p.Type != "test" {
		rows = append(rows, EmailRow{"Monitor", p.Monitor.Name})
		if p.Monitor.Target != "" {
			rows = append(rows, EmailRow{"Target", p.Monitor.Target})
		}
	}
	if p.ExtraLatency != nil && (p.Type == "slow" || p.Type == "recovery") {
		rows = append(rows, EmailRow{"Latency", fmt.Sprintf("%.1f ms", *p.ExtraLatency)})
	}
	rows = append(rows, EmailRow{"When", p.At.UTC().Format("Mon 2 Jan 2006, 15:04:05 UTC")})

	layout := EmailLayout{
		Brand: brand, Tone: tone, Eyebrow: eyebrow, Title: title,
		Lead: alertLead(p), Rows: rows, Note: p.Detail,
	}
	if p.Type == "test" {
		layout.Note = "" // the lead says it; chat channels use the detail
	}
	if p.BaseURL != "" && p.Monitor.ID > 0 {
		layout.Action = &EmailAction{Label: "Open in " + brand, URL: fmt.Sprintf("%s/monitors/%d", p.BaseURL, p.Monitor.ID)}
	}
	htmlBody, text := RenderEmail(layout)
	return Email{Subject: subject, HTML: htmlBody, Text: text}
}
