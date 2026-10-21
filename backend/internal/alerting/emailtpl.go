package alerting

import (
	"fmt"
	"html"
	"strings"
)

// EmailLayout is the one look every Argus email shares: a white card on a
// light page, a bar in the colour of what happened, the facts as rows, and a
// way back into Argus. Colours are the app's own and pass 4.5:1 on white.
type EmailLayout struct {
	Brand   string // top of the card: "Argus", or the product name from branding
	Tone    string // down, up, warn, or info
	Eyebrow string // a few words above the title: "Monitor down"
	Title   string
	Lead    string // one paragraph; also the inbox preview
	Rows    []EmailRow
	Note    string // error text and the like, shown monospaced
	// RawHTML and RawText are extra content, such as a report table; the
	// HTML must already be escaped.
	RawHTML string
	RawText string
	Action  *EmailAction
	Footer  string
	// FooterLink sits after the footer text, such as an unsubscribe link.
	FooterLink *EmailAction
}

type EmailRow struct{ Label, Value string }

type EmailAction struct{ Label, URL string }

var emailTones = map[string]string{
	"down": "#b42318",
	"up":   "#146c43",
	"warn": "#9a4708",
	"info": "#1f5bd8",
}

const (
	emailInk    = "#14171c"
	emailMuted  = "#5a6472"
	emailLine   = "#dde2ea"
	emailPage   = "#f6f7f9"
	emailAccent = "#1f5bd8"
	emailFont   = "-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif"
	emailMono   = "ui-monospace,SFMono-Regular,Menlo,Consolas,monospace"
)

// RenderEmail returns the HTML and plain-text versions of one message.
func RenderEmail(l EmailLayout) (string, string) {
	tone, ok := emailTones[l.Tone]
	if !ok {
		tone = emailTones["info"]
	}
	brand := l.Brand
	if brand == "" {
		brand = "Argus"
	}
	esc := html.EscapeString

	var h strings.Builder
	h.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8">`)
	h.WriteString(`<meta name="viewport" content="width=device-width,initial-scale=1">`)
	h.WriteString(`<meta name="color-scheme" content="light"><meta name="supported-color-schemes" content="light">`)
	fmt.Fprintf(&h, `<title>%s</title></head>`, esc(l.Title))
	fmt.Fprintf(&h, `<body style="margin:0;padding:0;background:%s;font-family:%s;color:%s">`, emailPage, emailFont, emailInk)
	// Preheader: what an inbox shows after the subject.
	fmt.Fprintf(&h, `<div style="display:none;max-height:0;overflow:hidden;opacity:0">%s</div>`, esc(l.Lead))
	h.WriteString(`<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:` + emailPage + `"><tr><td align="center" style="padding:32px 16px">`)
	fmt.Fprintf(&h, `<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="max-width:560px;width:100%%;background:#ffffff;border:1px solid %s;border-top:4px solid %s;border-radius:10px">`, emailLine, tone)

	fmt.Fprintf(&h, `<tr><td style="padding:20px 28px 0;font-size:13px;font-weight:600;color:%s">%s</td></tr>`, emailMuted, esc(brand))
	fmt.Fprintf(&h, `<tr><td style="padding:18px 28px 0">`)
	if l.Eyebrow != "" {
		fmt.Fprintf(&h, `<div style="font-size:12px;font-weight:700;letter-spacing:0.06em;text-transform:uppercase;color:%s">%s</div>`, tone, esc(l.Eyebrow))
	}
	fmt.Fprintf(&h, `<h1 style="margin:6px 0 0;font-size:22px;line-height:1.3;font-weight:700;color:%s">%s</h1>`, emailInk, esc(l.Title))
	if l.Lead != "" {
		fmt.Fprintf(&h, `<p style="margin:10px 0 0;font-size:15px;line-height:1.55;color:%s">%s</p>`, emailInk, esc(l.Lead))
	}
	h.WriteString(`</td></tr>`)

	if len(l.Rows) > 0 {
		fmt.Fprintf(&h, `<tr><td style="padding:20px 28px 0"><table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="border-top:1px solid %s;font-size:14px">`, emailLine)
		for _, r := range l.Rows {
			fmt.Fprintf(&h, `<tr><td style="padding:10px 12px 10px 0;border-bottom:1px solid %s;color:%s;width:120px;vertical-align:top">%s</td>`+
				`<td style="padding:10px 0;border-bottom:1px solid %s;color:%s;word-break:break-word">%s</td></tr>`,
				emailLine, emailMuted, esc(r.Label), emailLine, emailInk, esc(r.Value))
		}
		h.WriteString(`</table></td></tr>`)
	}
	if l.Note != "" {
		fmt.Fprintf(&h, `<tr><td style="padding:16px 28px 0"><div style="background:%s;border:1px solid %s;border-radius:6px;padding:12px 14px;font-family:%s;font-size:13px;line-height:1.5;color:%s;white-space:pre-wrap;word-break:break-word">%s</div></td></tr>`,
			emailPage, emailLine, emailMono, emailInk, esc(l.Note))
	}
	if l.RawHTML != "" {
		fmt.Fprintf(&h, `<tr><td style="padding:16px 28px 0">%s</td></tr>`, l.RawHTML)
	}
	if l.Action != nil && l.Action.URL != "" {
		fmt.Fprintf(&h, `<tr><td style="padding:24px 28px 0"><a href="%s" style="display:inline-block;background:%s;color:#ffffff;text-decoration:none;font-size:14px;font-weight:600;padding:10px 18px;border-radius:6px">%s</a></td></tr>`,
			esc(l.Action.URL), emailAccent, esc(l.Action.Label))
	}
	footer := l.Footer
	if footer == "" {
		footer = "Sent by " + brand + "."
	}
	footerLink := ""
	if l.FooterLink != nil && l.FooterLink.URL != "" {
		footerLink = fmt.Sprintf(` <a href="%s" style="color:%s;text-decoration:underline">%s</a>`, esc(l.FooterLink.URL), emailMuted, esc(l.FooterLink.Label))
	}
	fmt.Fprintf(&h, `<tr><td style="padding:24px 28px 24px;font-size:12px;line-height:1.5;color:%s">%s%s</td></tr>`, emailMuted, esc(footer), footerLink)
	h.WriteString(`</table></td></tr></table></body></html>`)

	var t strings.Builder
	t.WriteString(brand + "\n\n")
	if l.Eyebrow != "" {
		t.WriteString(strings.ToUpper(l.Eyebrow) + "\n")
	}
	t.WriteString(l.Title + "\n")
	if l.Lead != "" {
		t.WriteString("\n" + l.Lead + "\n")
	}
	if len(l.Rows) > 0 {
		t.WriteString("\n")
		for _, r := range l.Rows {
			fmt.Fprintf(&t, "%s: %s\n", r.Label, r.Value)
		}
	}
	if l.Note != "" {
		t.WriteString("\n" + l.Note + "\n")
	}
	if l.RawText != "" {
		t.WriteString("\n" + l.RawText + "\n")
	}
	if l.Action != nil && l.Action.URL != "" {
		fmt.Fprintf(&t, "\n%s: %s\n", l.Action.Label, l.Action.URL)
	}
	t.WriteString("\n" + footer + "\n")
	if l.FooterLink != nil && l.FooterLink.URL != "" {
		fmt.Fprintf(&t, "%s: %s\n", l.FooterLink.Label, l.FooterLink.URL)
	}
	return h.String(), t.String()
}
