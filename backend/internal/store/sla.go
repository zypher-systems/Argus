package store

import (
	"context"
	"strings"
	"time"
)

func (s *Store) SLAReport(ctx context.Context, from time.Time) ([]SLARow, error) {
	return s.SLAReportRange(ctx, from, time.Time{})
}

// SLAReportRange reports on [from, to). A zero `to` means "up to now", which
// is what the dashboard asks for; the monthly email asks for a closed month.
func (s *Store) SLAReportRange(ctx context.Context, from, to time.Time) ([]SLARow, error) {
	upper := to
	if upper.IsZero() {
		upper = time.Now().UTC().Add(24 * time.Hour)
	}
	rows, err := s.Pool.Query(ctx, `SELECT m.id, m.name, m.slo_target,
		COALESCE(SUM(r.total), 0), COALESCE(SUM(r.failures), 0)
		FROM monitors m
		LEFT JOIN rollup_daily r ON r.monitor_id = m.id AND r.bucket >= $1 AND r.bucket < $3
		WHERE ($2 = 0 OR m.org_id=$2)
		GROUP BY m.id, m.name, m.slo_target
		ORDER BY m.name ASC, m.id ASC`, from, OrgID(ctx), upper)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SLARow
	for rows.Next() {
		var row SLARow
		if err := rows.Scan(&row.MonitorID, &row.Name, &row.SLOTarget, &row.TotalChecks, &row.FailedChecks); err != nil {
			return nil, err
		}
		if row.TotalChecks > 0 {
			pct := 100.0 * float64(row.TotalChecks-row.FailedChecks) / float64(row.TotalChecks)
			row.UptimePct = &pct
			row.WithinSLO = pct+1e-9 >= row.SLOTarget
		} else {
			row.WithinSLO = true
		}
		out = append(out, row)
	}
	if out == nil {
		out = []SLARow{}
	}
	return out, rows.Err()
}

func ParseSLARange(raw string) time.Time {
	days := 30
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "7d", "7":
		days = 7
	case "90d", "90":
		days = 90
	case "365d", "365", "1y":
		days = 365
	}
	return time.Now().UTC().AddDate(0, 0, -days).Truncate(24 * time.Hour)
}
