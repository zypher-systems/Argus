package store

import (
	"context"
	"testing"
)

func TestRealmManyToManyAndIsolation(t *testing.T) {
	st := openTestStore(t)
	adminCtx := WithOrg(context.Background(), 1)

	r, err := st.CreateRealm(adminCtx, "Websites")
	if err != nil {
		t.Fatal(err)
	}
	m := Monitor{Name: "site", Type: "icmp", Target: "127.0.0.1", IntervalSeconds: 60, Retries: 3, Enabled: true}
	if err := st.CreateMonitor(adminCtx, &m); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMonitorRealms(adminCtx, m.ID, []int64{r.ID}); err != nil {
		t.Fatal(err)
	}
	ids, err := st.LoadRealmIDs(adminCtx, m.ID)
	if err != nil || len(ids) != 1 || ids[0] != r.ID {
		t.Fatalf("realm ids: %v %v", ids, err)
	}

	apps, err := st.CreateRealm(adminCtx, "Apps")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMonitorRealms(adminCtx, m.ID, []int64{r.ID, apps.ID}); err != nil {
		t.Fatal(err)
	}
	ids, err = st.LoadRealmIDs(adminCtx, m.ID)
	if err != nil || len(ids) != 2 {
		t.Fatalf("want 2 realms, got %v %v", ids, err)
	}

	other, err := st.CreateOrg(adminCtx, "Other", "other", PlanCommunity)
	if err != nil {
		t.Fatal(err)
	}
	otherCtx := WithOrg(context.Background(), other.ID)
	if err := st.SetMonitorRealms(otherCtx, m.ID, []int64{r.ID}); err == nil {
		t.Fatal("expected other org to fail assigning this org's realm")
	}

	listed, err := st.ListRealms(otherCtx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("other org should not see realms, got %d", len(listed))
	}

	foreign, err := st.CreateRealm(otherCtx, "OtherSites")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetMonitorRealms(otherCtx, m.ID, []int64{foreign.ID}); err == nil {
		t.Fatal("expected other org not to attach its realm to this monitor")
	}

	if _, err := st.CreateRealm(adminCtx, "websites"); err == nil {
		t.Fatal("expected duplicate name to fail")
	}
	if err := st.SetMonitorRealms(adminCtx, m.ID, []int64{999999}); err == nil {
		t.Fatal("expected unknown realm to fail")
	}
}
