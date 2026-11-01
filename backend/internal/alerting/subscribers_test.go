package alerting

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"argus/internal/store"
)

// A private incident is for the people running the service. Its title and
// status must not reach the page's subscribers either.
func TestIncidentReachesSubscribersOnlyWhenPublic(t *testing.T) {
	f := newQueueFixture(t)
	ctx := context.Background()
	if _, err := f.st.Pool.Exec(ctx, `TRUNCATE status_pages CASCADE`); err != nil {
		t.Fatal(err)
	}
	var delivered atomic.Int32
	hook := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		delivered.Add(1)
	}))
	defer hook.Close()

	page := store.StatusPage{Name: "Acme", Slug: "acme", Published: true, MonitorIDs: []int64{f.m.ID}}
	if err := f.st.CreateStatusPage(ctx, &page); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateSubscriber(ctx, page.ID, "webhook", hook.URL, true); err != nil {
		t.Fatal(err)
	}

	inc := store.Incident{ID: 1, MonitorID: &f.m.ID, Title: "Database failover", Status: "investigating", Impact: "major"}
	f.d.notifyIncident(ctx, inc)
	if n := delivered.Load(); n != 0 {
		t.Fatalf("private incident delivered to %d subscribers", n)
	}

	inc.Public = true
	f.d.notifyIncident(ctx, inc)
	if n := delivered.Load(); n != 1 {
		t.Fatalf("public incident delivered %d times, want 1", n)
	}
}
