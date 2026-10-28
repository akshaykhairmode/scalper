// Command scalper is the localhost manual options terminal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/akshaykhairmode/scalper/internal/config"
	"github.com/akshaykhairmode/scalper/internal/logging"
	"github.com/akshaykhairmode/scalper/internal/market"
	"github.com/akshaykhairmode/scalper/internal/paper"
	"github.com/akshaykhairmode/scalper/internal/scrip"
	"github.com/akshaykhairmode/scalper/internal/store"
	"github.com/akshaykhairmode/scalper/internal/version"
	"github.com/akshaykhairmode/scalper/internal/web"
)

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "scalper: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) (err error) {
	fs := flag.NewFlagSet("scalper", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to config.yaml (required)")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Fprintf(stdout, "scalper %s go=%s\n", version.String(), runtime.Version())
		return nil
	}
	if *configPath == "" {
		return errors.New("-config is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	logHandle, err := logging.New(
		logging.Options{
			Level:         cfg.Logging.Level,
			Format:        cfg.Logging.Format,
			File:          cfg.Logging.File,
			MaxSizeMB:     cfg.Logging.MaxSizeMB,
			MaxBackups:    cfg.Logging.MaxBackups,
			RetentionDays: cfg.Logging.RetentionDays,
		},
		"version", version.Version,
		"commit", version.Commit,
		"build_date", version.BuildDate,
		"go_version", runtime.Version(),
		"pid", os.Getpid(),
	)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, logHandle.Close()) }()
	log := logHandle.Logger

	log.Info("starting scalper",
		"event", "startup",
		"config", cfg.Summary(),
		"config_path", *configPath,
		"listen", cfg.App.Listen,
		"db_path", cfg.App.DBPath)
	if warning := config.PermissionsWarning(*configPath); warning != "" {
		log.Warn(warning, "event", "config_permissions")
	}
	for _, warning := range cfg.Warnings() {
		log.Warn(warning, "event", "config_warning")
	}
	if logHandle.Degraded() {
		log.Warn("logging is degraded", "event", "logging_degraded", "error", logHandle.SinkError())
	}

	accessToken := strings.TrimSpace(os.Getenv("KOTAK_ACCESS_TOKEN"))
	hasToken := accessToken != ""
	if hasToken {
		log.Info("access token present", "event", "access_token_present")
	} else {
		log.Warn("KOTAK_ACCESS_TOKEN not set; Kotak login is disabled and trading remains paper",
			"event", "access_token_missing")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, store.Options{
		Path:   cfg.App.DBPath,
		Logger: log,
	})
	if err != nil {
		log.Error("database startup failed", "event", "startup_failed", "error", err)
		return err
	}
	defer func() { err = errors.Join(err, st.Close()) }()
	versionApplied, err := st.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	log.Info("schema ready", "event", "db_schema", "version", versionApplied, "path", st.Path())

	underlyings := make([]scrip.Underlying, 0, len(cfg.Market.Indices))
	for _, index := range cfg.Market.Indices {
		underlyings = append(underlyings, scrip.Underlying{
			Key:             index.Key,
			DisplayName:     index.DisplayName,
			ScripName:       index.ScripName,
			SpotSymbol:      index.IndexSymbol,
			ExchangeSegment: index.ExchangeSegment,
			SpotSegment:     index.IndexSegment,
		})
	}
	scripManager, err := scrip.NewManager(scrip.ManagerConfig{
		Segments:    cfg.Scrip.Segments,
		Underlyings: underlyings,
		Client:      scrip.NewClient(accessToken),
		Repository:  st,
		Logger:      log,
	})
	if err != nil {
		return fmt.Errorf("configure Scrip Master: %w", err)
	}
	if err := scripManager.Load(ctx); err != nil {
		log.Warn("Scrip Master cache load failed", "event", "scrip_load_failed", "error", err)
	}
	if hasToken {
		go func() {
			refreshCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			if err := scripManager.Refresh(refreshCtx, market.DefaultBaseURL); err != nil {
				log.Error("initial Scrip Master refresh failed", "event", "scrip_refresh_failed", "error", err)
			}
		}()
	}
	quotes := market.NewQuotesClient(accessToken, log)
	marketService, err := market.NewService(cfg, scripManager, quotes)
	if err != nil {
		return fmt.Errorf("configure live market data: %w", err)
	}
	paperService, err := paper.NewService(ctx, st, cfg.Trading)
	if err != nil {
		return fmt.Errorf("configure paper trading: %w", err)
	}
	hsmFeed := market.NewHSMFeed(cfg, scripManager, log)
	defer hsmFeed.Stop()

	srv, err := web.New(web.Options{
		Logger:       log,
		Config:       cfg,
		AccessToken:  accessToken,
		Market:       marketService,
		Paper:        paperService,
		HSM:          hsmFeed,
		DB:           st,
		LogDegraded:  logHandle.Degraded,
		TokenPresent: func() bool { return hasToken },
	})
	if err != nil {
		log.Error("web startup failed", "event", "startup_failed", "error", err)
		return err
	}
	defer srv.Close()

	listener, err := net.Listen("tcp", cfg.App.Listen)
	if err != nil {
		log.Error("listen failed", "event", "startup_failed", "listen", cfg.App.Listen, "error", err)
		return err
	}
	httpServer := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- httpServer.Serve(listener)
	}()
	log.Info("listening",
		"event", "http_listening",
		"address", listener.Addr().String())

	if cfg.Web.OpenBrowser {
		openBrowser("http://"+listener.Addr().String(), log)
	}

	select {
	case <-ctx.Done():
		log.Info("shutdown requested", "event", "shutdown_requested")
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server failed", "event", "http_server_failed", "error", err)
			return err
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown failed", "event", "shutdown_failed", "error", err)
		return err
	}
	log.Info("shutdown complete", "event", "shutdown_complete")
	return nil
}

func openBrowser(url string, log *slog.Logger) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Warn("could not open browser", "event", "browser_open_failed", "error", err)
		return
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Warn("browser command failed", "event", "browser_open_failed", "error", err)
		}
	}()
}
