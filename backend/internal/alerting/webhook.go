package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxHookBody = 1 << 20

func ValidHookURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("url must be http or https")
	}
	return raw, nil
}

func eventColor(kind string) (hex string, discord int, slack string) {
	switch kind {
	case "recovery":
		return "#22c55e", 0x22c55e, "good"
	case "cert", "domain":
		return "#f59e0b", 0xf59e0b, "warning"
	case "slow":
		return "#f59e0b", 0xf59e0b, "warning"
	case "escalation":
		return "#f97316", 0xf97316, "warning"
	case "resend":
		return "#f97316", 0xf97316, "warning"
	case "test":
		return "#1f5bd8", 0x1f5bd8, "#1f5bd8"
	default:
		return "#ef4444", 0xef4444, "danger"
	}
}

func eventTitle(p Payload) string {
	name := p.Monitor.Name
	switch p.Type {
	case "down":
		return fmt.Sprintf("[Argus] DOWN: %s", name)
	case "recovery":
		return fmt.Sprintf("[Argus] RECOVERED: %s", name)
	case "escalation":
		return fmt.Sprintf("[Argus] STILL DOWN: %s", name)
	case "resend":
		return fmt.Sprintf("[Argus] STILL DOWN: %s", name)
	case "cert":
		return fmt.Sprintf("[Argus] CERT EXPIRING: %s", name)
	case "domain":
		return fmt.Sprintf("[Argus] DOMAIN EXPIRING: %s", name)
	case "slow":
		return fmt.Sprintf("[Argus] SLOW: %s", name)
	case "test":
		return "[Argus] Test notification"
	default:
		return fmt.Sprintf("[Argus] %s: %s", strings.ToUpper(p.Type), name)
	}
}

func WebhookBody(p Payload) []byte {
	msg := eventTitle(p)
	body, _ := json.Marshal(map[string]any{
		"event":   p.Type,
		"message": msg,
		"at":      p.At.UTC().Format(time.RFC3339),
		"detail":  p.Detail,
		"monitor": map[string]any{
			"id":     p.Monitor.ID,
			"name":   p.Monitor.Name,
			"type":   p.Monitor.Type,
			"target": p.Monitor.Target,
		},
	})
	return body
}

func SlackBody(p Payload) []byte {
	_, _, color := eventColor(p.Type)
	fields := []map[string]any{
		{"title": "Monitor", "value": p.Monitor.Name, "short": true},
		{"title": "Target", "value": p.Monitor.Target, "short": true},
	}
	if p.Detail != "" {
		fields = append(fields, map[string]any{"title": "Detail", "value": p.Detail, "short": false})
	}
	body, _ := json.Marshal(map[string]any{
		"text": eventTitle(p),
		"attachments": []map[string]any{
			{
				"color":  color,
				"ts":     p.At.Unix(),
				"fields": fields,
			},
		},
	})
	return body
}

func DiscordBody(p Payload) []byte {
	_, color, _ := eventColor(p.Type)
	fields := []map[string]any{
		{"name": "Monitor", "value": p.Monitor.Name, "inline": true},
		{"name": "Target", "value": p.Monitor.Target, "inline": true},
	}
	if p.Detail != "" {
		fields = append(fields, map[string]any{"name": "Detail", "value": p.Detail, "inline": false})
	}
	body, _ := json.Marshal(map[string]any{
		"username": "Argus",
		"embeds": []map[string]any{
			{
				"title":     eventTitle(p),
				"color":     color,
				"timestamp": p.At.UTC().Format(time.RFC3339),
				"fields":    fields,
			},
		},
	})
	return body
}

func PostJSON(ctx context.Context, hookURL string, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hookURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Argus/0.6")

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
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxHookBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	return nil
}
