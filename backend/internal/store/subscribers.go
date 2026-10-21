package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

func hashSubscriberToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newSubscriberToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *Store) ListSubscribers(ctx context.Context, pageID int64) ([]StatusSubscriber, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, status_page_id, kind, dest, confirmed, created_at
		FROM status_subscribers WHERE status_page_id=$1 ORDER BY created_at DESC, id DESC`, pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatusSubscriber
	for rows.Next() {
		var sub StatusSubscriber
		if err := rows.Scan(&sub.ID, &sub.StatusPageID, &sub.Kind, &sub.Dest, &sub.Confirmed, &sub.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	if out == nil {
		out = []StatusSubscriber{}
	}
	return out, rows.Err()
}

func (s *Store) CreateSubscriber(ctx context.Context, pageID int64, kind, dest string, confirmed bool) (*StatusSubscriber, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	dest = strings.TrimSpace(dest)
	if kind != "email" && kind != "webhook" {
		return nil, errors.New("kind must be email or webhook")
	}
	if dest == "" {
		return nil, errors.New("destination is required")
	}
	token, err := newSubscriberToken()
	if err != nil {
		return nil, err
	}
	hash := hashSubscriberToken(token)

	var existing StatusSubscriber
	err = s.Pool.QueryRow(ctx, `SELECT id, status_page_id, kind, dest, confirmed, created_at
		FROM status_subscribers WHERE status_page_id=$1 AND kind=$2 AND dest=$3`,
		pageID, kind, dest).Scan(&existing.ID, &existing.StatusPageID, &existing.Kind, &existing.Dest, &existing.Confirmed, &existing.CreatedAt)
	if err == nil {
		if existing.Confirmed && kind == "email" && !confirmed {
			return nil, errors.New("already subscribed")
		}
		if confirmed {
			_, err = s.Pool.Exec(ctx, `UPDATE status_subscribers SET confirmed=TRUE, token_hash=$2 WHERE id=$1`, existing.ID, hash)
		} else {
			_, err = s.Pool.Exec(ctx, `UPDATE status_subscribers SET token_hash=$2, confirmed=FALSE WHERE id=$1`, existing.ID, hash)
			existing.Confirmed = false
		}
		if err != nil {
			return nil, err
		}
		existing.Token = token
		if confirmed {
			existing.Confirmed = true
		}
		return &existing, nil
	}
	if err != pgxErrNoRows {
		return nil, err
	}

	var sub StatusSubscriber
	unsub, err := newSubscriberToken()
	if err != nil {
		return nil, err
	}
	err = s.Pool.QueryRow(ctx, `INSERT INTO status_subscribers (status_page_id, kind, dest, token_hash, confirmed, unsubscribe_token)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, status_page_id, kind, dest, confirmed, created_at`,
		pageID, kind, dest, hash, confirmed, unsub).Scan(
		&sub.ID, &sub.StatusPageID, &sub.Kind, &sub.Dest, &sub.Confirmed, &sub.CreatedAt)
	if err != nil {
		return nil, err
	}
	sub.Token = token
	return &sub, nil
}

func (s *Store) ConfirmSubscriber(ctx context.Context, token string) (*StatusSubscriber, error) {
	hash := hashSubscriberToken(strings.TrimSpace(token))
	var sub StatusSubscriber
	err := s.Pool.QueryRow(ctx, `UPDATE status_subscribers SET confirmed=TRUE WHERE token_hash=$1
		RETURNING id, status_page_id, kind, dest, confirmed, created_at`, hash).Scan(
		&sub.ID, &sub.StatusPageID, &sub.Kind, &sub.Dest, &sub.Confirmed, &sub.CreatedAt)
	if err != nil {
		if err == pgxErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &sub, nil
}

// DeleteSubscriberByToken removes a subscription by its confirm token or by
// the unsubscribe token its emails carry.
func (s *Store) DeleteSubscriberByToken(ctx context.Context, token string) error {
	token = strings.TrimSpace(token)
	hash := hashSubscriberToken(token)
	tag, err := s.Pool.Exec(ctx, `DELETE FROM status_subscribers WHERE token_hash=$1 OR unsubscribe_token=$2`, hash, token)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSubscriber(ctx context.Context, pageID, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM status_subscribers WHERE id=$1 AND status_page_id=$2`, id, pageID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ConfirmedSubscribersForMonitor(ctx context.Context, monitorID int64) ([]StatusSubscriber, error) {
	rows, err := s.Pool.Query(ctx, `SELECT sub.id, sub.status_page_id, sub.kind, sub.dest, sub.confirmed, sub.created_at, sub.unsubscribe_token
		FROM status_subscribers sub
		JOIN status_pages p ON p.id = sub.status_page_id
		WHERE sub.confirmed = TRUE AND p.published = TRUE
		AND (p.monitor_ids IS NULL OR $1 = ANY(p.monitor_ids))`, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatusSubscriber
	for rows.Next() {
		var sub StatusSubscriber
		if err := rows.Scan(&sub.ID, &sub.StatusPageID, &sub.Kind, &sub.Dest, &sub.Confirmed, &sub.CreatedAt, &sub.UnsubscribeToken); err != nil {
			return nil, err
		}
		out = append(out, sub)
	}
	if out == nil {
		out = []StatusSubscriber{}
	}
	return out, rows.Err()
}
