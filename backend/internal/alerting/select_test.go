package alerting

import (
	"testing"

	"argus/internal/store"
)

func TestSelectChannels(t *testing.T) {
	all := []store.Channel{
		{ID: 1, Name: "ops", Type: "slack", Events: []string{"down", "recovery"}},
		{ID: 2, Name: "noise", Type: "discord", Events: []string{"slow"}},
		{ID: 3, Name: "all", Type: "webhook", Events: store.DefaultChannelEvents},
		{ID: 4, Name: "mail", Type: "email", Dest: "ops@example.com", Events: []string{"slow"}},
	}

	got := SelectChannels(all, nil, "down")
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("default down = %#v", got)
	}

	got = SelectChannels(all, []int64{}, "down")
	if len(got) != 0 {
		t.Fatalf("explicit none = %#v", got)
	}

	got = SelectChannels(all, []int64{2, 3}, "down")
	if len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("subset down = %#v", got)
	}

	got = SelectChannels(all, []int64{2}, "slow")
	if len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("subset slow = %#v", got)
	}

	got = SelectChannels(all, nil, "test")
	if len(got) != 4 {
		t.Fatalf("test uses every channel, got %d", len(got))
	}

	got = SelectChannels(all, []int64{4}, "slow")
	if len(got) != 1 || got[0].Type != "email" {
		t.Fatalf("email slow = %#v", got)
	}
	got = SelectChannels(all, []int64{4}, "down")
	if len(got) != 0 {
		t.Fatalf("email down filtered = %#v", got)
	}
}
