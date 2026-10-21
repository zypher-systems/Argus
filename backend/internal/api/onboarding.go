package api

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"argus/internal/probe"
	"argus/internal/store"
)

const maxQuickAdd = 50

func (s *Server) handleGetOnboarding(w http.ResponseWriter, r *http.Request) {
	o, err := s.store.GetOnboarding(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load getting started")
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) handlePutOnboarding(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dismissed bool `json:"dismissed"`
	}
	if err := decodeJSON(r, &body, 1024); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := s.store.SetOnboardingDismissed(r.Context(), body.Dismissed, time.Now().UTC()); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to save")
		return
	}
	s.handleGetOnboarding(w, r)
}

// quickMonitor turns one pasted line into a monitor request: a URL is an
// HTTP check, host:port a TCP check, and anything else is pinged.
func quickMonitor(line string) monitorRequest {
	line = strings.TrimSpace(line)
	req := monitorRequest{Target: line, IntervalSeconds: 60, Retries: 3}
	switch {
	case strings.Contains(line, "://"):
		req.Type = "http"
		req.Name = line
		if u, err := url.Parse(line); err == nil && u.Host != "" {
			req.Name = u.Host
			if p := strings.TrimSuffix(u.EscapedPath(), "/"); p != "" {
				req.Name += p
			}
		}
	default:
		if host, port, err := net.SplitHostPort(line); err == nil && host != "" {
			if _, err := strconv.Atoi(port); err == nil {
				req.Type = "tcp"
				req.Name = line
				break
			}
		}
		req.Type = "icmp"
		req.Name = line
	}
	if len(req.Name) > 80 {
		req.Name = req.Name[:80]
	}
	return req
}

type quickSkip struct {
	Line   int    `json:"line"`
	Input  string `json:"input"`
	Reason string `json:"reason"`
}

// handleQuickAdd creates a monitor for each pasted line. Every line is
// validated exactly as the full form would, and one bad line does not stop
// the rest; the answer lists what was created and why anything was not.
func (s *Server) handleQuickAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(r, &body, 32768); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	type entry struct {
		line  int
		input string
	}
	var entries []entry
	for i, raw := range strings.Split(body.Text, "\n") {
		in := strings.TrimSpace(raw)
		if in == "" || strings.HasPrefix(in, "#") {
			continue
		}
		entries = append(entries, entry{i + 1, in})
	}
	if len(entries) == 0 {
		writeErr(w, http.StatusBadRequest, "paste at least one URL or host")
		return
	}
	if len(entries) > maxQuickAdd {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("at most %d at a time", maxQuickAdd))
		return
	}

	existing, err := s.store.ListMonitors(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load monitors")
		return
	}
	watched := map[string]bool{}
	for _, m := range existing {
		watched[m.Type+"|"+m.Target] = true
	}

	created := []store.Monitor{}
	skipped := []quickSkip{}
	for _, e := range entries {
		req := quickMonitor(e.input)
		if msg := req.validate(); msg != "" {
			skipped = append(skipped, quickSkip{e.line, e.input, msg})
			continue
		}
		key := req.Type + "|" + strings.TrimSpace(req.Target)
		if watched[key] {
			skipped = append(skipped, quickSkip{e.line, e.input, "already watched"})
			continue
		}
		m := store.Monitor{
			Name: strings.TrimSpace(req.Name), Type: req.Type, Target: strings.TrimSpace(req.Target),
			IntervalSeconds: req.IntervalSeconds, Retries: req.Retries, Enabled: *req.Enabled,
			SLOTarget: req.SLOTarget, ProbeNodeIDs: []int64{}, ConfirmLocations: 1, Config: req.Config,
		}
		if err := s.store.CreateMonitor(r.Context(), &m); err != nil {
			skipped = append(skipped, quickSkip{e.line, e.input, "could not be saved"})
			continue
		}
		watched[key] = true
		s.enqueueCheckNow(r.Context(), m.ID, m.Type)
		created = append(created, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": created, "skipped": skipped})
}

// handleOnboardingStatusPage publishes a first status page listing every
// monitor, under a free slug made from the organization's name.
func (s *Server) handleOnboardingStatusPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := "Service status"
	base := "public"
	if org, err := s.store.GetOrg(ctx, max(store.OrgID(ctx), 1)); err == nil {
		if n := strings.TrimSpace(org.Name); n != "" && !strings.EqualFold(n, "default") {
			name = n + " status"
			if slug := store.Slugify(n); slug != "" {
				base = slug
			}
		}
	}
	if _, reserved := reservedSlugs[base]; reserved {
		base = "public"
	}
	slug := ""
	for i := 1; i <= 50; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		taken, err := s.store.StatusSlugTaken(ctx, candidate)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "failed to create status page")
			return
		}
		if !taken {
			slug = candidate
			break
		}
	}
	if slug == "" {
		writeErr(w, http.StatusConflict, "could not find a free address; create the page from Status pages")
		return
	}
	monitors, err := s.store.ListMonitors(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load monitors")
		return
	}
	ids := []int64{}
	for _, m := range monitors {
		if m.Type != "group" {
			ids = append(ids, m.ID)
		}
	}
	p := store.StatusPage{Name: name, Slug: slug, Published: true, MonitorIDs: ids}
	if err := s.store.CreateStatusPage(ctx, &p); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create status page")
		return
	}
	s.statusCache.invalidate()
	writeJSON(w, http.StatusCreated, p)
}

// handleSchedulePreview shows when a cron schedule would fire, so the form
// can say "next: Mon 02:00" while it is typed. An invalid schedule is a 200
// with an error, since it is an answer, not a failed request.
func (s *Server) handleSchedulePreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Schedule string `json:"schedule"`
		Timezone string `json:"timezone"`
	}
	if err := decodeJSON(r, &body, 1024); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	next, err := probe.NextRuns(body.Schedule, body.Timezone, time.Now(), 3)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"next": []time.Time{}, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"next": next, "error": ""})
}
