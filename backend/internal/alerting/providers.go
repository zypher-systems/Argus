package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"argus/internal/store"
)

// Endpoints are variables so tests can point them at a local server.
var (
	PagerDutyEventsURL   = "https://events.pagerduty.com/v2/enqueue"
	PagerDutyEventsEUURL = "https://events.eu.pagerduty.com/v2/enqueue"
	OpsgenieAlertsURL    = "https://api.opsgenie.com/v2/alerts"
	OpsgenieAlertsEUURL  = "https://api.eu.opsgenie.com/v2/alerts"
	TelegramAPIURL       = "https://api.telegram.org"
)

// euSuffix marks a key as belonging to a provider's EU region: "key@eu".
const euSuffix = "@eu"

func splitRegion(dest string) (key string, eu bool) {
	dest = strings.TrimSpace(dest)
	if strings.HasSuffix(strings.ToLower(dest), euSuffix) {
		return strings.TrimSpace(dest[:len(dest)-len(euSuffix)]), true
	}
	return dest, false
}

// resolving reports whether an event means "the problem is over".
func resolving(eventType string) bool {
	return eventType == "recovery" || eventType == "probe_up"
}

// alertKey is the stable id an incident-management provider uses to match a
// resolve to the alert it closes.
//
// Each kind of problem has its own key. PagerDuty and Opsgenie fold a new
// alert into an open one with the same key, so with one key per monitor an
// outage arriving while a certificate warning was still open paged nobody,
// and that warning was only ever closed by an unrelated recovery.
func alertKey(p Payload) string {
	if p.Type == "test" {
		return "argus-test"
	}
	if p.Key != "" {
		return p.Key
	}
	if p.Monitor.ID == 0 {
		return "argus-probe"
	}
	key := fmt.Sprintf("argus-monitor-%d", p.Monitor.ID)
	switch {
	case p.Type == "slow", p.Type == "recovery" && p.Of == "slow":
		return key + "-slow"
	case p.Type == "cert", p.Type == "domain":
		return key + "-" + p.Type
	}
	// down, escalation, resend, and the recovery that ends them.
	return key
}

func severity(eventType string) string {
	switch eventType {
	case "down", "probe_down":
		return "critical"
	case "escalation", "resend":
		return "error"
	case "slow", "cert", "domain":
		return "warning"
	default:
		return "info"
	}
}

// Deliver sends one payload to one channel. Email is handled by the mailer.
func Deliver(ctx context.Context, ch store.Channel, p Payload) error {
	switch ch.Type {
	case store.ChannelPagerDuty:
		return sendPagerDuty(ctx, ch.Dest, p)
	case store.ChannelOpsgenie:
		return sendOpsgenie(ctx, ch.Dest, p)
	case store.ChannelTelegram:
		return sendTelegram(ctx, ch.Dest, p)
	case store.ChannelNtfy:
		return sendNtfy(ctx, ch.Dest, p)
	case store.ChannelTeams:
		return PostJSON(ctx, ch.Dest, TeamsBody(p))
	default:
		return PostJSON(ctx, ch.Dest, HookBody(ch.Type, p))
	}
}

// --- PagerDuty (Events API v2) ---

// PagerDutyBody builds a trigger or resolve for the monitor's dedup key, so a
// recovery closes the incident the outage opened instead of opening another.
func PagerDutyBody(routingKey string, p Payload) []byte {
	action := "trigger"
	if resolving(p.Type) {
		action = "resolve"
	}
	body := map[string]any{
		"routing_key":  routingKey,
		"event_action": action,
		"dedup_key":    alertKey(p),
	}
	if action == "trigger" {
		payload := map[string]any{
			"summary":   eventTitle(p),
			"source":    p.Monitor.Target,
			"severity":  severity(p.Type),
			"timestamp": p.At.UTC().Format(time.RFC3339),
			"component": p.Monitor.Type,
			"custom_details": map[string]any{
				"monitor": p.Monitor.Name,
				"target":  p.Monitor.Target,
				"event":   p.Type,
				"detail":  p.Detail,
			},
		}
		if p.Monitor.ID == 0 {
			payload["source"] = "argus-probe"
		}
		body["payload"] = payload
	}
	raw, _ := json.Marshal(body)
	return raw
}

func sendPagerDuty(ctx context.Context, dest string, p Payload) error {
	key, eu := splitRegion(dest)
	endpoint := PagerDutyEventsURL
	if eu {
		endpoint = PagerDutyEventsEUURL
	}
	return postJSONWithHeaders(ctx, endpoint, PagerDutyBody(key, p), nil)
}

// --- Opsgenie ---

// OpsgenieBody builds the create-alert body. Resolves use the close endpoint,
// matched on the same alias.
func OpsgenieBody(p Payload) []byte {
	priority := "P3"
	switch severity(p.Type) {
	case "critical":
		priority = "P1"
	case "error":
		priority = "P2"
	}
	raw, _ := json.Marshal(map[string]any{
		"message":     eventTitle(p),
		"alias":       alertKey(p),
		"description": opsgenieDescription(p),
		"priority":    priority,
		"source":      "Argus",
		"details": map[string]string{
			"monitor": p.Monitor.Name,
			"target":  p.Monitor.Target,
			"event":   p.Type,
		},
	})
	return raw
}

func opsgenieDescription(p Payload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Monitor: %s\nTarget: %s\nEvent: %s\nTime: %s",
		p.Monitor.Name, p.Monitor.Target, p.Type, p.At.UTC().Format(time.RFC3339))
	if p.Detail != "" {
		fmt.Fprintf(&b, "\nDetail: %s", p.Detail)
	}
	return b.String()
}

func sendOpsgenie(ctx context.Context, dest string, p Payload) error {
	key, eu := splitRegion(dest)
	base := OpsgenieAlertsURL
	if eu {
		base = OpsgenieAlertsEUURL
	}
	headers := map[string]string{"Authorization": "GenieKey " + key}
	if resolving(p.Type) {
		closeURL := fmt.Sprintf("%s/%s/close?identifierType=alias", base, url.PathEscape(alertKey(p)))
		body, _ := json.Marshal(map[string]string{"source": "Argus", "note": eventTitle(p)})
		return postJSONWithHeaders(ctx, closeURL, body, headers)
	}
	return postJSONWithHeaders(ctx, base, OpsgenieBody(p), headers)
}

// --- Microsoft Teams ---

// TeamsBody is a MessageCard, which both Teams incoming webhooks and Power
// Automate "post to channel" flows accept.
func TeamsBody(p Payload) []byte {
	colour, _, _ := eventColor(p.Type)
	facts := []map[string]string{
		{"name": "Monitor", "value": p.Monitor.Name},
		{"name": "Target", "value": p.Monitor.Target},
		{"name": "Event", "value": p.Type},
		{"name": "Time", "value": p.At.UTC().Format("2006-01-02 15:04:05 UTC")},
	}
	if p.Detail != "" {
		facts = append(facts, map[string]string{"name": "Detail", "value": p.Detail})
	}
	raw, _ := json.Marshal(map[string]any{
		"@type":      "MessageCard",
		"@context":   "https://schema.org/extensions",
		"themeColor": strings.TrimPrefix(colour, "#"),
		"summary":    eventTitle(p),
		"title":      eventTitle(p),
		"sections":   []map[string]any{{"facts": facts, "markdown": false}},
	})
	return raw
}

// --- Telegram ---

// TelegramDest splits "<bot token>:<chat id>". Bot tokens contain a colon of
// their own, so the chat id is taken from the last one.
func TelegramDest(dest string) (token, chatID string, err error) {
	dest = strings.TrimSpace(dest)
	at := strings.LastIndex(dest, ":")
	if at <= 0 || at == len(dest)-1 {
		return "", "", fmt.Errorf("expected <bot token>:<chat id>")
	}
	token = strings.TrimSpace(dest[:at])
	chatID = strings.TrimSpace(dest[at+1:])
	if !strings.Contains(token, ":") {
		return "", "", fmt.Errorf("expected <bot token>:<chat id>")
	}
	if _, convErr := strconv.ParseInt(chatID, 10, 64); convErr != nil {
		return "", "", fmt.Errorf("chat id must be a number")
	}
	return token, chatID, nil
}

// TelegramText is plain text: Telegram's markdown parser rejects unescaped
// characters that show up in URLs and error strings.
func TelegramText(p Payload) string {
	var b strings.Builder
	b.WriteString(eventTitle(p))
	fmt.Fprintf(&b, "\nTarget: %s", p.Monitor.Target)
	if p.Detail != "" {
		fmt.Fprintf(&b, "\nDetail: %s", p.Detail)
	}
	fmt.Fprintf(&b, "\nTime: %s", p.At.UTC().Format("2006-01-02 15:04:05 UTC"))
	return b.String()
}

func sendTelegram(ctx context.Context, dest string, p Payload) error {
	token, chatID, err := TelegramDest(dest)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{
		"chat_id":                  chatID,
		"text":                     TelegramText(p),
		"disable_web_page_preview": true,
	})
	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", strings.TrimRight(TelegramAPIURL, "/"), token)
	return postJSONWithHeaders(ctx, endpoint, body, nil)
}

// --- ntfy ---

func ntfyPriority(eventType string) string {
	switch severity(eventType) {
	case "critical":
		return "urgent"
	case "error":
		return "high"
	case "warning":
		return "default"
	default:
		return "low"
	}
}

func ntfyTags(eventType string) string {
	switch eventType {
	case "recovery", "probe_up":
		return "white_check_mark"
	case "slow", "cert", "domain":
		return "warning"
	case "test":
		return "bell"
	default:
		return "rotating_light"
	}
}

// sendNtfy posts the message body to a topic URL, with the title, priority and
// tags as headers the way ntfy expects.
func sendNtfy(ctx context.Context, topicURL string, p Payload) error {
	text := p.Monitor.Name
	if p.Monitor.Target != "" {
		text += " — " + p.Monitor.Target
	}
	if p.Detail != "" {
		text += "\n" + p.Detail
	}
	if p.Monitor.ID == 0 {
		text = p.Detail
	}
	return postRaw(ctx, topicURL, []byte(text), map[string]string{
		"Content-Type": "text/plain; charset=utf-8",
		"Title":        eventTitle(p),
		"Priority":     ntfyPriority(p.Type),
		"Tags":         ntfyTags(p.Type),
	})
}

// --- transport ---

func postJSONWithHeaders(ctx context.Context, endpoint string, payload []byte, headers map[string]string) error {
	h := map[string]string{"Content-Type": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	return postRaw(ctx, endpoint, payload, h)
}

func postRaw(ctx context.Context, endpoint string, payload []byte, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Argus")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxHookBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Providers explain refusals in the body; keeping a little of it turns
		// "http 422" into something an operator can act on.
		msg := strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		if msg != "" {
			return fmt.Errorf("http %d: %s", resp.StatusCode, msg)
		}
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	return nil
}
