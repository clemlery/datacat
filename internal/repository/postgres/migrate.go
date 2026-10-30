package postgres

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/clemlery/datacat/migrations"
)

// Migrate applique les migrations embarquées sur le pool existant.
// Fermer le *sql.DB issu de OpenDBFromPool ne ferme pas le pool.
func Migrate(pool *pgxpool.Pool, logger *slog.Logger) (err error) {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("lecture des migrations embarquées : %w", err)
	}

	driver, err := migratepgx.WithInstance(stdlib.OpenDBFromPool(pool), &migratepgx.Config{})
	if err != nil {
		return fmt.Errorf("initialisation du driver de migration : %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		return fmt.Errorf("initialisation de golang-migrate : %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if err == nil {
			err = errors.Join(srcErr, dbErr)
		}
	}()

	before, _, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("lecture de la version du schéma : %w", err)
	}

	err = m.Up()
	var dirty migrate.ErrDirty
	switch {
	case errors.Is(err, migrate.ErrNoChange):
		logger.Info("migrations already up to date", "version", before)
		return nil
	case errors.As(err, &dirty):
		return fmt.Errorf("base en état dirty à la version %d : une migration a échoué en cours d'application ; "+
			"corriger le schéma à la main puis exécuter `go tool migrate -path migrations -database <DSN> force <version>`",
			dirty.Version)
	case err != nil:
		return fmt.Errorf("application des migrations : %w", err)
	}

	after, _, err := m.Version()
	if err != nil {
		return fmt.Errorf("lecture de la version du schéma : %w", err)
	}
	logger.Info("migrations applied", "from", before, "to", after)
	return nil
}
