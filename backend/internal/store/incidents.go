package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var incidentStatuses = map[string]struct{}{
	"open":          {},
	"investigating": {},
	"identified":    {},
	"monitoring":    {},
	"resolved":      {},
}

var incidentImpacts = map[string]struct{}{
	"none":     {},
	"minor":    {},
	"major":    {},
	"critical": {},
}

func ValidIncidentStatus(s string) bool {
	_, ok := incidentStatuses[s]
	return ok
}

func ValidIncidentImpact(s string) bool {
	_, ok := incidentImpacts[s]
	return ok
}

func scanIncident(scan func(dest ...any) error) (Incident, error) {
	var inc Incident
	err := scan(&inc.ID, &inc.MonitorID, &inc.MonitorName, &inc.Status, &inc.Title, &inc.Impact,
		&inc.Public, &inc.AutoOpened, &inc.OpenedAt, &inc.ResolvedAt)
	return inc, err
}

const incidentColumns = `i.id, i.monitor_id, COALESCE(m.name, ''), i.status, i.title, i.impact,
	i.public, i.auto_opened, i.opened_at, i.resolved_at`

func (s *Store) ListIncidents(ctx context.Context, openOnly bool) ([]Incident, error) {
	q := `SELECT ` + incidentColumns + `
		FROM incidents i LEFT JOIN monitors m ON m.id = i.monitor_id
		WHERE ($1 = 0 OR i.org_id=$1)`
	args := []any{OrgID(ctx)}
	if openOnly {
		q += ` AND i.resolved_at IS NULL`
	}
	q += ` ORDER BY i.opened_at DESC, i.id DESC LIMIT 200`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		inc, err := scanIncident(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	if out == nil {
		out = []Incident{}
	}
	return out, rows.Err()
}

func (s *Store) GetIncident(ctx context.Context, id int64) (*Incident, error) {
	var inc Incident
	err := s.Pool.QueryRow(ctx, `SELECT `+incidentColumns+`
		FROM incidents i LEFT JOIN monitors m ON m.id = i.monitor_id WHERE i.id=$1 AND ($2 = 0 OR i.org_id=$2)`, id, OrgID(ctx)).Scan(
		&inc.ID, &inc.MonitorID, &inc.MonitorName, &inc.Status, &inc.Title, &inc.Impact,
		&inc.Public, &inc.AutoOpened, &inc.OpenedAt, &inc.ResolvedAt)
	if err != nil {
		if err == pgxErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	updates, err := s.listIncidentUpdates(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	inc.Updates = updates[id]
	if inc.Updates == nil {
		inc.Updates = []IncidentUpdate{}
	}
	return &inc, nil
}

func (s *Store) CreateIncident(ctx context.Context, inc *Incident) error {
	if inc.Status == "" {
		inc.Status = "open"
	}
	if inc.Impact == "" {
		inc.Impact = "major"
	}
	if inc.OpenedAt.IsZero() {
		inc.OpenedAt = time.Now().UTC()
	}
	err := s.Pool.QueryRow(ctx, `INSERT INTO incidents (monitor_id, status, title, impact, public, auto_opened, opened_at, org_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,COALESCE((SELECT org_id FROM monitors WHERE id=$1), $8)) RETURNING id`,
		inc.MonitorID, inc.Status, inc.Title, inc.Impact, inc.Public, inc.AutoOpened, inc.OpenedAt, writeOrg(ctx)).Scan(&inc.ID)
	if err != nil {
		return err
	}
	msg := "Incident opened"
	if len(inc.Updates) > 0 && strings.TrimSpace(inc.Updates[0].Message) != "" {
		msg = inc.Updates[0].Message
	}
	u := IncidentUpdate{IncidentID: inc.ID, Status: inc.Status, Message: msg, CreatedAt: inc.OpenedAt}
	if err := s.InsertIncidentUpdate(ctx, &u); err != nil {
		return err
	}
	inc.Updates = []IncidentUpdate{u}
	return nil
}

func (s *Store) OpenAutoIncident(ctx context.Context, monitorID int64, name, detail string, at time.Time) (*Incident, bool, error) {
	return openAutoIncident(ctx, s.Pool, monitorID, name, detail, at)
}

// incidentWriter is the pool, or a transaction on it.
type incidentWriter interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func openAutoIncident(ctx context.Context, db incidentWriter, monitorID int64, name, detail string, at time.Time) (*Incident, bool, error) {
	title := strings.TrimSpace(name) + " is down"
	msg := PublicEventDetail(detail)
	if msg == "" {
		msg = "Check failed"
	}
	var inc Incident
	err := db.QueryRow(ctx, `INSERT INTO incidents (monitor_id, status, title, impact, public, auto_opened, opened_at, org_id)
		VALUES ($1,'open',$2,'major',TRUE,TRUE,$3,(SELECT org_id FROM monitors WHERE id=$1))
		ON CONFLICT (monitor_id) WHERE resolved_at IS NULL AND auto_opened = TRUE AND monitor_id IS NOT NULL
		DO NOTHING
		RETURNING id, monitor_id, status, title, impact, public, auto_opened, opened_at, resolved_at`,
		monitorID, title, at).Scan(&inc.ID, &inc.MonitorID, &inc.Status, &inc.Title, &inc.Impact,
		&inc.Public, &inc.AutoOpened, &inc.OpenedAt, &inc.ResolvedAt)
	if err != nil {
		if err == pgxErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	inc.MonitorName = name
	u := IncidentUpdate{IncidentID: inc.ID, Status: "open", Message: msg, CreatedAt: at}
	if err := insertIncidentUpdate(ctx, db, &u); err != nil {
		return &inc, true, err
	}
	inc.Updates = []IncidentUpdate{u}
	return &inc, true, nil
}

func (s *Store) ResolveAutoIncident(ctx context.Context, monitorID int64, at time.Time) (*Incident, error) {
	var id int64
	err := s.Pool.QueryRow(ctx, `UPDATE incidents SET status='resolved', resolved_at=$2
		WHERE monitor_id=$1 AND resolved_at IS NULL AND auto_opened = TRUE
		RETURNING id`, monitorID, at).Scan(&id)
	if err != nil {
		if err == pgxErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	u := IncidentUpdate{IncidentID: id, Status: "resolved", Message: "Recovered", CreatedAt: at}
	if err := s.InsertIncidentUpdate(ctx, &u); err != nil {
		return nil, err
	}
	return s.GetIncident(ctx, id)
}

func (s *Store) AddIncidentUpdate(ctx context.Context, id int64, status, message string, at time.Time) (*Incident, error) {
	inc, err := s.GetIncident(ctx, id)
	if err != nil {
		return nil, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if inc.ResolvedAt != nil && status != "resolved" {
		return nil, fmt.Errorf("incident is already resolved")
	}
	u := IncidentUpdate{IncidentID: id, Status: status, Message: message, CreatedAt: at}
	if err := s.InsertIncidentUpdate(ctx, &u); err != nil {
		return nil, err
	}
	if status == "resolved" {
		_, err = s.Pool.Exec(ctx, `UPDATE incidents SET status='resolved', resolved_at=COALESCE(resolved_at, $2) WHERE id=$1`, id, at)
	} else {
		_, err = s.Pool.Exec(ctx, `UPDATE incidents SET status=$2 WHERE id=$1`, id, status)
	}
	if err != nil {
		return nil, err
	}
	return s.GetIncident(ctx, id)
}

func (s *Store) InsertIncidentUpdate(ctx context.Context, u *IncidentUpdate) error {
	return insertIncidentUpdate(ctx, s.Pool, u)
}

func insertIncidentUpdate(ctx context.Context, db incidentWriter, u *IncidentUpdate) error {
	return db.QueryRow(ctx, `INSERT INTO incident_updates (incident_id, status, message, created_at)
		VALUES ($1,$2,$3,$4) RETURNING id`,
		u.IncidentID, u.Status, u.Message, u.CreatedAt).Scan(&u.ID)
}

func (s *Store) listIncidentUpdates(ctx context.Context, ids []int64) (map[int64][]IncidentUpdate, error) {
	out := map[int64][]IncidentUpdate{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, incident_id, status, message, created_at
		FROM incident_updates WHERE incident_id = ANY($1) ORDER BY created_at ASC, id ASC`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var u IncidentUpdate
		if err := rows.Scan(&u.ID, &u.IncidentID, &u.Status, &u.Message, &u.CreatedAt); err != nil {
			return nil, err
		}
		out[u.IncidentID] = append(out[u.IncidentID], u)
	}
	return out, rows.Err()
}

func (s *Store) PublicIncidents(ctx context.Context, ids []int64, since time.Time, limit int) ([]PublicIncident, error) {
	if limit <= 0 {
		limit = 40
	}
	q := `SELECT i.id, i.title, i.status, i.impact, COALESCE(m.name, ''), i.opened_at, i.resolved_at
		FROM incidents i LEFT JOIN monitors m ON m.id = i.monitor_id
		WHERE i.public = TRUE AND i.opened_at >= $1`
	args := []any{since}
	n := 2
	if ids != nil {
		q += fmt.Sprintf(` AND i.monitor_id = ANY($%d)`, n)
		args = append(args, ids)
		n++
	}
	q += fmt.Sprintf(` ORDER BY i.opened_at DESC, i.id DESC LIMIT $%d`, n)
	args = append(args, limit)
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var listed []PublicIncident
	var incIDs []int64
	for rows.Next() {
		var inc PublicIncident
		if err := rows.Scan(&inc.ID, &inc.Title, &inc.Status, &inc.Impact, &inc.Monitor, &inc.OpenedAt, &inc.ResolvedAt); err != nil {
			return nil, err
		}
		listed = append(listed, inc)
		incIDs = append(incIDs, inc.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	updates, err := s.listIncidentUpdates(ctx, incIDs)
	if err != nil {
		return nil, err
	}
	out := make([]PublicIncident, 0, len(listed))
	for _, inc := range listed {
		us := updates[inc.ID]
		inc.Updates = make([]PublicIncidentUpdate, 0, len(us))
		for _, u := range us {
			inc.Updates = append(inc.Updates, PublicIncidentUpdate{
				Status:    u.Status,
				Message:   u.Message,
				CreatedAt: u.CreatedAt,
			})
		}
		out = append(out, inc)
	}
	if out == nil {
		out = []PublicIncident{}
	}
	return out, nil
}
