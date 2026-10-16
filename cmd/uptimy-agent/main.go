// Command uptimy-agent is a self-hosted uptime monitor with a built-in web UI
// and status page. It runs standalone, and can optionally report to Uptimy.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // heartbeat cron schedules need time zones, and minimal images lack the database

	"github.com/uptimy/agent/internal/api"
	"github.com/uptimy/agent/internal/checks"
	"github.com/uptimy/agent/internal/cloudsync"
	"github.com/uptimy/agent/internal/config"
	"github.com/uptimy/agent/internal/connect"
	"github.com/uptimy/agent/internal/delivery"
	"github.com/uptimy/agent/internal/discovery"
	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/filesync"
	"github.com/uptimy/agent/internal/kube"
	"github.com/uptimy/agent/internal/managed"
	"github.com/uptimy/agent/internal/notify"
	"github.com/uptimy/agent/internal/scheduler"
	"github.com/uptimy/agent/internal/store"
	"github.com/uptimy/agent/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version":
			fmt.Println(version)
			return
		case "healthcheck":
			// For Docker HEALTHCHECK: the distroless image has no curl.
			os.Exit(healthcheck())
		case "validate":
			// Check a monitors file in CI, without starting the agent.
			os.Exit(validate(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
		case "reset-2fa":
			// For a user, the only admin included, who lost both their
			// phone and their recovery codes.
			os.Exit(resetTwoFactor(os.Args[2:]))
		}
	}
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(cfg.DatabasePath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	generated, err := api.EnsureDefaultAdmin(ctx, st, cfg.AdminUsername, cfg.AdminPassword)
	if err != nil {
		return err
	}
	if generated != "" {
		// Shown once, like Argo CD / Jenkins: there is no well-known default.
		log.Warn("created the default admin user; sign in and choose a new password",
			"username", cfg.AdminUsername, "password", generated)
	}
	monitorsFileProblem, err := syncMonitorsFile(ctx, cfg, st, log)
	if err != nil {
		return err
	}

	kc := kube.NewInCluster()
	if kc != nil {
		log.Info("running inside Kubernetes; kubernetes monitors enabled")
	}
	hub := events.NewHub()
	sender := notify.NewSender(log)
	alerts := delivery.New(st, sender, hub, log)
	// Alerts outlive the scheduler (stopped before returning), so the last
	// status changes still go out; whatever doesn't make it stays in the
	// outbox for the next start.
	alertsCtx, stopAlerts := context.WithCancel(context.WithoutCancel(ctx))
	alertsDone := make(chan struct{})
	go func() { alerts.Run(alertsCtx, 5*time.Second); close(alertsDone) }()
	defer func() { stopAlerts(); <-alertsDone }()
	sched := scheduler.New(st, checks.New(kc), alerts, hub, log)
	if err := sched.Start(ctx); err != nil {
		return fmt.Errorf("start scheduler: %w", err)
	}
	var disc *discovery.Discoverer
	if kc != nil && cfg.KubernetesDiscovery {
		log.Info("kubernetes discovery enabled", "label", discovery.Label)
		disc = discovery.New(kc, log)
		go disc.Run(ctx, st, func(ch managed.Changes) {
			for _, id := range ch.Deleted {
				sched.Remove(id)
			}
			for _, m := range ch.Saved {
				sched.Upsert(m)
			}
			hub.Publish(events.Message{Type: "monitors"})
		}, sched.Report)
	}

	go prune(ctx, st, cfg.RetentionDays, log)

	// Uptimy Cloud sync, for a "Connect to Uptimy Cloud" connection: its
	// uploads are also the agent's check-in.
	syncer := cloudsync.New(st, func() *connect.Client {
		c := connect.NewClient(cfg.UptimyAPIURL, cfg.UptimyHeartbeatsURL)
		c.IngestURL = strings.TrimRight(cfg.UptimyIngestURL, "/")
		return c
	}, cfg.AgentName, version, log)
	go syncer.Run(ctx)
	go syncer.Watch(ctx, hub.Subscribe)

	srv := &api.Server{
		Config: cfg, Version: version, Store: st, Scheduler: sched,
		Sender: sender, Alerts: alerts, Hub: hub, Log: log, KubeAvailable: kc != nil, Discovery: disc,
		Sync: syncer, MonitorsFileProblem: monitorsFileProblem,
	}
	go srv.RunUptimyMaintenance(ctx)
	ui, err := web.Handler(cfg.UIDevServer)
	if err != nil {
		return err
	}
	if cfg.UIDevServer != "" {
		log.Info("development mode: proxying the UI to the Vite dev server", "dev_server", cfg.UIDevServer)
	}
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(ui),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: it would cut off the SSE stream.
	}
	httpServer.RegisterOnShutdown(hub.Close) // end live streams so shutdown is instant
	errc := make(chan error, 1)
	go func() {
		log.Info("uptimy-agent listening", "addr", cfg.Addr, "version", version)
		errc <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Warn("shutting down the HTTP server", "err", err)
	}
	sched.Wait()
	return nil
}

// syncMonitorsFile applies the monitors file, as far as it's valid (see
// filesync.Apply). A file with problems never stops the agent: they're
// logged and returned for the UI. Only a database error is fatal.
func syncMonitorsFile(ctx context.Context, cfg config.Config, st *store.Store, log *slog.Logger) (*filesync.Problem, error) {
	var data []byte
	var name string
	switch {
	case cfg.MonitorsYAML != "":
		data, name = []byte(cfg.MonitorsYAML), "MONITORS_YAML"
	case cfg.MonitorsFile != "":
		name = filepath.Base(cfg.MonitorsFile)
		var err error
		if data, err = os.ReadFile(cfg.MonitorsFile); err != nil {
			log.Error("can't read the monitors file; keeping the monitors it set before", "file", cfg.MonitorsFile, "err", err)
			return &filesync.Problem{File: name, Errors: []string{err.Error()}}, nil
		}
	default:
		return nil, nil
	}
	res, err := filesync.Apply(ctx, st, data)
	if err != nil {
		return nil, fmt.Errorf("sync monitors file: %w", err)
	}
	if len(res.Problems) == 0 {
		log.Info("monitors file applied", "created", res.Created, "updated", res.Updated, "deleted", res.Deleted)
		return nil, nil
	}
	p := &filesync.Problem{File: name, Applied: !res.Held}
	for _, e := range res.Problems {
		p.Errors = append(p.Errors, e.Error())
	}
	if res.Held {
		log.Error("the monitors file has an error and wasn't applied; keeping the monitors it set before", "file", name, "err", errors.Join(res.Problems...))
	} else {
		log.Error("the monitors file has invalid monitors; they were skipped, the rest applied", "file", name,
			"created", res.Created, "updated", res.Updated, "deleted", res.Deleted, "err", errors.Join(res.Problems...))
	}
	return p, nil
}

func prune(ctx context.Context, st *store.Store, days int, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if err := st.Prune(ctx, time.Now().AddDate(0, 0, -days)); err != nil && ctx.Err() == nil {
			log.Error("pruning old data", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func healthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz") //nolint:gosec // G704: always this agent on localhost
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// resetTwoFactor turns two-factor sign-in off for a user, straight in the
// database, for when no admin can sign in to do it:
//
//	uptimy-agent reset-2fa <username>
//	kubectl -n monitoring exec deploy/uptimy-agent -- uptimy-agent reset-2fa admin
func resetTwoFactor(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: uptimy-agent reset-2fa <username>")
		return 2
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	st, err := store.Open(cfg.DatabasePath())
	if err != nil {
		fmt.Fprintln(os.Stderr, "open database:", err)
		return 1
	}
	defer st.Close()
	ctx := context.Background()
	u, err := st.GetUserByUsername(ctx, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "no user %q: %v\n", args[0], err)
		return 1
	}
	if err := st.DisableTOTP(ctx, u.ID); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("two-factor sign-in is off for %s; they can sign in with their password\n", u.Username)
	return 0
}
