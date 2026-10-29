// Package postgres contient l'accès PostgreSQL (pgx) : pool, migrations, repositories.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Paramètres du backoff de connexion (SPEC §7.1).
const (
	connectBudget    = 30 * time.Second
	connectFirstWait = 250 * time.Millisecond
	connectMaxWait   = 4 * time.Second
	pingTimeout      = 2 * time.Second
)

// Connect crée le pool puis le pingue avec un backoff exponentiel jusqu'à
// ce que la base réponde, que le budget soit épuisé ou que ctx soit annulé.
func Connect(ctx context.Context, databaseURL string, maxConns int32, logger *slog.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("DSN PostgreSQL invalide")
	}
	poolCfg.MaxConns = maxConns

	// NewWithConfig n'ouvre aucune connexion : le premier Ping le fait.
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("création du pool : %w", err)
	}

	deadline := time.Now().Add(connectBudget)
	wait := connectFirstWait
	for attempt := 1; ; attempt++ {
		pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
		err = pool.Ping(pingCtx)
		cancel()
		if err == nil {
			logger.Info("database connected", "attempt", attempt)
			return pool, nil
		}
		if ctx.Err() != nil {
			pool.Close()
			return nil, fmt.Errorf("connexion interrompue : %w", ctx.Err())
		}
		if time.Now().Add(wait).After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("base injoignable après %s (%d tentatives) : %w", connectBudget, attempt, err)
		}

		logger.Warn("database not ready, retrying", "attempt", attempt, "retry_in", wait.String(), "err", err)
		select {
		case <-ctx.Done():
			pool.Close()
			return nil, fmt.Errorf("connexion interrompue : %w", ctx.Err())
		case <-time.After(wait):
		}
		wait = min(wait*2, connectMaxWait)
	}
}
