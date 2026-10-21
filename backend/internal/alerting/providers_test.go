package alerting

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

type captured struct {
	method  string
	path    string
	query   string
	headers http.Header
	body    string
}

// recorder stands in for a provider's API.
func recorder(t *testing.T, status int, reply string) (*httptest.Server, *[]captured) {
	t.Helper()
	var got []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = append(got, captured{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), string(raw)})
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func providerPayload(kind string) Payload {
	return Payload{
		Monitor: store.Monitor{ID: 7, Name: "Checkout API", Type: "http", Target: "https://acme.test/api"},
		Type:    kind,
		At:      time.Date(2026, 9, 22, 15, 4, 5, 0, time.UTC),
		Detail:  "connection refused",
	}
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, body)
	}
	return m
}

func TestPagerDutyTriggerAndResolve(t *testing.T) {
	srv, got := recorder(t, 202, `{"status":"success"}`)
	prev := PagerDutyEventsURL
	PagerDutyEventsURL = srv.URL
	t.Cleanup(func() { PagerDutyEventsURL = prev })

	ch := store.Channel{Type: store.ChannelPagerDuty, Dest: "routing-key-123"}
	if err := Deliver(context.Background(), ch, providerPayload("down")); err != nil {
		t.Fatal(err)
	}
	if err := Deliver(context.Background(), ch, providerPayload("recovery")); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("calls %d", len(*got))
	}
	trigger := decode(t, (*got)[0].body)
	if trigger["routing_key"] != "routing-key-123" || trigger["event_action"] != "trigger" {
		t.Fatalf("trigger %v", trigger)
	}
	payload, _ := trigger["payload"].(map[string]any)
	if payload["severity"] != "critical" || !strings.Contains(payload["summary"].(string), "Checkout API") {
		t.Fatalf("payload %v", payload)
	}
	resolve := decode(t, (*got)[1].body)
	if resolve["event_action"] != "resolve" {
		t.Fatalf("resolve %v", resolve)
	}
	// The resolve has to carry the same dedup key or PagerDuty opens a second
	// incident instead of closing the first.
	if resolve["dedup_key"] != trigger["dedup_key"] || resolve["dedup_key"] != "argus-monitor-7" {
		t.Fatalf("dedup keys %v vs %v", trigger["dedup_key"], resolve["dedup_key"])
	}
	if _, ok := resolve["payload"]; ok {
		t.Fatal("a resolve carries no payload")
	}
}

func TestPagerDutyEURegion(t *testing.T) {
	srv, got := recorder(t, 202, "{}")
	prev := PagerDutyEventsEUURL
	PagerDutyEventsEUURL = srv.URL
	t.Cleanup(func() { PagerDutyEventsEUURL = prev })

	ch := store.Channel{Type: store.ChannelPagerDuty, Dest: "eu-key@eu"}
	if err := Deliver(context.Background(), ch, providerPayload("down")); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 1 || decode(t, (*got)[0].body)["routing_key"] != "eu-key" {
		t.Fatalf("eu call %+v", *got)
	}
}

func TestOpsgenieCreateAndClose(t *testing.T) {
	srv, got := recorder(t, 202, `{"result":"ok"}`)
	prev := OpsgenieAlertsURL
	OpsgenieAlertsURL = srv.URL + "/v2/alerts"
	t.Cleanup(func() { OpsgenieAlertsURL = prev })

	ch := store.Channel{Type: store.ChannelOpsgenie, Dest: "genie-key"}
	if err := Deliver(context.Background(), ch, providerPayload("down")); err != nil {
		t.Fatal(err)
	}
	if err := Deliver(context.Background(), ch, providerPayload("recovery")); err != nil {
		t.Fatal(err)
	}
	create, closed := (*got)[0], (*got)[1]
	if create.headers.Get("Authorization") != "GenieKey genie-key" {
		t.Fatalf("auth %q", create.headers.Get("Authorization"))
	}
	body := decode(t, create.body)
	if body["alias"] != "argus-monitor-7" || body["priority"] != "P1" {
		t.Fatalf("create %v", body)
	}
	if !strings.HasSuffix(closed.path, "/argus-monitor-7/close") || closed.query != "identifierType=alias" {
		t.Fatalf("close call %s?%s", closed.path, closed.query)
	}
}

func TestTeamsCard(t *testing.T) {
	srv, got := recorder(t, 200, "1")
	ch := store.Channel{Type: store.ChannelTeams, Dest: srv.URL}
	if err := Deliver(context.Background(), ch, providerPayload("down")); err != nil {
		t.Fatal(err)
	}
	card := decode(t, (*got)[0].body)
	if card["@type"] != "MessageCard" || card["themeColor"] == "" {
		t.Fatalf("card %v", card)
	}
	sections, _ := card["sections"].([]any)
	facts, _ := sections[0].(map[string]any)["facts"].([]any)
	if len(facts) != 5 {
		t.Fatalf("facts %v", facts)
	}
}

func TestTelegramDest(t *testing.T) {
	token, chat, err := TelegramDest(" 123456:ABC-DEF_ghi :-1001234567890 ")
	if err != nil || token != "123456:ABC-DEF_ghi" || chat != "-1001234567890" {
		t.Fatalf("got %q %q %v", token, chat, err)
	}
	for _, bad := range []string{"", "nocolon", "123456:ABC", "token:chat:", ":123"} {
		if _, _, err := TelegramDest(bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
}

func TestTelegramSend(t *testing.T) {
	srv, got := recorder(t, 200, `{"ok":true}`)
	prev := TelegramAPIURL
	TelegramAPIURL = srv.URL
	t.Cleanup(func() { TelegramAPIURL = prev })

	ch := store.Channel{Type: store.ChannelTelegram, Dest: "123456:ABC-DEF:987654"}
	if err := Deliver(context.Background(), ch, providerPayload("down")); err != nil {
		t.Fatal(err)
	}
	call := (*got)[0]
	if call.path != "/bot123456:ABC-DEF/sendMessage" {
		t.Fatalf("path %s", call.path)
	}
	body := decode(t, call.body)
	if body["chat_id"] != "987654" || !strings.Contains(body["text"].(string), "acme.test") {
		t.Fatalf("body %v", body)
	}
}

func TestNtfyHeaders(t *testing.T) {
	srv, got := recorder(t, 200, "{}")
	ch := store.Channel{Type: store.ChannelNtfy, Dest: srv.URL + "/argus-alerts"}
	if err := Deliver(context.Background(), ch, providerPayload("down")); err != nil {
		t.Fatal(err)
	}
	call := (*got)[0]
	if call.path != "/argus-alerts" || call.headers.Get("Priority") != "urgent" {
		t.Fatalf("call %s priority %q", call.path, call.headers.Get("Priority"))
	}
	if !strings.Contains(call.headers.Get("Title"), "DOWN") || !strings.Contains(call.body, "Checkout API") {
		t.Fatalf("title %q body %q", call.headers.Get("Title"), call.body)
	}
	if err := Deliver(context.Background(), store.Channel{Type: store.ChannelNtfy, Dest: srv.URL + "/t"}, providerPayload("recovery")); err != nil {
		t.Fatal(err)
	}
	if p := (*got)[1].headers.Get("Priority"); p != "low" {
		t.Fatalf("recovery priority %q", p)
	}
}

func TestProviderErrorsCarryTheBody(t *testing.T) {
	srv, _ := recorder(t, 422, `{"errors":{"routing_key":"not found"}}`)
	prev := PagerDutyEventsURL
	PagerDutyEventsURL = srv.URL
	t.Cleanup(func() { PagerDutyEventsURL = prev })

	err := Deliver(context.Background(), store.Channel{Type: store.ChannelPagerDuty, Dest: "bad"}, providerPayload("down"))
	if err == nil || !strings.Contains(err.Error(), "routing_key") {
		t.Fatalf("an operator needs the provider's reason, got %v", err)
	}
}

func TestProbeEventsUseAStableKey(t *testing.T) {
	p := Payload{Monitor: store.Monitor{}, Type: "probe_down", At: time.Now(), Detail: "probe stopped"}
	if alertKey(p) != "argus-probe" {
		t.Fatalf("probe key %q", alertKey(p))
	}
	if severity("probe_down") != "critical" || !resolving("probe_up") {
		t.Fatal("probe events must page and resolve")
	}
}
