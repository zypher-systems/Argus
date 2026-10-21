package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, token_prefix, created_at, last_used_at
		FROM api_tokens WHERE ($1 = 0 OR org_id=$1) ORDER BY created_at DESC, id DESC`, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedAt, &t.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if out == nil {
		out = []APIToken{}
	}
	return out, rows.Err()
}

func (s *Store) CreateAPIToken(ctx context.Context, userID int64, name string) (*APIToken, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	plain := "argus_" + hex.EncodeToString(raw)
	prefix := plain[:12]
	var t APIToken
	err := s.Pool.QueryRow(ctx, `INSERT INTO api_tokens (name, token_hash, token_prefix, user_id, org_id)
		VALUES ($1,$2,$3,$4,$5) RETURNING id, created_at`,
		name, hashToken(plain), prefix, userID, writeOrg(ctx)).Scan(&t.ID, &t.CreatedAt)
	if err != nil {
		return nil, err
	}
	t.Name = name
	t.Prefix = prefix
	t.Token = plain
	t.UserID = userID
	return &t, nil
}

func (s *Store) DeleteAPIToken(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM api_tokens WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, id, OrgID(ctx))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UserIDByAPIToken(ctx context.Context, token string) (int64, bool, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, false, nil
	}
	var id int64
	err := s.Pool.QueryRow(ctx, `SELECT id FROM api_tokens WHERE token_hash=$1`, hashToken(token)).Scan(&id)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	_, _ = s.Pool.Exec(ctx, `UPDATE api_tokens SET last_used_at=now() WHERE id=$1`, id)
	var userID int64
	err = s.Pool.QueryRow(ctx, `SELECT COALESCE(t.user_id, (SELECT id FROM users ORDER BY id ASC LIMIT 1))
		FROM api_tokens t WHERE t.id=$1`, id).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return userID, true, nil
}
