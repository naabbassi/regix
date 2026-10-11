// Regix is a web interface for a Docker / OCI registry.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"regix/internal/config"
	"regix/internal/inventory"
	"regix/internal/registry"
	"regix/internal/server"
	"regix/internal/web"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-version", "--version":
			fmt.Println(version)
			return
		case "healthcheck":
			// For container health checks: the image has no shell or curl to do this with.
			os.Exit(healthcheck())
		}
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "regix:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		return fmt.Errorf("log level %q is not one of debug, info, warn, error", cfg.LogLevel)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	client, err := registry.New(registry.Options{
		URL:           cfg.Registry.URL,
		CAFile:        cfg.Registry.CAFile,
		InsecureTLS:   cfg.Registry.InsecureTLS,
		UserAgent:     "regix/" + version,
		MaxConcurrent: 8,
		Timeout:       30 * time.Second,
	})
	if err != nil {
		return err
	}
	inv := inventory.New(client, inventory.Options{
		ReadOnly:      cfg.ReadOnly,
		ProtectedTags: cfg.ProtectedTags,
		CacheTTL:      time.Duration(cfg.CacheTTL),
	}, log)
	app, err := server.New(cfg, client, inv, log, version, web.Assets())
	if err != nil {
		return err
	}

	if cfg.Auth.Mode == config.AuthNone {
		log.Warn("sign-in is switched off: anyone who can reach Regix can use it", "readOnly", cfg.ReadOnly)
	}

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      3 * time.Minute, // the first listing of a large registry takes a while
		IdleTimeout:       2 * time.Minute,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	failed := make(chan error, 1)
	go func() { failed <- httpServer.ListenAndServe() }()
	log.Info("regix started", "version", version, "listen", cfg.Listen, "registry", cfg.Registry.URL,
		"authMode", cfg.Auth.Mode, "readOnly", cfg.ReadOnly)

	select {
	case err := <-failed:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func healthcheck() int {
	listen := os.Getenv("REGIX_LISTEN")
	if listen == "" {
		listen = ":8080"
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return 1
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
