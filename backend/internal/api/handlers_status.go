package api

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"argus/internal/store"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

var reservedSlugs = map[string]struct{}{
	"api": {}, "login": {}, "logout": {}, "settings": {}, "monitors": {},
	"status": {}, "status-pages": {}, "admin": {}, "health": {},
}

type statusPageRequest struct {
	Name         string   `json:"name"`
	Slug         string   `json:"slug"`
	Headline     string   `json:"headline"`
	Branded      bool     `json:"branded"`
	Published    *bool    `json:"published"`
	MonitorIDs   *[]int64 `json:"monitor_ids"`
	CustomDomain string   `json:"custom_domain"`
}

func (b statusPageRequest) validate() (store.StatusPage, string) {
	var p store.StatusPage
	p.Name = strings.TrimSpace(b.Name)
	if p.Name == "" {
		return p, "name is required"
	}
	if len(p.Name) > 80 {
		return p, "name must be at most 80 characters"
	}
	p.Slug = strings.ToLower(strings.TrimSpace(b.Slug))
	if !slugRe.MatchString(p.Slug) {
		return p, "slug must be 1-40 lowercase letters, numbers, or hyphens"
	}
	if _, ok := reservedSlugs[p.Slug]; ok {
		return p, "slug is reserved"
	}
	p.Headline = strings.TrimSpace(b.Headline)
	if len(p.Headline) > 200 {
		return p, "headline must be at most 200 characters"
	}
	p.Branded = b.Branded
	p.Published = true
	if b.Published != nil {
		p.Published = *b.Published
	}
	if b.MonitorIDs != nil {
		if len(*b.MonitorIDs) > 100 {
			return p, "at most 100 monitors on a status page"
		}
		p.MonitorIDs = *b.MonitorIDs
	}
	domain := strings.ToLower(strings.TrimSpace(b.CustomDomain))
	if domain != "" {
		if strings.Contains(domain, "://") || strings.ContainsAny(domain, "/ ") {
			return p, "custom domain must be a hostname, without a scheme or path"
		}
		if !store.ValidCustomDomain(domain) {
			return p, "custom domain must be a valid hostname"
		}
		p.CustomDomain = domain
	}
	return p, ""
}

func statusPageIDFromURL(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid status page id")
		return 0, false
	}
	return id, true
}

func (s *Server) handleListStatusPages(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListStatusPages(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list status pages")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateStatusPage(w http.ResponseWriter, r *http.Request) {
	var body statusPageRequest
	if err := decodeJSON(r, &body, 32768); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	p, msg := body.validate()
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	existing, err := s.store.ListStatusPages(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list status pages")
		return
	}
	if len(existing) >= 20 {
		writeErr(w, http.StatusBadRequest, "at most 20 status pages")
		return
	}
	if p.MonitorIDs != nil {
		if err := s.store.ValidateMonitorIDs(r.Context(), p.MonitorIDs); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if _, err := s.store.StatusPageBySlug(r.Context(), p.Slug); err == nil {
		writeErr(w, http.StatusConflict, "slug already in use")
		return
	}
	if err := s.store.CreateStatusPage(r.Context(), &p); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create status page")
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleUpdateStatusPage(w http.ResponseWriter, r *http.Request) {
	id, ok := statusPageIDFromURL(w, r)
	if !ok {
		return
	}
	existing, err := s.store.GetStatusPage(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "status page not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load status page")
		return
	}
	var body statusPageRequest
	if err := decodeJSON(r, &body, 32768); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	p, msg := body.validate()
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	p.ID = id
	p.CreatedAt = existing.CreatedAt
	if body.MonitorIDs == nil {
		p.MonitorIDs = existing.MonitorIDs
	} else if err := s.store.ValidateMonitorIDs(r.Context(), p.MonitorIDs); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if p.Slug != existing.Slug {
		if other, err := s.store.StatusPageBySlug(r.Context(), p.Slug); err == nil && other.ID != id {
			writeErr(w, http.StatusConflict, "slug already in use")
			return
		}
	}
	if err := s.store.UpdateStatusPage(r.Context(), &p); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to update status page")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteStatusPage(w http.ResponseWriter, r *http.Request) {
	id, ok := statusPageIDFromURL(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteStatusPage(r.Context(), id); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "status page not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to delete status page")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePublicStatus(w http.ResponseWriter, r *http.Request) {
	ip := s.rateKey(r)
	if !s.statusLimit.allow(ip) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	slug := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "slug")))
	if !slugRe.MatchString(slug) {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	if cached, ok := s.statusCache.get(slug); ok {
		writeJSON(w, http.StatusOK, cached)
		return
	}
	page, err := s.store.StatusPageBySlug(r.Context(), slug)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "status page not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load status page")
		return
	}
	s.writePublicPage(w, r, page)
}

func (s *Server) handleStatusByHost(w http.ResponseWriter, r *http.Request) {
	ip := s.rateKey(r)
	if !s.statusLimit.allow(ip) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	host := store.NormalizeStatusHost(r.Host)
	if host == "" {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	if cached, ok := s.statusCache.get("host:" + host); ok {
		writeJSON(w, http.StatusOK, cached)
		return
	}
	page, err := s.store.StatusPageByHost(r.Context(), host)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "status page not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load status page")
		return
	}
	if page.CustomDomain == "" {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	s.writePublicPage(w, r, page)
}

func (s *Server) writePublicPage(w http.ResponseWriter, r *http.Request, page *store.StatusPage) {
	if !page.Published {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	pub, msg := s.publicView(r.Context(), page)
	if msg != "" {
		writeErr(w, http.StatusInternalServerError, msg)
		return
	}
	writeJSON(w, http.StatusOK, pub)
}

// publicView builds what a visitor sees of page and caches it. Everything
// that reads the cache gets the same view, so it is built in one place. msg
// is non-empty when it could not be built.
func (s *Server) publicView(ctx context.Context, page *store.StatusPage) (pub store.PublicStatus, msg string) {
	monitors, err := s.store.PublicMonitors(ctx, page.MonitorIDs)
	if err != nil {
		return pub, "failed to load monitors"
	}
	incidents, err := s.store.PublicIncidents(ctx, page.MonitorIDs, time.Now().UTC().Add(-90*24*time.Hour), 40)
	if err != nil {
		return pub, "failed to load incidents"
	}
	maintenance, err := s.store.PublicMaintenanceWindows(ctx, page.MonitorIDs, time.Now().UTC())
	if err != nil {
		return pub, "failed to load maintenance windows"
	}
	pub = store.PublicStatus{
		Name:         page.Name,
		Slug:         page.Slug,
		Headline:     page.Headline,
		Overall:      overallStatus(monitors),
		Monitors:     monitors,
		Incidents:    incidents,
		Maintenance:  maintenance,
		CustomDomain: page.CustomDomain,
	}
	if page.Branded {
		settings, err := s.store.LoadSettings(ctx)
		if err == nil {
			pub.Branding = &store.PublicBranding{
				ProductName: settings.Branding.ProductName,
				LogoURL:     settings.Branding.LogoURL,
				AccentColor: settings.Branding.AccentColor,
			}
		}
	}
	s.statusCache.put(page.Slug, pub)
	if page.CustomDomain != "" {
		s.statusCache.put("host:"+store.NormalizeStatusHost(page.CustomDomain), pub)
	}
	return pub, ""
}

func overallStatus(monitors []store.PublicMonitor) string {
	down, slow := false, false
	for _, m := range monitors {
		if m.Status == "down" {
			down = true
		}
		if m.Slow {
			slow = true
		}
	}
	if down {
		return "down"
	}
	if slow {
		return "degraded"
	}
	return "operational"
}

// publicPageFor resolves a slug to its published page and public view, using
// the same cache the JSON endpoint uses.
func (s *Server) publicPageFor(w http.ResponseWriter, r *http.Request) (store.PublicStatus, bool) {
	if !s.statusLimit.allow(s.rateKey(r)) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "too many requests")
		return store.PublicStatus{}, false
	}
	slug := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "slug")))
	if !slugRe.MatchString(slug) {
		writeErr(w, http.StatusNotFound, "status page not found")
		return store.PublicStatus{}, false
	}
	if cached, ok := s.statusCache.get(slug); ok {
		return cached, true
	}
	page, err := s.store.StatusPageBySlug(r.Context(), slug)
	if err != nil || !page.Published {
		writeErr(w, http.StatusNotFound, "status page not found")
		return store.PublicStatus{}, false
	}
	pub, msg := s.publicView(r.Context(), page)
	if msg != "" {
		writeErr(w, http.StatusInternalServerError, msg)
		return store.PublicStatus{}, false
	}
	return pub, true
}

func writeBadge(w http.ResponseWriter, svg []byte) {
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	// Short cache: a badge embedded in a README should not show yesterday's
	// status, but it also should not hammer the API.
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(svg)
}

// handleStatusBadge is an embeddable SVG of the page's overall status.
func (s *Server) handleStatusBadge(w http.ResponseWriter, r *http.Request) {
	pub, ok := s.publicPageFor(w, r)
	if !ok {
		return
	}
	label := strings.TrimSpace(r.URL.Query().Get("label"))
	if label == "" {
		label = "status"
	}
	if len(label) > 32 {
		label = label[:32]
	}
	value, colour := StatusBadge(pub.Overall)
	writeBadge(w, BadgeSVG(label, value, colour))
}

// handleUptimeBadge is an embeddable SVG of 90-day uptime across the page.
func (s *Server) handleUptimeBadge(w http.ResponseWriter, r *http.Request) {
	pub, ok := s.publicPageFor(w, r)
	if !ok {
		return
	}
	label := strings.TrimSpace(r.URL.Query().Get("label"))
	if label == "" {
		label = "uptime"
	}
	if len(label) > 32 {
		label = label[:32]
	}
	value, colour := UptimeBadge(pub.Monitors)
	writeBadge(w, BadgeSVG(label, value, colour))
}

// handleStatusFeed is the incident history as RSS.
func (s *Server) handleStatusFeed(w http.ResponseWriter, r *http.Request) {
	pub, ok := s.publicPageFor(w, r)
	if !ok {
		return
	}
	feed, err := IncidentFeed(pub, s.statusPageURL(r, pub.Slug))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to build feed")
		return
	}
	w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(feed)
}

// statusPageURL is the public address of a status page: the configured base
// URL when there is one, otherwise the host this request arrived on.
func (s *Server) statusPageURL(r *http.Request, slug string) string {
	if all, err := s.store.LoadSettings(r.Context()); err == nil {
		if base := strings.TrimRight(strings.TrimSpace(all.General.BaseURL), "/"); base != "" {
			return base + "/status/" + slug
		}
	}
	scheme := "https"
	if r.TLS == nil && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "http"
	}
	return scheme + "://" + r.Host + "/status/" + slug
}
