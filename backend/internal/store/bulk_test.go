package store

import "testing"

func TestMergeMonitorTag(t *testing.T) {
	cases := []struct {
		existing, tag, want string
	}{
		{"", "prod", "prod"},
		{"prod", "prod", "prod"},
		{"prod", "PROD", "prod"},
		{"prod, edge", "core", "prod, edge, core"},
		{" prod,  edge ", "edge", "prod, edge"},
	}
	for _, c := range cases {
		if got := mergeMonitorTag(c.existing, c.tag); got != c.want {
			t.Errorf("mergeMonitorTag(%q, %q) = %q want %q", c.existing, c.tag, got, c.want)
		}
	}
}
