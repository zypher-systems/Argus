package store

import (
	"context"
	"fmt"
	"strings"
)

const maxBulkIDs = 250

func normalizeBulkIDs(ids []int64) ([]int64, error) {
	out := uniquePositive(ids)
	if len(out) == 0 {
		return nil, fmt.Errorf("ids are required")
	}
	if len(out) > maxBulkIDs {
		return nil, fmt.Errorf("at most %d monitors per bulk request", maxBulkIDs)
	}
	return out, nil
}

func mergeMonitorTag(existing, tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return strings.TrimSpace(existing)
	}
	var parts []string
	seen := map[string]struct{}{}
	for _, p := range strings.Split(existing, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		key := strings.ToLower(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		parts = append(parts, p)
	}
	if _, ok := seen[strings.ToLower(tag)]; !ok {
		parts = append(parts, tag)
	}
	return strings.Join(parts, ", ")
}

func (s *Store) BulkSetEnabled(ctx context.Context, ids []int64, enabled bool) (int64, error) {
	ids, err := normalizeBulkIDs(ids)
	if err != nil {
		return 0, err
	}
	matched, _, err := s.setEnabled(ctx, ids, enabled, "bulk")
	if err != nil {
		return 0, err
	}
	if err := s.NotifyConfig(ctx); err != nil {
		return matched, err
	}
	return matched, nil
}

// setEnabled pauses or resumes this organization's monitors among ids, in
// one statement. It returns how many of them are this organization's, and
// how many actually changed.
//
// Only a monitor that changes is touched further: it gets its "paused" or
// "resumed" event, and one that is switched back on has resumed_at set,
// which holds back anything owed for it until it has been checked again.
// Marking every id asked for, as this first did, held back monitors that
// had never been paused, and reached monitors belonging to someone else.
func (s *Store) setEnabled(ctx context.Context, ids []int64, enabled bool, note string) (matched, changed int64, err error) {
	event := "paused"
	if enabled {
		event = "resumed"
	}
	err = s.Pool.QueryRow(ctx, `WITH before AS (
			SELECT id, enabled FROM monitors
			WHERE id = ANY($2) AND ($3 = 0 OR org_id = $3) FOR UPDATE
		), switched AS (
			UPDATE monitors m SET enabled = $1 FROM before b
			WHERE m.id = b.id AND b.enabled <> $1 RETURNING m.id
		), marked AS (
			UPDATE monitor_state st SET resumed_at = now() FROM switched
			WHERE $1 AND st.monitor_id = switched.id RETURNING 1
		), logged AS (
			INSERT INTO monitor_events (monitor_id, type, detail)
			SELECT id, $4, $5 FROM switched RETURNING 1
		)
		SELECT (SELECT count(*) FROM before), (SELECT count(*) FROM switched),
			(SELECT count(*) FROM marked), (SELECT count(*) FROM logged)`,
		enabled, ids, OrgID(ctx), event, note).Scan(&matched, &changed, new(int64), new(int64))
	return matched, changed, err
}

func (s *Store) BulkDelete(ctx context.Context, ids []int64) (int64, error) {
	ids, err := normalizeBulkIDs(ids)
	if err != nil {
		return 0, err
	}
	tag, err := s.Pool.Exec(ctx, `DELETE FROM monitors WHERE id = ANY($1) AND ($2 = 0 OR org_id=$2)`, ids, OrgID(ctx))
	if err != nil {
		return 0, err
	}
	n := tag.RowsAffected()
	if err := s.NotifyConfig(ctx); err != nil {
		return n, err
	}
	return n, nil
}

func (s *Store) BulkAddTag(ctx context.Context, ids []int64, tag string) (int64, error) {
	ids, err := normalizeBulkIDs(ids)
	if err != nil {
		return 0, err
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return 0, fmt.Errorf("tag is required")
	}
	if strings.Contains(tag, ",") {
		return 0, fmt.Errorf("tag cannot contain a comma")
	}
	if len(tag) > 40 {
		return 0, fmt.Errorf("tag must be at most 40 characters")
	}
	var n int64
	for _, id := range ids {
		var existing string
		err := s.Pool.QueryRow(ctx, `SELECT tags FROM monitors WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, id, OrgID(ctx)).Scan(&existing)
		if err != nil {
			if err == pgxErrNoRows {
				continue
			}
			return n, err
		}
		next := mergeMonitorTag(existing, tag)
		if next == existing {
			continue
		}
		if _, err := s.Pool.Exec(ctx, `UPDATE monitors SET tags=$2 WHERE id=$1`, id, next); err != nil {
			return n, err
		}
		n++
	}
	if err := s.NotifyConfig(ctx); err != nil {
		return n, err
	}
	return n, nil
}

func (s *Store) BulkAddRealm(ctx context.Context, ids []int64, realmID int64) (int64, error) {
	ids, err := normalizeBulkIDs(ids)
	if err != nil {
		return 0, err
	}
	if err := s.ValidateRealmIDs(ctx, []int64{realmID}); err != nil {
		return 0, err
	}
	tag, err := s.Pool.Exec(ctx, `INSERT INTO realm_monitors (realm_id, monitor_id)
		SELECT $1, m.id FROM monitors m
		WHERE m.id = ANY($2) AND ($3 = 0 OR m.org_id=$3)
		ON CONFLICT DO NOTHING`, realmID, ids, OrgID(ctx))
	if err != nil {
		return 0, err
	}
	n := tag.RowsAffected()
	if err := s.NotifyConfig(ctx); err != nil {
		return n, err
	}
	return n, nil
}

func (s *Store) BulkAttachChannel(ctx context.Context, ids []int64, channelID int64) (int64, error) {
	ids, err := normalizeBulkIDs(ids)
	if err != nil {
		return 0, err
	}
	if err := s.ValidateChannelIDs(ctx, []int64{channelID}); err != nil {
		return 0, err
	}
	tag, err := s.Pool.Exec(ctx, `UPDATE monitors SET channel_ids = array_append(channel_ids, $1)
		WHERE id = ANY($2) AND ($3 = 0 OR org_id=$3)
		AND channel_ids IS NOT NULL AND NOT ($1 = ANY(channel_ids))`,
		channelID, ids, OrgID(ctx))
	if err != nil {
		return 0, err
	}
	n := tag.RowsAffected()
	if err := s.NotifyConfig(ctx); err != nil {
		return n, err
	}
	return n, nil
}

func (s *Store) CountChannels(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM notification_channels WHERE ($1 = 0 OR org_id=$1)`, OrgID(ctx)).Scan(&n)
	return n, err
}
