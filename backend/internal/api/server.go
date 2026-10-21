package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"argus/internal/alerting"
	"argus/internal/config"
	"argus/internal/probe"
	"argus/internal/rollup"
	"argus/internal/store"
)

const sessionCookie = "argus_session"

type Server struct {
	cfg            *config.Config
	store          *store.Store
	hub            *Hub
	mailer         *alerting.Mailer
	dispatch       *alerting.Dispatcher
	applier        *probe.Applier
	http           *http.Server
	loginLimit     *rateLimiter
	statusLimit    *rateLimiter
	subscribeLimit *rateLimiter
	accountGuard   *accountGuard
	testRunLimit   *rateLimiter
	statusCache    *statusCache
}

func New(cfg *config.Config, st *store.Store) *Server {
	mailer := alerting.NewMailer(st)
	s := &Server{
		cfg:            cfg,
		store:          st,
		hub:            NewHub(),
		mailer:         mailer,
		dispatch:       alerting.NewDispatcher(st, mailer),
		loginLimit:     newRateLimiter(15*time.Minute, 5),
		statusLimit:    newRateLimiter(time.Minute, 30),
		subscribeLimit: newRateLimiter(15*time.Minute, 5),
		accountGuard:   newAccountGuard(15*time.Minute, 20, 30*24*time.Hour),
		testRunLimit:   newRateLimiter(time.Minute, 10),
		statusCache:    newStatusCache(15 * time.Second),
	}
	s.http = &http.Server{
		Addr:           cfg.ListenAddr,
		Handler:        s.Handler(),
		ReadTimeout:    15 * time.Second,
		WriteTimeout:   120 * time.Second,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 16384,
	}
	s.applier = probe.NewApplier(st, s.dispatch)
	return s
}

func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Get("/metrics", s.handleMetrics)
		r.Get("/openapi.json", s.handleOpenAPI)
		r.Get("/branding", s.handlePublicBranding)
		r.Get("/status/{slug}", s.handlePublicStatus)
		r.Get("/status/{slug}/badge.svg", s.handleStatusBadge)
		r.Get("/status/{slug}/uptime.svg", s.handleUptimeBadge)
		r.Get("/status/{slug}/feed.xml", s.handleStatusFeed)
		r.Get("/status-by-host", s.handleStatusByHost)
		r.Post("/status/{slug}/subscribe", s.handlePublicSubscribe)
		r.Get("/subscribe/confirm", s.handleSubscribeConfirm)
		r.Post("/subscribe/unsubscribe", s.handleSubscribeUnsubscribe)
		r.Get("/push/{token}", s.handlePush)
		r.Post("/push/{token}", s.handlePush)
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/logout", s.handleLogout)
		r.Post("/auth/register", s.handleRegister)
		r.Get("/auth/hosted", s.handleHostedStatus)
		r.Get("/auth/me", s.handleMe)
		r.Get("/auth/oidc", s.handleOIDCStatus)
		r.Get("/auth/oidc/start", s.handleOIDCStart)
		r.Get("/auth/oidc/callback", s.handleOIDCCallback)
		r.Get("/probe/jobs", s.handleProbeJobs)
		r.Post("/probe/results", s.handleProbeResults)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Use(s.auditAfter)
			r.Post("/auth/password", s.handleChangePassword)

			r.Get("/instance", s.handleInstance)
			r.Get("/org", s.handleGetOrg)
			r.Put("/org", s.handleUpdateOrg)
			r.Post("/reports/test", s.handleReportTest)
			r.Get("/sla", s.handleSLA)
			r.Get("/sla.csv", s.handleSLA)
			r.Get("/audit", s.handleAudit)

			r.Get("/probe-nodes", s.handleListProbeNodes)
			r.Post("/probe-nodes", s.handleCreateProbeNode)
			r.Delete("/probe-nodes/{id}", s.handleDeleteProbeNode)

			r.Group(func(r chi.Router) {
				r.Use(s.requireAdmin)
				r.Get("/users", s.handleListUsers)
				r.Post("/users", s.handleCreateUser)
				r.Put("/users/{id}", s.handleUpdateUser)
				r.Delete("/users/{id}", s.handleDeleteUser)
				// Exports carry every stored credential in clear.
				r.Get("/export", s.handleExport)
				r.Post("/export", s.handleExportEncrypted)
			})

			r.Get("/monitors", s.handleListMonitors)
			r.Get("/monitors/series", s.handleMonitorSeries)
			r.Post("/monitors", s.handleCreateMonitor)
			r.Post("/monitors/bulk", s.handleBulkMonitors)
			r.Post("/monitors/test-run", s.handleTestRun)
			r.Post("/monitors/quick", s.handleQuickAdd)
			r.Post("/schedule/preview", s.handleSchedulePreview)
			r.Get("/onboarding", s.handleGetOnboarding)
			r.Put("/onboarding", s.handlePutOnboarding)
			r.Post("/onboarding/status-page", s.handleOnboardingStatusPage)
			r.Get("/monitors/{id}", s.handleGetMonitor)
			r.Put("/monitors/{id}", s.handleUpdateMonitor)
			r.Delete("/monitors/{id}", s.handleDeleteMonitor)
			r.Post("/monitors/{id}/pause", s.handlePauseMonitor)
			r.Post("/monitors/{id}/check", s.handleCheckNow)
			r.Post("/monitors/{id}/mute", s.handleMuteMonitor)
			r.Post("/monitors/{id}/ack", s.handleAckMonitor)
			r.Post("/monitors/{id}/clone", s.handleCloneMonitor)
			r.Get("/monitors/{id}/results", s.handleResults)
			r.Get("/monitors/{id}/events", s.handleEvents)
			r.Get("/monitors/{id}/alerts", s.handleMonitorAlerts)
			r.Get("/monitors/{id}/percentiles", s.handleMonitorPercentiles)

			r.Get("/realms", s.handleListRealms)
			r.Post("/realms", s.handleCreateRealm)
			r.Put("/realms/{id}", s.handleUpdateRealm)
			r.Delete("/realms/{id}", s.handleDeleteRealm)

			r.Post("/import", s.handleImport)

			r.Get("/tokens", s.handleListTokens)
			r.Post("/tokens", s.handleCreateToken)
			r.Delete("/tokens/{id}", s.handleDeleteToken)

			r.Get("/settings", s.handleGetSettings)
			r.Put("/settings", s.handlePutSettings)
			r.Post("/settings/smtp-test", s.handleSMTPTest)

			r.Get("/channels", s.handleListChannels)
			r.Post("/channels", s.handleCreateChannel)
			r.Put("/channels/{id}", s.handleUpdateChannel)
			r.Delete("/channels/{id}", s.handleDeleteChannel)
			r.Post("/channels/{id}/test", s.handleChannelTestByID)

			r.Get("/maintenance-windows", s.handleListMaintenance)
			r.Post("/maintenance-windows", s.handleCreateMaintenance)
			r.Delete("/maintenance-windows/{id}", s.handleDeleteMaintenance)

			r.Get("/status-pages", s.handleListStatusPages)
			r.Post("/status-pages", s.handleCreateStatusPage)
			r.Put("/status-pages/{id}", s.handleUpdateStatusPage)
			r.Delete("/status-pages/{id}", s.handleDeleteStatusPage)
			r.Get("/status-pages/{id}/subscribers", s.handleListSubscribers)
			r.Post("/status-pages/{id}/subscribers", s.handleCreateSubscriber)
			r.Delete("/status-pages/{id}/subscribers/{sid}", s.handleDeleteSubscriber)

			r.Get("/incidents", s.handleListIncidents)
			r.Post("/incidents", s.handleCreateIncident)
			r.Get("/incidents/{id}", s.handleGetIncident)
			r.Post("/incidents/{id}/updates", s.handleIncidentUpdate)

			r.Get("/ws", s.handleWS)
		})
	})

	return r
}

func (s *Server) Run(ctx context.Context) error {
	go s.hub.Run(ctx, s.store)
	go rollup.New(s.store).Run(ctx)
	go s.watchProbe(ctx)
	go s.reportLoop(ctx)
	go s.dispatch.Run(ctx)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", s.cfg.ListenAddr)
		errCh <- s.http.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
