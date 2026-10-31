// Package config lit la configuration de l'API depuis les variables d'environnement.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config regroupe les paramètres de l'API (SPEC §6).
type Config struct {
	DatabaseURL     string
	HTTPAddr        string
	LogLevel        slog.Level
	DBMaxConns      int32
	ShutdownTimeout time.Duration
}

// Load lit la configuration via getenv et agrège toutes les erreurs
// rencontrées, pour qu'un seul démarrage suffise à toutes les voir.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		HTTPAddr:        ":8080",
		LogLevel:        slog.LevelInfo,
		DBMaxConns:      10,
		ShutdownTimeout: 10 * time.Second,
	}
	var errs []error

	cfg.DatabaseURL = getenv("DATABASE_URL")
	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL est obligatoire"))
	} else if _, err := pgxpool.ParseConfig(cfg.DatabaseURL); err != nil {
		// L'erreur de pgx peut contenir le DSN, donc le mot de passe : on ne la propage pas.
		errs = append(errs, errors.New("DATABASE_URL n'est pas un DSN PostgreSQL valide"))
	}

	if v := getenv("HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}

	if v := getenv("LOG_LEVEL"); v != "" {
		switch v {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "info":
			cfg.LogLevel = slog.LevelInfo
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		default:
			errs = append(errs, fmt.Errorf("LOG_LEVEL=%q : valeurs acceptées debug, info, warn, error", v))
		}
	}

	if v := getenv("DB_MAX_CONNS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 || n > math.MaxInt32 {
			errs = append(errs, fmt.Errorf("DB_MAX_CONNS=%q : entier entre 1 et %d attendu", v, math.MaxInt32))
		} else {
			cfg.DBMaxConns = int32(n)
		}
	}

	if v := getenv("SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT=%q : durée positive attendue (ex. 10s)", v))
		} else {
			cfg.ShutdownTimeout = d
		}
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
