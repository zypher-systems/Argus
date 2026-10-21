package api

import (
	"net/http"
	"net/mail"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"argus/internal/alerting"
	"argus/internal/store"
)

func (s *Server) handleListSubscribers(w http.ResponseWriter, r *http.Request) {
	id, ok := statusPageIDFromURL(w, r)
	if !ok {
		return
	}
	if _, err := s.store.GetStatusPage(r.Context(), id); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "status page not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load status page")
		return
	}
	list, err := s.store.ListSubscribers(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list subscribers")
		return
	}
	// A webhook address is the means to send to it, as a channel's is.
	if u := s.currentUser(r); u != nil && u.Role == store.RoleViewer {
		for i := range list {
			if list[i].Kind == "webhook" {
				list[i].Dest = maskChannelDest(store.ChannelWebhook, list[i].Dest)
			}
		}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateSubscriber(w http.ResponseWriter, r *http.Request) {
	id, ok := statusPageIDFromURL(w, r)
	if !ok {
		return
	}
	if _, err := s.store.GetStatusPage(r.Context(), id); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "status page not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load status page")
		return
	}
	var body struct {
		Kind string `json:"kind"`
		Dest string `json:"dest"`
	}
	if err := decodeJSON(r, &body, 4096); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	kind := strings.ToLower(strings.TrimSpace(body.Kind))
	dest := strings.TrimSpace(body.Dest)
	switch kind {
	case "webhook":
		u, err := alerting.ValidHookURL(dest)
		if err != nil || u == "" {
			writeErr(w, http.StatusBadRequest, "webhook dest must be an http(s) url")
			return
		}
		dest = u
	case "email":
		addr, err := mail.ParseAddress(dest)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid email")
			return
		}
		dest = strings.ToLower(addr.Address)
	default:
		writeErr(w, http.StatusBadRequest, "kind must be email or webhook")
		return
	}
	sub, err := s.store.CreateSubscriber(r.Context(), id, kind, dest, true)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	sub.Token = ""
	writeJSON(w, http.StatusCreated, sub)
}

func (s *Server) handleDeleteSubscriber(w http.ResponseWriter, r *http.Request) {
	id, ok := statusPageIDFromURL(w, r)
	if !ok {
		return
	}
	sid, err := strconv.ParseInt(chi.URLParam(r, "sid"), 10, 64)
	if err != nil || sid <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid subscriber id")
		return
	}
	// The page must be this organization's, as it must be to list or add.
	// Without this a subscriber of anyone's page could be deleted by its id.
	if _, err := s.store.GetStatusPage(r.Context(), id); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "status page not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load status page")
		return
	}
	if err := s.store.DeleteSubscriber(r.Context(), id, sid); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "subscriber not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to delete subscriber")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePublicSubscribe(w http.ResponseWriter, r *http.Request) {
	ip := s.rateKey(r)
	if !s.subscribeLimit.allow(ip) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "too many requests")
		return
	}
	slug := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "slug")))
	page, err := s.store.StatusPageBySlug(r.Context(), slug)
	if err != nil || !page.Published {
		writeErr(w, http.StatusNotFound, "status page not found")
		return
	}
	var body struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(r, &body, 2048); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	addr, err := mail.ParseAddress(strings.TrimSpace(body.Email))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid email")
		return
	}
	recipient := strings.ToLower(addr.Address)
	sub, err := s.store.CreateSubscriber(r.Context(), page.ID, "email", recipient, false)
	if err != nil {
		if err.Error() == "already subscribed" {
			writeJSON(w, http.StatusOK, map[string]string{"status": "already_subscribed"})
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to subscribe")
		return
	}
	origin := s.publicOrigin(r, page)
	confirm := origin + "/subscribe/confirm?token=" + sub.Token
	htmlBody, text := alerting.RenderEmail(alerting.EmailLayout{
		Brand:   page.Name,
		Tone:    "info",
		Eyebrow: "Confirm your subscription",
		Title:   "Get incident updates from " + page.Name,
		Lead:    "Confirm this address and you will get an email when " + page.Name + " opens, updates, or resolves an incident.",
		Action:  &alerting.EmailAction{Label: "Confirm subscription", URL: confirm},
		Footer:  "If you did not ask for this, ignore it; nothing is sent until you confirm.",
	})
	email := alerting.Email{Subject: "Confirm updates from " + page.Name, HTML: htmlBody, Text: text}
	if err := s.mailer.SendEmail(r.Context(), email, []string{recipient}); err != nil {
		writeErr(w, http.StatusBadRequest, "smtp is not configured; cannot send confirmation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "confirm_sent"})
}

func (s *Server) handleSubscribeConfirm(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		writeErr(w, http.StatusBadRequest, "token is required")
		return
	}
	if _, err := s.store.ConfirmSubscriber(r.Context(), token); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "subscription not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to confirm")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "confirmed"})
}

func (s *Server) handleSubscribeUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	_ = decodeJSON(r, &body, 2048)
	token := strings.TrimSpace(body.Token)
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if token == "" {
		writeErr(w, http.StatusBadRequest, "token is required")
		return
	}
	if err := s.store.DeleteSubscriberByToken(r.Context(), token); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "subscription not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to unsubscribe")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unsubscribed"})
}

func (s *Server) publicOrigin(r *http.Request, page *store.StatusPage) string {
	settings, err := s.store.LoadSettings(r.Context())
	if err == nil {
		base := strings.TrimRight(strings.TrimSpace(settings.General.BaseURL), "/")
		if base != "" {
			return base
		}
	}
	if page != nil && page.CustomDomain != "" {
		return "https://" + page.CustomDomain
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	return proto + "://" + r.Host
}
