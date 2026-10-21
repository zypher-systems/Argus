package store

import (
	"context"
	"fmt"
	"slices"
	"time"
)

const (
	ChannelWebhook   = "webhook"
	ChannelSlack     = "slack"
	ChannelDiscord   = "discord"
	ChannelEmail     = "email"
	ChannelPagerDuty = "pagerduty"
	ChannelOpsgenie  = "opsgenie"
	ChannelTeams     = "teams"
	ChannelTelegram  = "telegram"
	ChannelNtfy      = "ntfy"
)

// ChannelTypes is every channel Argus can deliver to, in the order the UI
// lists them.
var ChannelTypes = []string{
	ChannelEmail, ChannelSlack, ChannelDiscord, ChannelTeams,
	ChannelPagerDuty, ChannelOpsgenie, ChannelTelegram, ChannelNtfy, ChannelWebhook,
}

// ChannelIsURL reports whether a channel's dest is a plain http(s) URL.
func ChannelIsURL(kind string) bool {
	switch kind {
	case ChannelWebhook, ChannelSlack, ChannelDiscord, ChannelTeams, ChannelNtfy:
		return true
	default:
		return false
	}
}

var DefaultChannelEvents = []string{"down", "recovery", "slow", "cert", "domain", "escalation", "probe_down", "probe_up", "resend"}

type Channel struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Dest      string    `json:"dest"`
	Events    []string  `json:"events"`
	CreatedAt time.Time `json:"created_at"`
}

func (c Channel) Allows(event string) bool {
	if event == "test" {
		return true
	}
	return slices.Contains(c.Events, event)
}

func (s *Store) ListChannels(ctx context.Context) ([]Channel, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, type, dest, events, created_at
		FROM notification_channels WHERE ($1 = 0 OR org_id=$1) ORDER BY name ASC, id ASC`, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.Dest, &c.Events, &c.CreatedAt); err != nil {
			return nil, err
		}
		if c.Events == nil {
			c.Events = []string{}
		}
		out = append(out, c)
	}
	if out == nil {
		out = []Channel{}
	}
	return out, rows.Err()
}

func (s *Store) GetChannel(ctx context.Context, id int64) (*Channel, error) {
	var c Channel
	err := s.Pool.QueryRow(ctx, `SELECT id, name, type, dest, events, created_at
		FROM notification_channels WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, id, OrgID(ctx)).Scan(&c.ID, &c.Name, &c.Type, &c.Dest, &c.Events, &c.CreatedAt)
	if err != nil {
		if err == pgxErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if c.Events == nil {
		c.Events = []string{}
	}
	return &c, nil
}

func (s *Store) CreateChannel(ctx context.Context, c *Channel) error {
	return s.Pool.QueryRow(ctx, `INSERT INTO notification_channels (name, type, dest, events, org_id)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
		c.Name, c.Type, c.Dest, c.Events, writeOrg(ctx)).Scan(&c.ID, &c.CreatedAt)
}

func (s *Store) UpdateChannel(ctx context.Context, c *Channel) error {
	tag, err := s.Pool.Exec(ctx, `UPDATE notification_channels SET name=$1, type=$2, dest=$3, events=$4
		WHERE id=$5 AND ($6 = 0 OR org_id=$6)`, c.Name, c.Type, c.Dest, c.Events, c.ID, OrgID(ctx))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteChannel removes a channel and takes it off every monitor attached to
// it. A monitor left with none goes back to the default, every channel: an
// id that no longer exists matched nothing, so a monitor attached only to a
// deleted channel alerted nobody and said so nowhere.
func (s *Store) DeleteChannel(ctx context.Context, id int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `DELETE FROM notification_channels WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, id, OrgID(ctx))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE monitors
		SET channel_ids = NULLIF(array_remove(channel_ids, $1), '{}')
		WHERE $1 = ANY(channel_ids)`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ValidateChannelIDs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	seen := map[int64]struct{}{}
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("invalid channel id")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate channel id")
		}
		seen[id] = struct{}{}
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM notification_channels WHERE id = ANY($1) AND ($2 = 0 OR org_id=$2)`, ids, OrgID(ctx)).Scan(&n); err != nil {
		return err
	}
	if n != len(ids) {
		return fmt.Errorf("unknown notification channel")
	}
	return nil
}
