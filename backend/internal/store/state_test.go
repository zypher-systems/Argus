package store

import "testing"

func f64(v float64) *float64 { return &v }

func TestNextSlow(t *testing.T) {
	cases := []struct {
		name       string
		wasSlow    bool
		consec     int
		retries    int
		threshold  int
		success    bool
		latency    *float64
		wantSlow   bool
		wantConsec int
	}{
		{"disabled ignores latency", false, 0, 3, 0, true, f64(500), false, 0},
		{"first over-limit", false, 0, 3, 100, true, f64(150), false, 1},
		{"second over-limit", false, 1, 3, 100, true, f64(150), false, 2},
		{"third over-limit fires", false, 2, 3, 100, true, f64(150), true, 3},
		{"at threshold is not over", false, 2, 3, 100, true, f64(100), false, 0},
		{"under resets streak", false, 2, 3, 100, true, f64(80), false, 0},
		{"stay slow while over", true, 3, 3, 100, true, f64(150), true, 4},
		{"recover on under", true, 4, 3, 100, true, f64(80), false, 0},
		{"timeout is not slow", false, 2, 3, 100, false, nil, false, 0},
		{"timeout while slow stays slow", true, 3, 3, 100, false, nil, true, 0},
		{"retries 1 fires immediately", false, 0, 1, 100, true, f64(150), true, 1},
		{"nil latency success not over", false, 0, 1, 100, true, nil, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			slow, n := nextSlow(c.wasSlow, c.consec, c.retries, c.threshold, c.success, c.latency)
			if slow != c.wantSlow || n != c.wantConsec {
				t.Fatalf("nextSlow(...) = (%v,%d), want (%v,%d)", slow, n, c.wantSlow, c.wantConsec)
			}
		})
	}
}

func TestNextStatus(t *testing.T) {
	cases := []struct {
		name     string
		current  string
		success  bool
		fails    int
		retries  int
		expected string
		changed  bool
	}{
		{"pending first success", "pending", true, 1, 3, "up", true},
		{"up stays up on success", "up", true, 0, 3, "up", false},
		{"up single failure", "up", false, 1, 3, "up", false},
		{"up reaches threshold", "up", false, 3, 3, "down", true},
		{"down stays down", "down", false, 5, 3, "down", false},
		{"down recovers", "down", true, 3, 3, "up", true},
		{"pending reaches threshold", "pending", false, 3, 3, "down", true},
		{"pending below threshold", "pending", false, 2, 3, "pending", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := nextStatus(c.current, c.success, c.fails, c.retries)
			if got != c.expected || changed != c.changed {
				t.Fatalf("nextStatus(%s,%v,%d,%d) = (%s,%v), want (%s,%v)",
					c.current, c.success, c.fails, c.retries, got, changed, c.expected, c.changed)
			}
		})
	}
}
