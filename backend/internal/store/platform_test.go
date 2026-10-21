package store

import "testing"

func TestValidRole(t *testing.T) {
	if !ValidRole(RoleAdmin) || !ValidRole(RoleOperator) || !ValidRole(RoleViewer) {
		t.Fatal("expected known roles to be valid")
	}
	if ValidRole("root") {
		t.Fatal("root is not a role")
	}
}

func TestAssignedToNode(t *testing.T) {
	local := Monitor{Name: "a"}
	if !AssignedToNode(local, LocalProbeNodeID) {
		t.Fatal("unassigned monitor belongs to local")
	}
	if AssignedToNode(local, 9) {
		t.Fatal("unassigned monitor should not run on remote")
	}
	remote := Monitor{ProbeNodeIDs: []int64{4, 5}}
	if AssignedToNode(remote, LocalProbeNodeID) {
		t.Fatal("remote-only monitor should skip local")
	}
	if !AssignedToNode(remote, 5) {
		t.Fatal("assigned remote should match")
	}
}

func TestValidPlan(t *testing.T) {
	if !ValidPlan(PlanCommunity) || !ValidPlan(PlanPro) {
		t.Fatal("expected known plans to be valid")
	}
	if ValidPlan("enterprise") {
		t.Fatal("enterprise is not a plan")
	}
}

func TestParseSLARange(t *testing.T) {
	d := ParseSLARange("90d")
	if d.IsZero() {
		t.Fatal("expected a from date")
	}
}
