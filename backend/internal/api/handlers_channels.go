package api

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"argus/internal/alerting"
	"argus/internal/store"
)

type channelRequest struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Dest   string   `json:"dest"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

var allowedChannelEvents = map[string]struct{}{
	"down": {}, "recovery": {}, "slow": {}, "cert": {}, "domain": {}, "escalation": {},
	"probe_down": {}, "probe_up": {}, "resend": {},
}

func (b channelRequest) validate() (store.Channel, string) {
	var c store.Channel
	c.Name = strings.TrimSpace(b.Name)
	if c.Name == "" {
		return c, "name is required"
	}
	if len(c.Name) > 80 {
		return c, "name must be at most 80 characters"
	}
	c.Type = strings.ToLower(strings.TrimSpace(b.Type))
	if !slices.Contains(store.ChannelTypes, c.Type) {
		return c, "type must be one of " + strings.Join(store.ChannelTypes, ", ")
	}
	dest := strings.TrimSpace(b.Dest)
	if dest == "" {
		dest = strings.TrimSpace(b.URL)
	}
	switch {
	case c.Type == store.ChannelEmail:
		normalized, err := alerting.ValidEmailDest(dest)
		if err != nil {
			return c, "dest must be comma-separated email addresses"
		}
		c.Dest = normalized
	case store.ChannelIsURL(c.Type):
		url, err := alerting.ValidHookURL(dest)
		if err != nil || url == "" {
			return c, "dest must be an http or https URL"
		}
		c.Dest = url
	case c.Type == store.ChannelTelegram:
		if _, _, err := alerting.TelegramDest(dest); err != nil {
			return c, "dest must be <bot token>:<chat id>"
		}
		c.Dest = dest
	default:
		// PagerDuty routing key or Opsgenie API key, optionally "key@eu".
		if dest == "" || len(dest) > 200 || strings.ContainsAny(dest, " \t\r\n") {
			return c, "dest must be the integration key, optionally with @eu for the EU region"
		}
		c.Dest = dest
	}
	if b.Events == nil {
		c.Events = append([]string{}, store.DefaultChannelEvents...)
		return c, ""
	}
	seen := map[string]struct{}{}
	for _, ev := range b.Events {
		ev = strings.ToLower(strings.TrimSpace(ev))
		if _, ok := allowedChannelEvents[ev]; !ok {
			return c, "events must be down, recovery, slow, cert, domain, escalation, probe_down, probe_up, or resend"
		}
		if _, ok := seen[ev]; ok {
			continue
		}
		seen[ev] = struct{}{}
		c.Events = append(c.Events, ev)
	}
	if c.Events == nil {
		c.Events = []string{}
	}
	return c, ""
}

func channelIDFromURL(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "invalid channel id")
		return 0, false
	}
	return id, true
}

func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListChannels(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	// For most channel types the destination is the credential: anyone with
	// a Slack webhook URL or a PagerDuty key can post to it. Viewers can see
	// where alerts go, not the means to send them.
	if u := s.currentUser(r); u != nil && u.Role == store.RoleViewer {
		for i := range list {
			list[i].Dest = maskChannelDest(list[i].Type, list[i].Dest)
		}
	}
	writeJSON(w, http.StatusOK, list)
}

// maskChannelDest keeps enough to recognise a channel and nothing to use it.
func maskChannelDest(kind, dest string) string {
	switch {
	case kind == store.ChannelEmail:
		return dest
	case store.ChannelIsURL(kind):
		if u, err := url.Parse(dest); err == nil && u.Host != "" {
			return u.Scheme + "://" + u.Host + "/…"
		}
		return "…"
	default:
		// Keys and bot tokens: the last four characters tell two apart.
		if len(dest) <= 8 {
			return "…"
		}
		return "…" + dest[len(dest)-4:]
	}
}

func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	var body channelRequest
	if err := decodeJSON(r, &body, 16384); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	c, msg := body.validate()
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	existing, err := s.store.ListChannels(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	if len(existing) >= 50 {
		writeErr(w, http.StatusBadRequest, "at most 50 notification channels")
		return
	}
	if err := s.store.CreateChannel(r.Context(), &c); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create channel")
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := channelIDFromURL(w, r)
	if !ok {
		return
	}
	var body channelRequest
	if err := decodeJSON(r, &body, 16384); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	c, msg := body.validate()
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	c.ID = id
	if err := s.store.UpdateChannel(r.Context(), &c); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "channel not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to update channel")
		return
	}
	got, err := s.store.GetChannel(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusOK, c)
		return
	}
	writeJSON(w, http.StatusOK, got)
}

func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := channelIDFromURL(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteChannel(r.Context(), id); err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "channel not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to delete channel")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleChannelTestByID(w http.ResponseWriter, r *http.Request) {
	id, ok := channelIDFromURL(w, r)
	if !ok {
		return
	}
	ch, err := s.store.GetChannel(r.Context(), id)
	if err != nil {
		if err == store.ErrNotFound {
			writeErr(w, http.StatusNotFound, "channel not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to load channel")
		return
	}
	p := alerting.Payload{
		Monitor: store.Monitor{Name: "Argus", Type: "http", Target: "test"},
		Type:    "test",
		At:      time.Now().UTC(),
		Detail:  "Congratulations — this channel is working.",
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if ch.Type == store.ChannelEmail {
		recips := alerting.SplitRecipients(ch.Dest)
		s.dispatch.Decorate(ctx, &p)
		if err := s.mailer.SendEmail(ctx, alerting.EmailContent(p), recips); err != nil {
			writeErr(w, http.StatusBadGateway, "test failed: "+err.Error())
			return
		}
	} else if err := alerting.Deliver(ctx, *ch, p); err != nil {
		writeErr(w, http.StatusBadGateway, "test failed: "+err.Error())
		return
	}
	_ = s.store.InsertAlert(r.Context(), 0, "test", "sent", ch.Name, "")
	// Getting started asks for one delivered test; this is it.
	_ = s.store.MarkTestAlert(r.Context(), time.Now().UTC())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
