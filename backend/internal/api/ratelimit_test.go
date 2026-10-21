package api

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	l := newRateLimiter(time.Minute, 2)
	if !l.allow("a") {
		t.Fatal("first should pass")
	}
	if !l.allow("a") {
		t.Fatal("second should pass")
	}
	if l.allow("a") {
		t.Fatal("third should block")
	}
	if !l.allow("b") {
		t.Fatal("other key should pass")
	}
	l.reset("a")
	if !l.allow("a") {
		t.Fatal("reset should pass")
	}
}

func TestClientIPFrom(t *testing.T) {
	req := func(remote string, xff ...string) *http.Request {
		r, _ := http.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		for _, v := range xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		return r
	}
	cases := []struct {
		name    string
		r       *http.Request
		proxies int
		want    string
	}{
		{"direct, no header", req("10.1.2.3:4444"), 1, "10.1.2.3"},
		{"bundled nginx", req("172.18.0.5:5555", "203.0.113.7"), 1, "203.0.113.7"},
		// The caller wrote 9.9.9.9 themselves; nginx appended who it really was.
		{"forged left entry ignored", req("172.18.0.5:5555", "9.9.9.9, 203.0.113.7"), 1, "203.0.113.7"},
		{"forged entries across headers", req("172.18.0.5:5555", "9.9.9.9", "8.8.8.8, 203.0.113.7"), 1, "203.0.113.7"},
		{"outer proxy then nginx", req("172.18.0.5:5555", "9.9.9.9, 203.0.113.7, 10.0.0.2"), 2, "203.0.113.7"},
		{"no proxies trusts nothing", req("10.1.2.3:4444", "203.0.113.7"), 0, "10.1.2.3"},
		{"port on a hop", req("172.18.0.5:5555", "203.0.113.7:61000"), 1, "203.0.113.7"},
		{"ipv6 hop with port", req("172.18.0.5:5555", "[2001:db8::1]:61000"), 1, "2001:db8::1"},
		{"mapped ipv4 normalised", req("[::ffff:10.1.2.3]:4444"), 1, "10.1.2.3"},
		{"garbage falls back to peer", req("172.18.0.5:5555", "not-an-ip"), 1, "172.18.0.5"},
	}
	for _, c := range cases {
		if got := clientIPFrom(c.r, c.proxies); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	// X-Real-IP is set by a caller as easily as anything else.
	r := req("10.1.2.3:4444")
	r.Header.Set("X-Real-IP", "9.9.9.9")
	if got := clientIPFrom(r, 1); got != "10.1.2.3" {
		t.Fatalf("X-Real-IP must not be trusted, got %q", got)
	}
}

func TestLimitKeyGroupsIPv6Subnets(t *testing.T) {
	if limitKey("2001:db8:1:2::1") != limitKey("2001:db8:1:2:ffff::9") {
		t.Fatal("addresses in one /64 should share a limit")
	}
	if limitKey("2001:db8:1:2::1") == limitKey("2001:db8:1:3::1") {
		t.Fatal("different /64s should not share a limit")
	}
	if limitKey("203.0.113.7") != "203.0.113.7" {
		t.Fatal("ipv4 is counted per address")
	}
}

func TestRateLimiterForgetsIdleKeys(t *testing.T) {
	l := newRateLimiter(20*time.Millisecond, 5)
	for i := 0; i < 100; i++ {
		l.allow("10.0.0." + strconv.Itoa(i))
	}
	time.Sleep(30 * time.Millisecond)
	l.allow("10.9.9.9")
	l.mu.Lock()
	n := len(l.attempts)
	l.mu.Unlock()
	if n != 1 {
		t.Fatalf("idle keys should be swept, %d remain", n)
	}
}
