package alerting

import (
	"testing"

	"argus/internal/store"
)

// PagerDuty and Opsgenie fold a new alert into an open one with the same
// key. Each kind of problem needs its own, or an outage arriving while a
// certificate warning is open pages nobody; and each recovery must name the
// key of the problem it ends, or it closes the wrong alert.
func TestAlertKeysSeparateKindsOfProblem(t *testing.T) {
	m := store.Monitor{ID: 7, Name: "shop"}
	key := func(p Payload) string { p.Monitor = m; return alertKey(p) }

	outage := key(Payload{Type: "down"})
	for _, same := range []Payload{{Type: "recovery"}, {Type: "escalation"}, {Type: "resend"}} {
		if got := key(same); got != outage {
			t.Errorf("%s belongs to the outage's alert: %q vs %q", same.Type, got, outage)
		}
	}
	slow := key(Payload{Type: "slow"})
	if got := key(Payload{Type: "recovery", Of: "slow"}); got != slow {
		t.Errorf("a slow monitor's recovery must close the slow alert: %q vs %q", got, slow)
	}
	seen := map[string]string{}
	for _, p := range []Payload{{Type: "down"}, {Type: "slow"}, {Type: "cert"}, {Type: "domain"}} {
		k := key(p)
		if other, dup := seen[k]; dup {
			t.Errorf("%s and %s share the key %q", p.Type, other, k)
		}
		seen[k] = p.Type
	}

	// Alerts that are not about a monitor.
	probe := alertKey(Payload{Type: "probe_down"})
	if got := alertKey(Payload{Type: "probe_up"}); got != probe {
		t.Errorf("the probe's recovery must close the probe's alert: %q vs %q", got, probe)
	}
	if got := alertKey(Payload{Type: "test", Monitor: store.Monitor{Name: "Argus"}}); got == probe {
		t.Errorf("a channel test shares the probe's key %q: an unresolved test would swallow a real probe alert", got)
	}
	nodeA := alertKey(Payload{Type: "probe_down", Key: "argus-probe-node-1"})
	nodeB := alertKey(Payload{Type: "probe_down", Key: "argus-probe-node-2"})
	if nodeA == nodeB || nodeA == probe {
		t.Errorf("probe nodes need their own keys: %q %q %q", nodeA, nodeB, probe)
	}
	if got := alertKey(Payload{Type: "probe_up", Key: "argus-probe-node-1"}); got != nodeA {
		t.Errorf("a node's recovery must close that node's alert: %q vs %q", got, nodeA)
	}
}
