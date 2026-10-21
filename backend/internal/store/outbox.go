package store

import (
	"context"
	"time"
)

// Outbox statuses.
const (
	OutboxPending = "pending"
	// OutboxSending is an alert a worker has taken and is sending now. It
	// goes back to pending if that fails, and is taken again by another
	// worker if the first never says how it went.
	OutboxSending    = "sending"
	OutboxSent       = "sent"
	OutboxFailed     = "failed"
	OutboxSkipped    = "skipped"
	OutboxSuperseded = "superseded"
)

// OutboxItem is one alert on its way to one destination.
type OutboxItem struct {
	ID        int64
	MonitorID int64
	Type      string
	// ChannelID is the channel to send through; nil sends email straight to
	// the addresses in Dest.
	ChannelID *int64
	// Dest is the channel's name, or the addresses, as alert history shows it.
	Dest string
	// Stream names the problem this alert is about, as a pager would group
	// it. Alerts in one stream to one destination are delivered in the
	// order they were queued. Empty for alerts that stand alone.
	Stream string
	// Resolves is true for an alert that says the problem is over.
	Resolves  bool
	Payload   []byte
	Attempts  int
	CreatedAt time.Time
}

// EnqueueAlerts stores alerts for delivery, due at once, in one transaction
// with the claim that gives the right to send them. It reports false, and
// stores nothing, when the claim is not ours. now is the sender's clock, the
// one retries and age are measured on.
//
// In the same transaction, each alert replaces any alert in its stream that
// is still waiting and says the opposite: a recovery replaces an outage
// nobody has been sent yet, and a new outage replaces a recovery from the
// one before. Doing that here, with the insert, means a failure leaves what
// was waiting untouched; done first and separately, it could drop the
// outage and then fail to store the recovery.
func (s *Store) EnqueueAlerts(ctx context.Context, items []OutboxItem, now time.Time, claim AlertClaim) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if claim != nil {
		ok, err := claim(ctx, tx)
		if err != nil || !ok {
			return false, err
		}
	}
	cleared := map[string]bool{}
	for _, it := range items {
		switch {
		case it.Stream == "":
		case it.Resolves && !cleared[it.Stream]:
			// The problem is over, so no alert about it that is still
			// waiting is true any more, wherever it was going. Dropping one
			// strands nobody: it was never delivered.
			cleared[it.Stream] = true
			if _, err := tx.Exec(ctx, `UPDATE alert_outbox SET status='superseded', finished_at=$2
				WHERE stream=$1 AND status='pending' AND NOT resolves`, it.Stream, now); err != nil {
				return false, err
			}
		case !it.Resolves:
			// The problem is back. A recovery still waiting for this
			// destination is no longer true. One waiting for a destination
			// this alert is not going to stays: a channel taken off the
			// monitor mid-outage is still owed the recovery that closes
			// what it was sent.
			if _, err := tx.Exec(ctx, `UPDATE alert_outbox o SET status='superseded', finished_at=$2
				WHERE o.stream=$1 AND o.status='pending' AND o.resolves AND `+sameDestination("o", "$3", "$4"),
				it.Stream, now, it.ChannelID, it.Dest); err != nil {
				return false, err
			}
		}
		// In both cases only what is waiting: an alert being sent this
		// moment is left to finish, and this one takes its turn behind it.
		if _, err := tx.Exec(ctx, `INSERT INTO alert_outbox (monitor_id, type, channel_id, dest, stream, resolves, payload, created_at, next_attempt_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)`, it.MonitorID, it.Type, it.ChannelID, it.Dest, it.Stream, it.Resolves, it.Payload, now); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}

// sameDestination is the SQL for "row alias is going where channel and dest
// say". A channel is its id: its name can be changed while alerts wait for
// it, and must not split their order. Mail sent straight to addresses has no
// channel, and is identified by the addresses.
func sameDestination(alias, channel, dest string) string {
	return "(" + alias + ".channel_id = " + channel + "::bigint OR (" + alias + ".channel_id IS NULL AND " + channel + "::bigint IS NULL AND " + alias + ".dest = " + dest + "))"
}

// claimLock serializes claims across every process, for the instant a claim
// takes. The count of what is in flight to a destination is only true if no
// other claim is being made at the same moment.
const claimLock int64 = 0x4172677573_01

// maxSendingPerChannel is how many alerts may be in flight to one channel.
// A channel that hangs then ties up this many workers and no more, however
// many alerts are waiting for it.
const maxSendingPerChannel = 2

// ClaimDueAlert takes the next alert that is due, marking it as being sent
// until now+lease. It returns nil when there is none.
//
// An alert is not taken while an older one in the same stream, for the same
// destination, is still waiting or being sent. That is what delivers a
// recovery after the outage it ends and never before it.
//
// A process that dies mid-send leaves its alert marked as being sent; once
// the lease has passed it is taken again.
func (s *Store) ClaimDueAlert(ctx context.Context, now time.Time, lease time.Duration) (*OutboxItem, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, claimLock); err != nil {
		return nil, err
	}
	var it OutboxItem
	err = tx.QueryRow(ctx, `UPDATE alert_outbox SET status='sending', attempts = attempts + 1, next_attempt_at = $2
		WHERE id = (
			SELECT o.id FROM alert_outbox o
			WHERE o.status IN ('pending', 'sending') AND o.next_attempt_at <= $1
			  AND NOT EXISTS (
				SELECT 1 FROM alert_outbox older
				WHERE o.stream <> '' AND older.stream = o.stream AND older.id < o.id
				  AND older.status IN ('pending', 'sending')
				  AND `+sameDestination("older", "o.channel_id", "o.dest")+`)
			  AND (SELECT count(*) FROM alert_outbox busy
				WHERE busy.status = 'sending' AND busy.next_attempt_at > $1
				  AND `+sameDestination("busy", "o.channel_id", "o.dest")+`) < $3
			ORDER BY o.next_attempt_at, o.id LIMIT 1)
		RETURNING id, monitor_id, type, channel_id, dest, stream, resolves, payload, attempts, created_at`,
		now, now.Add(lease), maxSendingPerChannel).Scan(
		&it.ID, &it.MonitorID, &it.Type, &it.ChannelID, &it.Dest, &it.Stream, &it.Resolves, &it.Payload, &it.Attempts, &it.CreatedAt)
	if err == pgxErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &it, tx.Commit(ctx)
}

// OvertakenAlert reports whether a newer alert in the same stream says the
// opposite of this one, whatever became of it: waiting, being sent, or
// already delivered. For an alert that says the problem is over, the newer
// one must be for the same destination; for one that says there is a
// problem, a newer "it is over" anywhere in the stream is enough. Sending this one now would tell its recipients
// something that has stopped being true.
func (s *Store) OvertakenAlert(ctx context.Context, it OutboxItem) (bool, error) {
	if it.Stream == "" {
		return false, nil
	}
	var overtaken bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM alert_outbox n
		WHERE n.stream = $1 AND n.id > $2 AND n.resolves <> $3
		  AND n.status IN ('pending', 'sending', 'sent')
		  AND (NOT $3 OR `+sameDestination("n", "$4", "$5")+`))`,
		it.Stream, it.ID, it.Resolves, it.ChannelID, it.Dest).Scan(&overtaken)
	return overtaken, err
}

// FinishAlert closes an alert that was being sent: sent, failed, skipped or
// superseded.
func (s *Store) FinishAlert(ctx context.Context, id int64, status, errText string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE alert_outbox SET status=$2, last_error=$3, finished_at=now()
		WHERE id=$1 AND status='sending'`, id, status, errText)
	return err
}

// RetryAlert puts an alert back to wait for another attempt.
func (s *Store) RetryAlert(ctx context.Context, id int64, at time.Time, errText string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE alert_outbox SET status='pending', next_attempt_at=$2, last_error=$3
		WHERE id=$1 AND status='sending'`, id, at, errText)
	return err
}

// DeferAlert puts an alert back without counting the attempt: nothing was
// tried, it was only not its turn.
func (s *Store) DeferAlert(ctx context.Context, id int64, at time.Time, why string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE alert_outbox SET status='pending', next_attempt_at=$2, last_error=$3, attempts = GREATEST(attempts - 1, 0)
		WHERE id=$1 AND status='sending'`, id, at, why)
	return err
}

// PurgeAlertOutbox drops finished alerts older than keep.
func (s *Store) PurgeAlertOutbox(ctx context.Context, keep time.Duration) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM alert_outbox WHERE status NOT IN ('pending', 'sending') AND finished_at < $1`, time.Now().UTC().Add(-keep))
	return err
}

// UnresolvedChannels lists the channels that were sent an alert in stream
// and have not since been sent, or queued, the one that says it is over. A
// recovery goes to these whatever the monitor's channels are now, so a
// channel taken off a monitor mid-outage is not left with an alert open.
func (s *Store) UnresolvedChannels(ctx context.Context, stream string) ([]int64, error) {
	if stream == "" {
		return nil, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT t.channel_id FROM alert_outbox t
		WHERE t.stream = $1 AND NOT t.resolves AND t.channel_id IS NOT NULL AND t.status IN ('sent', 'sending')
		  AND NOT EXISTS (SELECT 1 FROM alert_outbox r
			WHERE r.stream = t.stream AND r.resolves AND r.channel_id = t.channel_id AND r.id > t.id
			  AND r.status IN ('pending', 'sending', 'sent'))`, stream)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
