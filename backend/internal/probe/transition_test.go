package probe

import (
	"errors"
	"testing"

	"argus/internal/store"
)

func TestTransitionAlert(t *testing.T) {
	fail := errors.New("timeout")
	cases := []struct {
		name      string
		t         store.StateTransition
		success   bool
		wantAlert string
		wantEvent string
	}{
		{
			name:      "down supersedes",
			t:         store.StateTransition{Changed: true, NewStatus: "down", BecameSlow: true, Slow: false},
			success:   false,
			wantAlert: "down",
			wantEvent: "down",
		},
		{
			name:      "become slow while up",
			t:         store.StateTransition{Changed: false, NewStatus: "up", BecameSlow: true, Slow: true},
			success:   true,
			wantAlert: "slow",
			wantEvent: "slow",
		},
		{
			name:      "recover from slow",
			t:         store.StateTransition{Changed: false, NewStatus: "up", RecoveredSlow: true, Slow: false},
			success:   true,
			wantAlert: "recovery",
			wantEvent: "up",
		},
		{
			name:      "recover from down",
			t:         store.StateTransition{Changed: true, OldStatus: "down", NewStatus: "up"},
			success:   true,
			wantAlert: "recovery",
			wantEvent: "up",
		},
		{
			name:      "pending to up is not recovery",
			t:         store.StateTransition{Changed: true, OldStatus: "pending", NewStatus: "up"},
			success:   true,
			wantAlert: "",
			wantEvent: "",
		},
		{
			name:      "pending to up and slow prefers slow",
			t:         store.StateTransition{Changed: true, NewStatus: "up", BecameSlow: true, Slow: true},
			success:   true,
			wantAlert: "slow",
			wantEvent: "slow",
		},
		{
			name:      "no event",
			t:         store.StateTransition{Changed: false, NewStatus: "up"},
			success:   true,
			wantAlert: "",
			wantEvent: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			alert, event, _ := transitionAlert(c.t, c.success, 120, fail)
			if alert != c.wantAlert || event != c.wantEvent {
				t.Fatalf("got alert=%q event=%q, want %q %q", alert, event, c.wantAlert, c.wantEvent)
			}
		})
	}
}
