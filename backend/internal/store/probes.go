package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

const LocalProbeNodeID int64 = 0

// AssignedToNode reports whether nodeID is one of the monitor's locations.
func AssignedToNode(m Monitor, nodeID int64) bool {
	for _, id := range Locations(m) {
		if id == nodeID {
			return true
		}
	}
	return false
}

// Locations are where a monitor is checked from: its assigned probe nodes,
// where node 0 is this server, or this server alone when none are assigned.
func Locations(m Monitor) []int64 {
	// A heartbeat arrives here and a group has no check of its own.
	if len(m.ProbeNodeIDs) == 0 || m.Type == "push" || m.Type == "group" {
		return []int64{LocalProbeNodeID}
	}
	out := make([]int64, 0, len(m.ProbeNodeIDs))
	seen := map[int64]bool{}
	for _, id := range m.ProbeNodeIDs {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// ListEnabledMonitorsForNode is what one location should check now.
func (s *Store) ListEnabledMonitorsForNode(ctx context.Context, nodeID int64) ([]Monitor, error) {
	listed, err := s.ListEnabledMonitors(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Monitor, 0, len(listed))
	for _, m := range listed {
		if AssignedToNode(m, nodeID) {
			out = append(out, m)
		}
	}
	return out, nil
}

func hashProbeToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) ListProbeNodes(ctx context.Context) ([]ProbeNode, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, region, last_seen, enabled, created_at
		FROM probe_nodes WHERE ($1 = 0 OR org_id=$1) ORDER BY region ASC, name ASC, id ASC`, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProbeNode
	for rows.Next() {
		var n ProbeNode
		if err := rows.Scan(&n.ID, &n.Name, &n.Region, &n.LastSeen, &n.Enabled, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if out == nil {
		out = []ProbeNode{}
	}
	return out, rows.Err()
}

func (s *Store) CreateProbeNode(ctx context.Context, name, region string) (*ProbeNode, error) {
	name = strings.TrimSpace(name)
	region = strings.TrimSpace(region)
	if name == "" {
		return nil, errors.New("name is required")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	plain := "probe_" + hex.EncodeToString(raw)
	var n ProbeNode
	err := s.Pool.QueryRow(ctx, `INSERT INTO probe_nodes (name, region, token_hash, org_id)
		VALUES ($1,$2,$3,$4) RETURNING id, created_at`,
		name, region, hashProbeToken(plain), writeOrg(ctx)).Scan(&n.ID, &n.CreatedAt)
	if err != nil {
		return nil, err
	}
	n.Name = name
	n.Region = region
	n.Enabled = true
	n.Token = plain
	return &n, nil
}

// DeleteProbeNode removes a node and takes it off every monitor assigned to
// it. A monitor left with no location is checked from this server; left
// pointing at a node that is gone, nothing would check it at all.
//
// It returns the node as it was, so the caller can close an alert that the
// node's silence opened.
func (s *Store) DeleteProbeNode(ctx context.Context, id int64) (*WatchedProbeNode, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	node := WatchedProbeNode{ID: id}
	err = tx.QueryRow(ctx, `DELETE FROM probe_nodes WHERE id=$1 AND ($2 = 0 OR org_id=$2)
		RETURNING org_id, name, region, alerted_down`, id, OrgID(ctx)).Scan(&node.OrgID, &node.Name, &node.Region, &node.AlertedDown)
	if err == pgxErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Fewer locations may not be able to agree as many times as before.
	if _, err := tx.Exec(ctx, `UPDATE monitors
		SET probe_node_ids = array_remove(probe_node_ids, $1),
			confirm_locations = GREATEST(1, LEAST(confirm_locations, cardinality(array_remove(probe_node_ids, $1))))
		WHERE $1 = ANY(probe_node_ids)`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM probe_node_state WHERE node_id=$1`, id); err != nil {
		return nil, err
	}
	return &node, tx.Commit(ctx)
}

func (s *Store) ProbeNodeByToken(ctx context.Context, token string) (*ProbeNode, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrNotFound
	}
	var n ProbeNode
	err := s.Pool.QueryRow(ctx, `SELECT id, name, region, last_seen, enabled, created_at
		FROM probe_nodes WHERE token_hash=$1 AND enabled`, hashProbeToken(token)).Scan(
		&n.ID, &n.Name, &n.Region, &n.LastSeen, &n.Enabled, &n.CreatedAt)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_, _ = s.Pool.Exec(ctx, `UPDATE probe_nodes SET last_seen=now() WHERE id=$1`, n.ID)
	now := time.Now().UTC()
	n.LastSeen = &now
	return &n, nil
}

func (s *Store) ValidateProbeNodeIDs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	seen := map[int64]struct{}{}
	for _, id := range ids {
		if id == LocalProbeNodeID {
			continue
		}
		if id <= 0 {
			return errors.New("invalid probe node id")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		var exists bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM probe_nodes WHERE id=$1 AND enabled AND ($2 = 0 OR org_id=$2))`, id, OrgID(ctx)).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return errors.New("unknown probe node")
		}
	}
	return nil
}

func (s *Store) ListMonitorRegions(ctx context.Context, monitorID int64) ([]ProbeNodeView, error) {
	rows, err := s.Pool.Query(ctx, `SELECT s.node_id, COALESCE(n.name, 'Local'), COALESCE(n.region, 'local'),
		s.status, s.last_latency_ms, s.last_check_at, s.last_error, s.consecutive_failures
		FROM probe_node_state s
		LEFT JOIN probe_nodes n ON n.id = s.node_id
		WHERE s.monitor_id=$1
		ORDER BY COALESCE(n.region, 'local'), s.node_id`, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProbeNodeView
	for rows.Next() {
		var v ProbeNodeView
		if err := rows.Scan(&v.NodeID, &v.Name, &v.Region, &v.Status, &v.LastLatencyMs, &v.LastCheckAt, &v.LastError, &v.ConsecutiveFailures); err != nil {
			return nil, err
		}
		if v.NodeID == LocalProbeNodeID {
			v.Name = "Local"
			v.Region = "local"
		}
		out = append(out, v)
	}
	if out == nil {
		out = []ProbeNodeView{}
	}
	return out, rows.Err()
}

// WatchedProbeNode is a probe node that has work: at least one enabled
// monitor is assigned to it.
type WatchedProbeNode struct {
	ID          int64
	OrgID       int64
	Name        string
	Region      string
	LastSeen    *time.Time
	CreatedAt   time.Time
	AlertedDown bool
	Monitors    int
}

// WatchedProbeNodes lists the enabled nodes that enabled monitors are
// assigned to, with how many, for the check that they are still reporting.
func (s *Store) WatchedProbeNodes(ctx context.Context) ([]WatchedProbeNode, error) {
	rows, err := s.Pool.Query(ctx, `SELECT n.id, n.org_id, n.name, n.region, n.last_seen, n.created_at, n.alerted_down,
			(SELECT count(*) FROM monitors m WHERE m.enabled AND n.id = ANY(m.probe_node_ids))
		FROM probe_nodes n WHERE n.enabled ORDER BY n.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WatchedProbeNode
	for rows.Next() {
		var n WatchedProbeNode
		if err := rows.Scan(&n.ID, &n.OrgID, &n.Name, &n.Region, &n.LastSeen, &n.CreatedAt, &n.AlertedDown, &n.Monitors); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
