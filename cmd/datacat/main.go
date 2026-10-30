// Commande datacat : API REST de catalogue de jeux de données.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/clemlery/datacat/internal/config"
	"github.com/clemlery/datacat/internal/httpapi"
	"github.com/clemlery/datacat/internal/repository/postgres"
)

func main() {
	// Logger JSON dès le départ, pour que les erreurs de config soient elles aussi en JSON.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("datacat stopped", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("configuration invalide : %w", err)
	}
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns, logger)
	if err != nil {
		return fmt.Errorf("base de données : %w", err)
	}
	defer pool.Close() // exécuté après Shutdown : les requêtes en cours gardent leurs connexions

	if err := postgres.Migrate(pool, logger); err != nil {
		return fmt.Errorf("migrations : %w", err)
	}

	health := httpapi.NewHealth(pool, logger)
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewRouter(health),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	logger.Info("http server listening", "addr", cfg.HTTPAddr)

	select {
	case err := <-serveErr:
		return fmt.Errorf("serveur HTTP : %w", err)
	case <-ctx.Done():
	}
	stop() // un second signal interrompt immédiatement le processus

	logger.Info("shutdown started", "timeout", cfg.ShutdownTimeout.String())
	health.SetShuttingDown()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("arrêt gracieux : %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serveur HTTP : %w", err)
	}

	logger.Info("shutdown complete")
	return nil
}
