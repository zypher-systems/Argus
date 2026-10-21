package main

import (
	"bufio"
	"errors"
	"fmt"
	"strings"
	// Time zones for scheduled heartbeats, even in an image without zoneinfo.
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"argus/internal/alerting"
	"argus/internal/api"
	"argus/internal/config"
	"argus/internal/probe"
	"argus/internal/secrets"
	"argus/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "api":
		runAPI()
	case "probe":
		runProbe()
	case "reset-password":
		runResetPassword(os.Args[2:])
	default:
		usage()
	}
}

// runResetPassword sets an account's password from the server's command
// line: argus reset-password <email>, with the new password on standard
// input so it is not left in the shell history or the process list.
func runResetPassword(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: argus reset-password <email>   (the new password is read from standard input)")
		os.Exit(2)
	}
	fmt.Fprint(os.Stderr, "New password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(os.Stderr, "\nno password given")
		os.Exit(1)
	}
	st, _ := newStore()
	defer st.Close()
	switch err := st.ResetPassword(context.Background(), args[0], strings.TrimRight(line, "\r\n")); {
	case errors.Is(err, store.ErrNotFound):
		fmt.Fprintf(os.Stderr, "no account with the email %s\n", args[0])
		os.Exit(1)
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "password changed for %s; every session for that account was signed out\n", args[0])
}

func usage() {
	slog.Error("usage: argus [api|probe|reset-password <email>]")
	os.Exit(2)
}

func newStore() (*store.Store, *config.Config) {
	cfg := config.Load()
	if cfg.DatabaseURL == "" {
		slog.Error("ARGUS_DB_URL is required")
		os.Exit(1)
	}
	box, err := secrets.New(cfg.SecretKey)
	if err != nil {
		slog.Error("secret key", "error", err)
		os.Exit(1)
	}
	if box == nil {
		slog.Warn("ARGUS_SECRET_KEY is not set: monitor credentials are stored unencrypted")
	}
	store.SetSecretBox(box)

	st, err := store.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	st.SetHosted(cfg.Hosted)
	return st, cfg
}

func baseContext() (context.Context, func()) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx, stop
}

func runAPI() {
	ctx, stop := baseContext()
	defer stop()

	st, cfg := newStore()
	defer st.Close()

	if err := st.EnsureDefaultOrg(ctx); err != nil {
		slog.Error("organization bootstrap failed", "error", err)
		os.Exit(1)
	}
	if cfg.Hosted {
		if err := st.SeedRegistration(ctx, true); err != nil {
			slog.Error("registration seed failed", "error", err)
			os.Exit(1)
		}
	}

	// A first start has to end with an account someone can sign in to, and
	// not one whose password is printed in the README. Stopping here says so
	// in the log; starting anyway would leave a sign-in page nobody can pass,
	// or one anybody can. Hosted installs get their accounts by registration.
	if exists, err := st.UserExists(ctx); err != nil {
		slog.Error("admin bootstrap failed", "error", err)
		os.Exit(1)
	} else if !exists && !cfg.Hosted {
		if problem := cfg.BootstrapAdminProblem(); problem != "" {
			slog.Error("no account exists yet and the first one cannot be created: " + problem + " (deploy/.env), then start Argus again")
			os.Exit(1)
		}
	}
	if err := st.EnsureAdmin(ctx, cfg.AdminEmail, cfg.AdminPassword); err != nil {
		slog.Error("admin bootstrap failed", "error", err)
		os.Exit(1)
	}

	srv := api.New(cfg, st)
	if err := srv.Run(ctx); err != nil {
		slog.Error("api server failed", "error", err)
		os.Exit(1)
	}
}

func runProbe() {
	ctx, stop := baseContext()
	defer stop()

	cfg := config.Load()
	if cfg.ProbeToken != "" {
		runRemoteProbeHealth(ctx, cfg.ProbeListen)
		if err := probe.RunRemote(ctx, cfg.APIURL, cfg.ProbeToken); err != nil {
			slog.Error("remote probe stopped", "error", err)
			os.Exit(1)
		}
		return
	}

	st, cfg := newStore()
	defer st.Close()

	runProbeHealth(ctx, st, cfg.ProbeListen)

	mailer := alerting.NewMailer(st)
	dispatcher := alerting.NewDispatcher(st, mailer)
	// Delivers queued alerts, including any a previous run left unsent.
	go dispatcher.Run(ctx)
	engine := probe.NewEngine(st, dispatcher)
	if err := engine.Run(ctx); err != nil {
		slog.Error("probe engine stopped", "error", err)
		os.Exit(1)
	}
}

func runRemoteProbeHealth(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","mode":"remote"}`))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	go func() {
		slog.Info("probe health listening", "addr", addr, "mode", "remote")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("probe health server failed", "error", err)
		}
	}()
}

func runProbeHealth(ctx context.Context, st *store.Store, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		pingCtx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := st.Pool.Ping(pingCtx); err != nil {
			http.Error(w, `{"status":"down"}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 2 * time.Second}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()
	go func() {
		slog.Info("probe health listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("probe health server failed", "error", err)
		}
	}()
}
