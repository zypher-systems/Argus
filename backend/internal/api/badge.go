package api

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"argus/internal/store"
)

// Badge colours, matched to the interface palette.
const (
	badgeGreen = "#146c43"
	badgeRed   = "#b42318"
	badgeAmber = "#9a4708"
	badgeGrey  = "#5a6472"
	badgeLabel = "#414a56"
)

// charWidth approximates 11px DejaVu Sans, which is what shields-style badges
// assume. Close enough that text never overflows its pill.
func badgeTextWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r == ' ':
			w += 4
		case r == '.' || r == ':' || r == 'i' || r == 'l' || r == 'j':
			w += 3
		case r >= '0' && r <= '9':
			w += 7
		case r >= 'A' && r <= 'Z':
			w += 8
		default:
			w += 7
		}
	}
	return w
}

// BadgeSVG renders a two-part badge: a grey label and a coloured value.
func BadgeSVG(label, value, colour string) []byte {
	lw := badgeTextWidth(label) + 12
	vw := badgeTextWidth(value) + 12
	total := lw + vw
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s: %s">
<title>%s: %s</title>
<linearGradient id="s" x2="0" y2="100%%"><stop offset="0" stop-color="#fff" stop-opacity=".7"/><stop offset=".1" stop-color="#aaa" stop-opacity=".1"/><stop offset=".9" stop-color="#000" stop-opacity=".3"/><stop offset="1" stop-color="#000" stop-opacity=".5"/></linearGradient>
<clipPath id="r"><rect width="%d" height="20" rx="3" fill="#fff"/></clipPath>
<g clip-path="url(#r)">
<rect width="%d" height="20" fill="%s"/>
<rect x="%d" width="%d" height="20" fill="%s"/>
<rect width="%d" height="20" fill="url(#s)"/>
</g>
<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">
<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text>
<text x="%d" y="14">%s</text>
<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text>
<text x="%d" y="14">%s</text>
</g>
</svg>`,
		total, xmlEscape(label), xmlEscape(value),
		xmlEscape(label), xmlEscape(value),
		total,
		lw, badgeLabel,
		lw, vw, colour,
		total,
		lw/2, xmlEscape(label),
		lw/2, xmlEscape(label),
		lw+vw/2, xmlEscape(value),
		lw+vw/2, xmlEscape(value),
	)
	return []byte(svg)
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// StatusBadge describes the whole page: operational, degraded, or outage.
func StatusBadge(overall string) (value, colour string) {
	switch overall {
	case "down":
		return "outage", badgeRed
	case "degraded":
		return "degraded", badgeAmber
	default:
		return "operational", badgeGreen
	}
}

// UptimeBadge averages the published monitors' 90-day uptime.
func UptimeBadge(monitors []store.PublicMonitor) (value, colour string) {
	sum, n := 0.0, 0
	for _, m := range monitors {
		if m.Uptime90d != nil {
			sum += *m.Uptime90d
			n++
		}
	}
	if n == 0 {
		return "collecting", badgeGrey
	}
	pct := sum / float64(n)
	switch {
	case pct >= 99.9:
		colour = badgeGreen
	case pct >= 99:
		colour = badgeAmber
	default:
		colour = badgeRed
	}
	return fmt.Sprintf("%.2f%%", pct), colour
}

// --- RSS ---

type rssFeed struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Atom    string     `xml:"xmlns:atom,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Updated     string    `xml:"lastBuildDate,omitempty"`
	AtomLink    atomLink  `xml:"atom:link"`
	Items       []rssItem `xml:"item"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type rssItem struct {
	Title       string  `xml:"title"`
	Link        string  `xml:"link"`
	GUID        rssGUID `xml:"guid"`
	PubDate     string  `xml:"pubDate"`
	Description string  `xml:"description"`
}

type rssGUID struct {
	Value       string `xml:",chardata"`
	IsPermaLink bool   `xml:"isPermaLink,attr"`
}

// IncidentFeed renders the incident history as RSS 2.0, so status watchers can
// subscribe without email.
func IncidentFeed(page store.PublicStatus, pageURL string) ([]byte, error) {
	feed := rssFeed{
		Version: "2.0",
		Atom:    "http://www.w3.org/2005/Atom",
		Channel: rssChannel{
			Title:       page.Name + " status",
			Link:        pageURL,
			Description: firstNonEmptyString(page.Headline, "Incident history for "+page.Name),
			AtomLink:    atomLink{Href: strings.TrimRight(pageURL, "/") + "/feed.xml", Rel: "self", Type: "application/rss+xml"},
		},
	}
	newest := time.Time{}
	for _, inc := range page.Incidents {
		when := inc.OpenedAt
		var body strings.Builder
		fmt.Fprintf(&body, "%s — %s", strings.ToUpper(inc.Status), inc.Title)
		if inc.Monitor != "" {
			fmt.Fprintf(&body, " (%s)", inc.Monitor)
		}
		for _, u := range inc.Updates {
			fmt.Fprintf(&body, "\n\n%s — %s: %s", u.CreatedAt.UTC().Format(time.RFC1123Z), u.Status, u.Message)
			if u.CreatedAt.After(when) {
				when = u.CreatedAt
			}
		}
		if inc.ResolvedAt != nil {
			fmt.Fprintf(&body, "\n\nResolved %s", inc.ResolvedAt.UTC().Format(time.RFC1123Z))
		}
		if when.After(newest) {
			newest = when
		}
		feed.Channel.Items = append(feed.Channel.Items, rssItem{
			Title:       inc.Title,
			Link:        pageURL,
			GUID:        rssGUID{Value: fmt.Sprintf("%s#incident-%d", pageURL, inc.ID), IsPermaLink: false},
			PubDate:     inc.OpenedAt.UTC().Format(time.RFC1123Z),
			Description: body.String(),
		})
	}
	if !newest.IsZero() {
		feed.Channel.Updated = newest.UTC().Format(time.RFC1123Z)
	}
	out, err := xml.MarshalIndent(feed, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}

func firstNonEmptyString(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
