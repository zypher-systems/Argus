package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type Realm struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

func (s *Store) ListRealms(ctx context.Context) ([]Realm, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, created_at::text FROM realms
		WHERE ($1 = 0 OR org_id = $1) ORDER BY lower(name)`, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Realm
	for rows.Next() {
		var r Realm
		if err := rows.Scan(&r.ID, &r.Name, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) CreateRealm(ctx context.Context, name string) (*Realm, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	if len(name) > 80 {
		return nil, errors.New("name must be at most 80 characters")
	}
	var r Realm
	err := s.Pool.QueryRow(ctx, `INSERT INTO realms (name, org_id) VALUES ($1,$2)
		RETURNING id, name, created_at::text`, name, writeOrg(ctx)).
		Scan(&r.ID, &r.Name, &r.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, errors.New("a realm with that name already exists")
		}
		return nil, err
	}
	return &r, nil
}

func (s *Store) RenameRealm(ctx context.Context, id int64, name string) (*Realm, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("name is required")
	}
	if len(name) > 80 {
		return nil, errors.New("name must be at most 80 characters")
	}
	var r Realm
	err := s.Pool.QueryRow(ctx, `UPDATE realms SET name=$1
		WHERE id=$2 AND ($3 = 0 OR org_id=$3)
		RETURNING id, name, created_at::text`, name, id, OrgID(ctx)).
		Scan(&r.ID, &r.Name, &r.CreatedAt)
	if err != nil {
		if errors.Is(err, pgxErrNoRows) {
			return nil, ErrNotFound
		}
		if isUniqueViolation(err) {
			return nil, errors.New("a realm with that name already exists")
		}
		return nil, err
	}
	return &r, nil
}

func (s *Store) DeleteRealm(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM realms WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, id, OrgID(ctx))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ValidateRealmIDs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	seen := map[int64]struct{}{}
	uniq := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return errors.New("invalid realm id")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	var n int
	err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM realms WHERE id = ANY($1) AND ($2 = 0 OR org_id=$2)`, uniq, OrgID(ctx)).Scan(&n)
	if err != nil {
		return err
	}
	if n != len(uniq) {
		return errors.New("unknown realm")
	}
	return nil
}

func (s *Store) SetMonitorRealms(ctx context.Context, monitorID int64, realmIDs []int64) error {
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM monitors WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, monitorID, OrgID(ctx)).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if err := s.ValidateRealmIDs(ctx, realmIDs); err != nil {
		return err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM realm_monitors WHERE monitor_id=$1`, monitorID); err != nil {
		return err
	}
	for _, rid := range uniquePositive(realmIDs) {
		if _, err := tx.Exec(ctx, `INSERT INTO realm_monitors (realm_id, monitor_id) VALUES ($1,$2)`, rid, monitorID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) MapMonitorRealmIDs(ctx context.Context, monitorIDs []int64) (map[int64][]int64, error) {
	out := map[int64][]int64{}
	if len(monitorIDs) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT monitor_id, realm_id FROM realm_monitors
		WHERE monitor_id = ANY($1) ORDER BY monitor_id, realm_id`, monitorIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid, rid int64
		if err := rows.Scan(&mid, &rid); err != nil {
			return nil, err
		}
		out[mid] = append(out[mid], rid)
	}
	return out, rows.Err()
}

func (s *Store) AttachRealmIDs(ctx context.Context, monitors []MonitorWithState) error {
	ids := make([]int64, 0, len(monitors))
	for i := range monitors {
		ids = append(ids, monitors[i].ID)
		if monitors[i].RealmIDs == nil {
			monitors[i].RealmIDs = []int64{}
		}
	}
	m, err := s.MapMonitorRealmIDs(ctx, ids)
	if err != nil {
		return err
	}
	for i := range monitors {
		if ids := m[monitors[i].ID]; ids != nil {
			monitors[i].RealmIDs = ids
		}
	}
	return nil
}

func (s *Store) LoadRealmIDs(ctx context.Context, monitorID int64) ([]int64, error) {
	m, err := s.MapMonitorRealmIDs(ctx, []int64{monitorID})
	if err != nil {
		return nil, err
	}
	ids := m[monitorID]
	if ids == nil {
		ids = []int64{}
	}
	return ids, nil
}

func uniquePositive(ids []int64) []int64 {
	seen := map[int64]struct{}{}
	var out []int64
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func isUniqueViolation(err error) bool {
	var e *pgconn.PgError
	return errors.As(err, &e) && e.Code == "23505"
}
