package api

import (
	"strings"
	"testing"

	"argus/internal/store"
)

func TestChannelRequestValidate(t *testing.T) {
	ok := channelRequest{
		Name:   "ops",
		Type:   "slack",
		Dest:   "https://hooks.slack.com/services/T/B/x",
		Events: []string{"down", "recovery"},
	}
	c, msg := ok.validate()
	if msg != "" {
		t.Fatalf("valid rejected: %s", msg)
	}
	if c.Type != store.ChannelSlack || len(c.Events) != 2 {
		t.Fatalf("got %#v", c)
	}

	def := ok
	def.Events = nil
	c, msg = def.validate()
	if msg != "" {
		t.Fatalf("nil events rejected: %s", msg)
	}
	if len(c.Events) != len(store.DefaultChannelEvents) {
		t.Fatalf("default events %v", c.Events)
	}

	muted := ok
	muted.Events = []string{}
	c, msg = muted.validate()
	if msg != "" || len(c.Events) != 0 {
		t.Fatalf("empty events: %s %#v", msg, c)
	}

	if _, msg := (channelRequest{Type: "slack", Dest: ok.Dest}).validate(); msg == "" {
		t.Fatal("expected name required")
	}
	if _, msg := (channelRequest{Name: "x", Type: "pager", Dest: ok.Dest}).validate(); msg == "" {
		t.Fatal("expected bad type")
	}
	if _, msg := (channelRequest{Name: "x", Type: "webhook", Dest: "not-a-url"}).validate(); msg == "" {
		t.Fatal("expected bad dest")
	}
	if _, msg := (channelRequest{Name: "x", Type: "webhook", Dest: ""}).validate(); msg == "" {
		t.Fatal("expected empty dest")
	}
	if _, msg := (channelRequest{Name: "x", Type: "webhook", Dest: ok.Dest, Events: []string{"down", "nope"}}).validate(); msg == "" {
		t.Fatal("expected bad event")
	}

	mail, msg := (channelRequest{Name: "oncall", Type: "email", Dest: "ops@example.com, you@example.com"}).validate()
	if msg != "" || mail.Type != store.ChannelEmail || !strings.Contains(mail.Dest, "ops@example.com") {
		t.Fatalf("email dest: %s %#v", msg, mail)
	}
	if _, msg := (channelRequest{Name: "oncall", Type: "email", Dest: "https://example.com/x"}).validate(); msg == "" {
		t.Fatal("expected email dest rejected")
	}
	alias := channelRequest{Name: "hook", Type: "webhook", URL: ok.Dest}
	if c, msg := alias.validate(); msg != "" || c.Dest != ok.Dest {
		t.Fatalf("url alias: %s %#v", msg, c)
	}
	long := ok
	long.Name = strings.Repeat("n", 81)
	if _, msg := long.validate(); msg == "" {
		t.Fatal("expected long name")
	}
}
