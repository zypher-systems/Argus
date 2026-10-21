package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
)

func (s *Store) InsertAudit(ctx context.Context, userID *int64, email, action, resource string, resourceID *int64, detail any, ip string) error {
	if action == "" {
		return nil
	}
	raw, err := json.Marshal(detail)
	if err != nil || raw == nil {
		raw = []byte("{}")
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO audit_log (user_id, email, action, resource, resource_id, detail, ip, org_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		userID, email, action, resource, resourceID, raw, ip, orgIDPtr(ctx))
	return err
}

func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, ts, user_id, email, action, resource, resource_id, detail, ip
		FROM audit_log WHERE ($2 = 0 OR org_id=$2) ORDER BY ts DESC, id DESC LIMIT $1`, limit, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var detail []byte
		if err := rows.Scan(&e.ID, &e.TS, &e.UserID, &e.Email, &e.Action, &e.Resource, &e.ResourceID, &detail, &e.IP); err != nil {
			return nil, err
		}
		if len(detail) > 0 {
			_ = json.Unmarshal(detail, &e.Detail)
		}
		out = append(out, e)
	}
	if out == nil {
		out = []AuditEntry{}
	}
	return out, rows.Err()
}

func (s *Store) EnsureInstanceID(ctx context.Context) (string, error) {
	var id string
	err := s.getSetting(ctx, "instance_id", &id)
	if err == nil && id != "" {
		return id, nil
	}
	id, err = newInstanceID()
	if err != nil {
		return "", err
	}
	if err := s.setSetting(ctx, "instance_id", id); err != nil {
		return "", err
	}
	return id, nil
}

func newInstanceID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return hex.EncodeToString(b[0:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:]), nil
}

func orgIDPtr(ctx context.Context) *int64 {
	id := OrgID(ctx)
	if id <= 0 {
		return nil
	}
	return &id
}
