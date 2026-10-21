package store

import (
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

type StatusPage struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	Headline     string    `json:"headline"`
	Branded      bool      `json:"branded"`
	Published    bool      `json:"published"`
	MonitorIDs   []int64   `json:"monitor_ids"`
	CustomDomain string    `json:"custom_domain"`
	OrgID        int64     `json:"org_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type PublicDay struct {
	Day    string `json:"day"`
	Status string `json:"status"`
}

type PublicMonitor struct {
	Name      string      `json:"name"`
	Type      string      `json:"type"`
	Status    string      `json:"status"`
	Slow      bool        `json:"slow"`
	Uptime90d *float64    `json:"uptime_90d"`
	Days      []PublicDay `json:"days"`
}

type PublicIncidentUpdate struct {
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

type PublicIncident struct {
	ID         int64                  `json:"id"`
	Title      string                 `json:"title"`
	Status     string                 `json:"status"`
	Impact     string                 `json:"impact"`
	Monitor    string                 `json:"monitor"`
	OpenedAt   time.Time              `json:"opened_at"`
	ResolvedAt *time.Time             `json:"resolved_at"`
	Updates    []PublicIncidentUpdate `json:"updates"`
}

type PublicBranding struct {
	ProductName string `json:"product_name"`
	LogoURL     string `json:"logo_url"`
	AccentColor string `json:"accent_color"`
}

type PublicStatus struct {
	Name         string              `json:"name"`
	Slug         string              `json:"slug"`
	Headline     string              `json:"headline"`
	Overall      string              `json:"overall"`
	CustomDomain string              `json:"custom_domain,omitempty"`
	Branding     *PublicBranding     `json:"branding,omitempty"`
	Monitors     []PublicMonitor     `json:"monitors"`
	Incidents    []PublicIncident    `json:"incidents"`
	Maintenance  []PublicMaintenance `json:"maintenance,omitempty"`
}

func (s *Store) ListStatusPages(ctx context.Context) ([]StatusPage, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, slug, headline, branded, published, monitor_ids, COALESCE(custom_domain, ''), created_at, org_id
		FROM status_pages WHERE ($1 = 0 OR org_id=$1) ORDER BY name ASC, id ASC`, OrgID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatusPage
	for rows.Next() {
		var p StatusPage
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.Headline, &p.Branded, &p.Published, &p.MonitorIDs, &p.CustomDomain, &p.CreatedAt, &p.OrgID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if out == nil {
		out = []StatusPage{}
	}
	return out, rows.Err()
}

func (s *Store) GetStatusPage(ctx context.Context, id int64) (*StatusPage, error) {
	var p StatusPage
	err := s.Pool.QueryRow(ctx, `SELECT id, name, slug, headline, branded, published, monitor_ids, COALESCE(custom_domain, ''), created_at, org_id
		FROM status_pages WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, id, OrgID(ctx)).Scan(&p.ID, &p.Name, &p.Slug, &p.Headline, &p.Branded, &p.Published, &p.MonitorIDs, &p.CustomDomain, &p.CreatedAt, &p.OrgID)
	if err != nil {
		if err == pgxErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (s *Store) StatusPageBySlug(ctx context.Context, slug string) (*StatusPage, error) {
	var p StatusPage
	err := s.Pool.QueryRow(ctx, `SELECT id, name, slug, headline, branded, published, monitor_ids, COALESCE(custom_domain, ''), created_at, org_id
		FROM status_pages WHERE slug=$1`, slug).Scan(&p.ID, &p.Name, &p.Slug, &p.Headline, &p.Branded, &p.Published, &p.MonitorIDs, &p.CustomDomain, &p.CreatedAt, &p.OrgID)
	if err != nil {
		if err == pgxErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (s *Store) StatusPageByHost(ctx context.Context, host string) (*StatusPage, error) {
	host = NormalizeStatusHost(host)
	if host == "" {
		return nil, ErrNotFound
	}
	var p StatusPage
	err := s.Pool.QueryRow(ctx, `SELECT id, name, slug, headline, branded, published, monitor_ids, COALESCE(custom_domain, ''), created_at, org_id
		FROM status_pages WHERE published = TRUE AND lower(custom_domain) = $1`, host).Scan(
		&p.ID, &p.Name, &p.Slug, &p.Headline, &p.Branded, &p.Published, &p.MonitorIDs, &p.CustomDomain, &p.CreatedAt, &p.OrgID)
	if err != nil {
		if err == pgxErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (s *Store) CreateStatusPage(ctx context.Context, p *StatusPage) error {
	domain := nullIfEmpty(p.CustomDomain)
	return s.Pool.QueryRow(ctx, `INSERT INTO status_pages (name, slug, headline, branded, published, monitor_ids, custom_domain, org_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id, created_at`,
		p.Name, p.Slug, p.Headline, p.Branded, p.Published, p.MonitorIDs, domain, writeOrg(ctx)).Scan(&p.ID, &p.CreatedAt)
}

func (s *Store) UpdateStatusPage(ctx context.Context, p *StatusPage) error {
	domain := nullIfEmpty(p.CustomDomain)
	tag, err := s.Pool.Exec(ctx, `UPDATE status_pages SET name=$1, slug=$2, headline=$3, branded=$4, published=$5, monitor_ids=$6, custom_domain=$7
		WHERE id=$8 AND ($9 = 0 OR org_id=$9)`, p.Name, p.Slug, p.Headline, p.Branded, p.Published, p.MonitorIDs, domain, p.ID, OrgID(ctx))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteStatusPage(ctx context.Context, id int64) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM status_pages WHERE id=$1 AND ($2 = 0 OR org_id=$2)`, id, OrgID(ctx))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ValidateMonitorIDs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	seen := map[int64]struct{}{}
	for _, id := range ids {
		if id <= 0 {
			return fmt.Errorf("invalid monitor id")
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("duplicate monitor id")
		}
		seen[id] = struct{}{}
	}
	var n int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM monitors WHERE id = ANY($1) AND ($2 = 0 OR org_id=$2)`, ids, OrgID(ctx)).Scan(&n); err != nil {
		return err
	}
	if n != len(ids) {
		return fmt.Errorf("unknown monitor")
	}
	return nil
}

func (s *Store) PublicMonitors(ctx context.Context, ids []int64) ([]PublicMonitor, error) {
	q := `SELECT m.id, m.name, m.type, COALESCE(s.status, 'pending'), s.slow_since IS NOT NULL AND COALESCE(s.status,'') = 'up', m.created_at
		FROM monitors m LEFT JOIN monitor_state s ON s.monitor_id = m.id
		WHERE m.enabled = TRUE`
	args := []any{}
	if ids != nil {
		q += ` AND m.id = ANY($1)`
		args = append(args, ids)
	}
	q += ` ORDER BY m.name ASC`
	rows, err := s.Pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type row struct {
		id      int64
		created time.Time
		m       PublicMonitor
	}
	var listed []row
	var monitorIDs []int64
	created := map[int64]time.Time{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.m.Name, &r.m.Type, &r.m.Status, &r.m.Slow, &r.created); err != nil {
			return nil, err
		}
		listed = append(listed, r)
		monitorIDs = append(monitorIDs, r.id)
		created[r.id] = r.created
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	days, uptime, err := s.statusDayBars(ctx, monitorIDs, created)
	if err != nil {
		return nil, err
	}
	out := make([]PublicMonitor, 0, len(listed))
	for _, r := range listed {
		r.m.Days = days[r.id]
		if r.m.Days == nil {
			r.m.Days = []PublicDay{}
		}
		if v, ok := uptime[r.id]; ok {
			r.m.Uptime90d = &v
		}
		out = append(out, r.m)
	}
	if out == nil {
		out = []PublicMonitor{}
	}
	return out, nil
}

// AdaptiveHistoryWindow returns the first day and bar count for a public
// history strip. Young monitors do not get 90 empty slots.
func AdaptiveHistoryWindow(created, now time.Time) (from time.Time, days int) {
	today := now.UTC().Truncate(24 * time.Hour)
	if created.IsZero() {
		created = now
	}
	start := created.UTC().Truncate(24 * time.Hour)
	earliest := today.AddDate(0, 0, -89)
	if start.Before(earliest) {
		start = earliest
	}
	if start.After(today) {
		start = today
	}
	days = int(today.Sub(start)/(24*time.Hour)) + 1
	if days < 1 {
		days = 1
	}
	return start, days
}

func (s *Store) statusDayBars(ctx context.Context, ids []int64, created map[int64]time.Time) (map[int64][]PublicDay, map[int64]float64, error) {
	daysOut := map[int64][]PublicDay{}
	uptime := map[int64]float64{}
	if len(ids) == 0 {
		return daysOut, uptime, nil
	}
	now := time.Now().UTC()
	fromBy := map[int64]time.Time{}
	nBy := map[int64]int{}
	minFrom := now.UTC().Truncate(24 * time.Hour)
	for _, id := range ids {
		from, n := AdaptiveHistoryWindow(created[id], now)
		fromBy[id] = from
		nBy[id] = n
		if from.Before(minFrom) {
			minFrom = from
		}
	}
	rows, err := s.Pool.Query(ctx, `SELECT monitor_id, bucket::date, total, failures
		FROM rollup_daily WHERE monitor_id = ANY($1) AND bucket >= $2 ORDER BY bucket ASC`, ids, minFrom)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	type cell struct {
		total, failures int
	}
	byMon := map[int64]map[string]cell{}
	okSum := map[int64]int{}
	allSum := map[int64]int{}
	for rows.Next() {
		var id int64
		var day time.Time
		var total, failures int
		if err := rows.Scan(&id, &day, &total, &failures); err != nil {
			return nil, nil, err
		}
		key := day.UTC().Format("2006-01-02")
		if byMon[id] == nil {
			byMon[id] = map[string]cell{}
		}
		byMon[id][key] = cell{total, failures}
		okSum[id] += total - failures
		allSum[id] += total
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	for _, id := range ids {
		from := fromBy[id]
		n := nBy[id]
		bar := make([]PublicDay, 0, n)
		for i := 0; i < n; i++ {
			d := from.AddDate(0, 0, i)
			key := d.Format("2006-01-02")
			st := ""
			if c, ok := byMon[id][key]; ok && c.total > 0 {
				if c.failures > 0 {
					st = "down"
				} else {
					st = "up"
				}
			}
			bar = append(bar, PublicDay{Day: key, Status: st})
		}
		daysOut[id] = bar
		if allSum[id] > 0 {
			uptime[id] = 100.0 * float64(okSum[id]) / float64(allSum[id])
		}
	}
	return daysOut, uptime, nil
}

func NormalizeStatusHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.TrimSuffix(host, ".")
}

func nullIfEmpty(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.ToLower(s)
}

var customDomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

func ValidCustomDomain(host string) bool {
	host = NormalizeStatusHost(host)
	if host == "" || len(host) > 253 {
		return false
	}
	return customDomainRe.MatchString(host)
}

// StatusSlugTaken reports whether any organization already uses slug.
func (s *Store) StatusSlugTaken(ctx context.Context, slug string) (bool, error) {
	var taken bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM status_pages WHERE slug=$1)`, slug).Scan(&taken)
	return taken, err
}
