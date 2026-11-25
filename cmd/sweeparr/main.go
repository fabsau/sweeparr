package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fabsau/sweeparr"
	"github.com/fabsau/sweeparr/internal/api"
	"github.com/fabsau/sweeparr/internal/auth"
	"github.com/fabsau/sweeparr/internal/config"
	"github.com/fabsau/sweeparr/internal/db"
	"github.com/fabsau/sweeparr/internal/engine"
	"github.com/fabsau/sweeparr/internal/version"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "run HTTP healthcheck against local server and exit")
	validateConfig := flag.Bool("validate-config", false, "validate YAML/environment configuration and exit")
	showVersion := flag.Bool("version", false, "print Sweeparr version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("sweeparr v%s (%s)\n", version.Clean(version.Version), version.Commit)
		os.Exit(0)
	}
	if *healthcheck {
		port := os.Getenv("SWEEPARR_PORT")
		if port == "" {
			port = "8080"
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	env, err := config.LoadEnv()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}
	if *validateConfig {
		slog.Info("configuration valid",
			"config_managed", env.ConfigManaged,
			"config_file", env.ConfigFile,
			"servers", len(env.Servers),
			"oidc_enabled", env.OIDCEnabled(),
		)
		os.Exit(0)
	}
	config.ConfigureLogger(env.LogLevel, env.LogFormat)

	if err := dropPrivilegesIfConfigured(env.StateDir); err != nil {
		slog.Error("failed to switch process user/group", "error", err)
		os.Exit(1)
	}

	slog.Info("sweeparr starting",
		"version", version.Clean(version.Version),
		"commit", version.Commit,
		"uid", os.Getuid(),
		"gid", os.Getgid(),
		"port", env.Port,
		"state_dir", env.StateDir,
		"log_level", env.LogLevel,
		"log_format", env.LogFormat,
		"plex_url", env.PlexURL,
		"plex_token_source", env.PlexTokenSource,
		"plex_ssl_verify", env.PlexSSLVerify,
		"oidc_enabled", env.OIDCEnabled(),
		"rp_id", env.RPID,
	)

	database, err := db.Open(filepath.Join(env.StateDir, "sweeparr.db"))
	if err != nil {
		slog.Error("failed to open sqlite database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	authSvc, err := auth.NewService(database, env)
	if err != nil {
		slog.Error("failed to initialize auth service", "error", err)
		os.Exit(1)
	}
	if env.AdminUser != "" && env.AdminPassword != "" {
		if needs, _ := authSvc.NeedsSetup(); needs {
			if _, _, err := authSvc.SetupAdmin(env.AdminUser, env.AdminPassword, env.AdminUser); err != nil {
				slog.Warn("bootstrap admin setup skipped", "error", err)
			} else {
				slog.Info("bootstrapped initial admin user from environment", "username", env.AdminUser)
			}
		}
	}

	mgr, err := engine.NewManager(database, env, nil)
	if err != nil {
		slog.Error("failed to initialize engine manager", "error", err)
		os.Exit(1)
	}
	mgr.StartAll()
	defer mgr.StopAll()

	srv := &http.Server{
		Addr:              ":" + env.Port,
		Handler:           api.NewRouter(database, authSvc, mgr, sweeparr.WebDistFS()),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		slog.Info("http server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server terminated", "error", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	s := <-sig
	slog.Info("shutting down sweeparr", "signal", s.String())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
