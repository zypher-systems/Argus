package api

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"argus/internal/store"
	"argus/internal/version"
)

func (s *Server) handleSLA(w http.ResponseWriter, r *http.Request) {
	from := store.ParseSLARange(r.URL.Query().Get("range"))
	rows, err := s.store.SLAReport(r.Context(), from)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load SLA report")
		return
	}
	if r.URL.Query().Get("format") == "csv" || r.URL.Path == "/api/sla.csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="argus-sla.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"monitor_id", "name", "slo_target", "uptime_pct", "total_checks", "failed_checks", "within_slo"})
		for _, row := range rows {
			uptime := ""
			if row.UptimePct != nil {
				uptime = strconv.FormatFloat(*row.UptimePct, 'f', 4, 64)
			}
			_ = cw.Write([]string{
				strconv.FormatInt(row.MonitorID, 10),
				row.Name,
				strconv.FormatFloat(row.SLOTarget, 'f', 3, 64),
				uptime,
				strconv.Itoa(row.TotalChecks),
				strconv.Itoa(row.FailedChecks),
				strconv.FormatBool(row.WithinSLO),
			})
		}
		cw.Flush()
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from":  from.Format(time.RFC3339),
		"rows":  rows,
		"range": r.URL.Query().Get("range"),
	})
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.store.ListAudit(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load audit log")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleInstance(w http.ResponseWriter, r *http.Request) {
	id, err := s.store.EnsureInstanceID(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load instance")
		return
	}
	writeJSON(w, http.StatusOK, store.InstanceInfo{
		InstanceID: id,
		Version:    version.Version,
	})
}

// auditRead records a read that exposes secrets. auditAfter skips reads,
// which is right for everything except handing out credentials.
func (s *Server) auditRead(r *http.Request, action string, detail map[string]any) {
	u := s.currentUser(r)
	var uid *int64
	email := ""
	if u != nil {
		uid = &u.ID
		email = u.Email
	}
	_ = s.store.InsertAudit(r.Context(), uid, email, action, r.URL.Path, nil, detail, s.clientIP(r))
}

func (s *Server) auditAfter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/ws" || r.URL.Path == "/api/probe/jobs" || r.URL.Path == "/api/probe/results" {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		if rec.status >= 400 {
			return
		}
		u := s.currentUser(r)
		var uid *int64
		email := ""
		if u != nil {
			uid = &u.ID
			email = u.Email
		}
		action := r.Method + " " + r.URL.Path
		_ = s.store.InsertAudit(r.Context(), uid, email, action, r.URL.Path, nil, map[string]any{
			"status": rec.status,
		}, s.clientIP(r))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func parseTime(raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid time")
}
