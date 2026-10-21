package api

import "testing"

func TestStatusPageRequestValidate(t *testing.T) {
	pub := true
	ok := statusPageRequest{Name: "API", Slug: "api-status", Headline: "All systems", Published: &pub}
	p, msg := ok.validate()
	if msg != "" {
		t.Fatalf("valid rejected: %s", msg)
	}
	if p.Slug != "api-status" || !p.Published {
		t.Fatalf("got %#v", p)
	}

	if _, msg := (statusPageRequest{Name: "x", Slug: "Has Space"}).validate(); msg == "" {
		t.Fatal("expected invalid slug rejected")
	}
	if _, msg := (statusPageRequest{Name: "x", Slug: "login"}).validate(); msg == "" {
		t.Fatal("expected reserved slug")
	}
	if _, msg := (statusPageRequest{Slug: "ok"}).validate(); msg == "" {
		t.Fatal("expected name required")
	}
	if _, msg := (statusPageRequest{Name: "x", Slug: "-bad"}).validate(); msg == "" {
		t.Fatal("expected leading hyphen rejected")
	}
	ids := []int64{1, 2}
	withIDs := statusPageRequest{Name: "x", Slug: "ok", MonitorIDs: &ids}
	p, msg = withIDs.validate()
	if msg != "" || len(p.MonitorIDs) != 2 {
		t.Fatalf("ids: %s %#v", msg, p)
	}

	dom := statusPageRequest{Name: "x", Slug: "ok", CustomDomain: "status.example.com"}
	p, msg = dom.validate()
	if msg != "" || p.CustomDomain != "status.example.com" {
		t.Fatalf("domain: %s %#v", msg, p)
	}
	if _, msg := (statusPageRequest{Name: "x", Slug: "ok", CustomDomain: "https://nope"}).validate(); msg == "" {
		t.Fatal("scheme should be rejected")
	}
}
