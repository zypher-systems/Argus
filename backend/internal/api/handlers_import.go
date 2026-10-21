package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"argus/internal/store"
)

type argusExport struct {
	Format     string                   `json:"format"`
	Version    string                   `json:"version"`
	ExportedAt string                   `json:"exported_at"`
	Monitors   []store.MonitorWithState `json:"monitors"`
	Channels   []store.Channel          `json:"channels"`
}

func (s *Server) buildExport(r *http.Request) (argusExport, error) {
	monitors, err := s.store.ListMonitors(r.Context())
	if err != nil {
		return argusExport{}, err
	}
	channels, err := s.store.ListChannels(r.Context())
	if err != nil {
		return argusExport{}, err
	}
	return argusExport{
		Format:     "argus",
		Version:    "1.2.0",
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Monitors:   monitors,
		Channels:   channels,
	}, nil
}

// handleExport returns a plain export. It holds every credential Argus
// stores, so it is admin-only and recorded in the audit log even though it
// is a read.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	exp, err := s.buildExport(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to export")
		return
	}
	s.auditRead(r, "GET /api/export", map[string]any{"encrypted": false, "monitors": len(exp.Monitors)})
	writeJSON(w, http.StatusOK, exp)
}

// handleExportEncrypted seals the export with a passphrase. The request is a
// POST so the passphrase travels in the body, not a URL that ends up in logs.
func (s *Server) handleExportEncrypted(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Passphrase string `json:"passphrase"`
	}
	if err := decodeJSON(r, &body, 4096); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len([]rune(body.Passphrase)) < minExportPassphrase {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("passphrase must be at least %d characters", minExportPassphrase))
		return
	}
	exp, err := s.buildExport(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to export")
		return
	}
	plain, err := json.Marshal(exp)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to export")
		return
	}
	sealed, err := sealExport(plain, body.Passphrase)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to encrypt export")
		return
	}
	writeJSON(w, http.StatusOK, sealed)
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	var envelope struct {
		Format     string          `json:"format"`
		Data       json.RawMessage `json:"data"`
		Passphrase string          `json:"passphrase"`
	}
	payload := raw
	if json.Unmarshal(raw, &envelope) == nil && len(envelope.Data) > 0 {
		payload = envelope.Data
	}
	if isEncryptedExport(payload) {
		if envelope.Passphrase == "" {
			writeErr(w, http.StatusBadRequest, "this export is encrypted; enter its passphrase")
			return
		}
		plain, err := openExport(payload, envelope.Passphrase)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		payload, envelope.Format = plain, "argus"
	}
	format := strings.ToLower(strings.TrimSpace(envelope.Format))
	if format == "" {
		if strings.Contains(string(raw), `"monitorList"`) {
			format = "kuma"
		} else {
			format = "argus"
		}
	}

	var imported int
	var skipped []importSkip
	switch format {
	case "kuma":
		imported, skipped, err = s.importKuma(r, payload)
	case "argus":
		imported, skipped, err = s.importArgus(r, payload)
	default:
		writeErr(w, http.StatusBadRequest, "format must be argus or kuma")
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if skipped == nil {
		skipped = []importSkip{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported, "skipped": skipped})
}

func (s *Server) importArgus(r *http.Request, raw []byte) (int, []importSkip, error) {
	var dump argusExport
	if err := json.Unmarshal(raw, &dump); err != nil {
		return 0, nil, err
	}
	idMap := map[int64]int64{}
	skipped := []importSkip{}
	// Groups first so parent_id can resolve.
	monitors := append([]store.MonitorWithState{}, dump.Monitors...)
	sort.SliceStable(monitors, func(i, j int) bool {
		gi, gj := monitors[i].Type == "group", monitors[j].Type == "group"
		if gi == gj {
			return false
		}
		return gi
	})
	imported := 0
	for _, src := range monitors {
		m := src.Monitor
		oldID := m.ID
		m.ID = 0
		if m.ParentID != nil {
			if nid, ok := idMap[*m.ParentID]; ok {
				m.ParentID = &nid
			} else {
				m.ParentID = nil
			}
		}
		if m.DependsOn != nil {
			if nid, ok := idMap[*m.DependsOn]; ok {
				m.DependsOn = &nid
			} else {
				m.DependsOn = nil
			}
		}
		m.ChannelIDs = nil
		// Probe nodes are another install's ids. Keep them only when they
		// name nodes here; otherwise the monitor is checked from this server.
		if s.applyProbeNodes(r, &m) != nil {
			m.ProbeNodeIDs = nil
		}
		if n := len(store.Locations(m)); m.ConfirmLocations < 1 || m.ConfirmLocations > n {
			m.ConfirmLocations = min(max(m.ConfirmLocations, 1), n)
		}
		// An export can be edited by hand, so a monitor from one passes the
		// same checks as one typed into the form: interval and retry limits,
		// allowed methods and headers, read-only database commands.
		req := monitorRequest{
			Name: m.Name, Type: m.Type, Target: m.Target,
			IntervalSeconds: m.IntervalSeconds, Retries: m.Retries,
			EscalateMinutes: m.EscalateMinutes, EscalateRecipients: m.EscalateRecipients,
			Enabled: &m.Enabled, Tags: m.Tags, LatencyThresholdMs: m.LatencyThresholdMs,
			ResendMinutes: m.ResendMinutes, SLOTarget: m.SLOTarget,
			ProbeNodeIDs: m.ProbeNodeIDs, ConfirmLocations: m.ConfirmLocations, Config: m.Config,
		}
		if msg := req.validate(); msg != "" {
			skipped = append(skipped, importSkip{Name: m.Name, Reason: msg})
			continue
		}
		m.Name, m.Type, m.Target = strings.TrimSpace(req.Name), req.Type, strings.TrimSpace(req.Target)
		m.IntervalSeconds, m.Retries = req.IntervalSeconds, req.Retries
		m.EscalateMinutes, m.EscalateRecipients = req.EscalateMinutes, strings.TrimSpace(req.EscalateRecipients)
		m.ResendMinutes, m.LatencyThresholdMs, m.SLOTarget = req.ResendMinutes, req.LatencyThresholdMs, req.SLOTarget
		m.ProbeNodeIDs, m.ConfirmLocations, m.Config = req.ProbeNodeIDs, req.ConfirmLocations, req.Config
		if m.Type == "push" {
			_ = ensurePushToken(&m.Config)
		}
		if err := s.store.ValidateMonitorRefs(r.Context(), 0, m.ParentID, m.DependsOn); err != nil {
			skipped = append(skipped, importSkip{Name: m.Name, Reason: err.Error()})
			continue
		}
		if err := s.store.CreateMonitor(r.Context(), &m); err != nil {
			skipped = append(skipped, importSkip{Name: m.Name, Reason: err.Error()})
			continue
		}
		idMap[oldID] = m.ID
		imported++
	}
	return imported, skipped, nil
}

func (s *Server) importKuma(r *http.Request, raw []byte) (int, []importSkip, error) {
	var dump kumaBackup
	if err := json.Unmarshal(raw, &dump); err != nil {
		return 0, nil, err
	}
	sort.SliceStable(dump.MonitorList, func(i, j int) bool {
		gi, gj := dump.MonitorList[i].Type == "group", dump.MonitorList[j].Type == "group"
		if gi == gj {
			return false
		}
		return gi
	})
	idMap := map[int64]int64{}
	skipped := []importSkip{}
	imported := 0
	for _, km := range dump.MonitorList {
		m, oldID, err := mapKumaMonitor(km)
		if err != nil {
			skipped = append(skipped, importSkip{Name: km.Name, Reason: err.Error()})
			continue
		}
		req := monitorRequest{
			Name:            m.Name,
			Type:            m.Type,
			Target:          m.Target,
			IntervalSeconds: m.IntervalSeconds,
			Retries:         m.Retries,
			Enabled:         &m.Enabled,
			Tags:            m.Tags,
			Config:          m.Config,
		}
		if msg := req.validate(); msg != "" {
			skipped = append(skipped, importSkip{Name: m.Name, Reason: msg})
			continue
		}
		m.Name = strings.TrimSpace(req.Name)
		m.Type = req.Type
		m.Target = strings.TrimSpace(req.Target)
		m.IntervalSeconds = req.IntervalSeconds
		m.Retries = req.Retries
		m.Config = req.Config
		if parent := kumaParentID(km.Parent); parent != nil {
			if nid, ok := idMap[*parent]; ok {
				m.ParentID = &nid
			}
		}
		if m.Type == "push" {
			_ = ensurePushToken(&m.Config)
		}
		if err := s.store.CreateMonitor(r.Context(), &m); err != nil {
			skipped = append(skipped, importSkip{Name: m.Name, Reason: err.Error()})
			continue
		}
		if oldID > 0 {
			idMap[oldID] = m.ID
		}
		imported++
	}
	return imported, skipped, nil
}
