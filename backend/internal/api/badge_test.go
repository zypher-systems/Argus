package api

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"argus/internal/store"
)

func pct(v float64) *float64 { return &v }

func TestBadgeSVGIsValidAndFits(t *testing.T) {
	svg := BadgeSVG("uptime", "99.98%", badgeGreen)
	var doc struct {
		XMLName xml.Name `xml:"svg"`
		Width   int      `xml:"width,attr"`
	}
	if err := xml.Unmarshal(svg, &doc); err != nil {
		t.Fatalf("badge is not valid XML: %v", err)
	}
	if doc.Width < 80 || doc.Width > 220 {
		t.Fatalf("width %d looks wrong for this text", doc.Width)
	}
	if !strings.Contains(string(svg), "99.98%") || !strings.Contains(string(svg), badgeGreen) {
		t.Fatal("value or colour missing")
	}
}

func TestBadgeEscapesText(t *testing.T) {
	svg := string(BadgeSVG(`a<b&"c`, "ok", badgeGreen))
	if strings.Contains(svg, "a<b&\"c") {
		t.Fatal("label must be escaped into the SVG")
	}
	var doc any
	if err := xml.Unmarshal([]byte(svg), &doc); err != nil {
		t.Fatalf("escaping produced invalid XML: %v", err)
	}
}

func TestStatusAndUptimeBadgeValues(t *testing.T) {
	for overall, want := range map[string]string{"operational": "operational", "degraded": "degraded", "down": "outage"} {
		if v, _ := StatusBadge(overall); v != want {
			t.Errorf("StatusBadge(%q) = %q", overall, v)
		}
	}
	cases := []struct {
		name     string
		monitors []store.PublicMonitor
		want     string
		colour   string
	}{
		{"no data", []store.PublicMonitor{{}}, "collecting", badgeGrey},
		{"green", []store.PublicMonitor{{Uptime90d: pct(100)}, {Uptime90d: pct(99.99)}}, "100.00%", badgeGreen},
		{"amber", []store.PublicMonitor{{Uptime90d: pct(99.5)}}, "99.50%", badgeAmber},
		{"red", []store.PublicMonitor{{Uptime90d: pct(95)}}, "95.00%", badgeRed},
	}
	for _, c := range cases {
		v, colour := UptimeBadge(c.monitors)
		if v != c.want || colour != c.colour {
			t.Errorf("%s: got %q/%s want %q/%s", c.name, v, colour, c.want, c.colour)
		}
	}
}

func TestIncidentFeedIsValidRSS(t *testing.T) {
	resolved := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	page := store.PublicStatus{
		Name:     "Acme",
		Slug:     "acme",
		Headline: "Service status",
		Incidents: []store.PublicIncident{
			{
				ID: 3, Title: "Checkout API is down", Status: "resolved", Monitor: "Checkout API",
				OpenedAt: time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC), ResolvedAt: &resolved,
				Updates: []store.PublicIncidentUpdate{
					{Status: "investigating", Message: "Looking into errors at the edge", CreatedAt: time.Date(2026, 9, 20, 10, 5, 0, 0, time.UTC)},
					{Status: "resolved", Message: "Upstream fixed", CreatedAt: resolved},
				},
			},
			{ID: 4, Title: "Latency in EU", Status: "open", OpenedAt: time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)},
		},
	}
	raw, err := IncidentFeed(page, "https://status.acme.test/status/acme")
	if err != nil {
		t.Fatal(err)
	}
	var feed struct {
		XMLName xml.Name `xml:"rss"`
		Channel struct {
			Title string `xml:"title"`
			Link  string `xml:"link"`
			Items []struct {
				Title       string `xml:"title"`
				GUID        string `xml:"guid"`
				PubDate     string `xml:"pubDate"`
				Description string `xml:"description"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(raw, &feed); err != nil {
		t.Fatalf("feed is not valid XML: %v\n%s", err, raw)
	}
	if feed.Channel.Title != "Acme status" || len(feed.Channel.Items) != 2 {
		t.Fatalf("channel %+v", feed.Channel)
	}
	first := feed.Channel.Items[0]
	if first.Title != "Checkout API is down" || !strings.Contains(first.GUID, "incident-3") {
		t.Fatalf("item %+v", first)
	}
	if _, err := time.Parse(time.RFC1123Z, first.PubDate); err != nil {
		t.Fatalf("pubDate %q is not RFC1123Z: %v", first.PubDate, err)
	}
	if !strings.Contains(first.Description, "Upstream fixed") || !strings.Contains(first.Description, "Resolved") {
		t.Fatalf("description missing updates: %q", first.Description)
	}
}

func TestIncidentFeedWithNoIncidents(t *testing.T) {
	raw, err := IncidentFeed(store.PublicStatus{Name: "Quiet", Slug: "quiet"}, "https://x.test/status/quiet")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := xml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("empty feed must still be valid: %v", err)
	}
	if !strings.Contains(string(raw), "Incident history for Quiet") {
		t.Fatal("expected a description fallback")
	}
}
