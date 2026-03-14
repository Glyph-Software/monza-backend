package db

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// migrationsSource returns the file:// URL for the migrations directory.
// Uses MIGRATIONS_PATH if set (e.g. in Docker); otherwise path relative to cwd (local runs).
func migrationsSource() string {
	if p := os.Getenv("MIGRATIONS_PATH"); p != "" {
		if len(p) > 7 && p[:7] == "file://" {
			return p
		}
		return "file://" + p
	}
	cwd, _ := os.Getwd()
	dir := filepath.Join(cwd, "internal", "db", "migrations")
	return "file://" + filepath.ToSlash(dir)
}

// RunMigrations applies all up migrations using the given database URL.
// It is safe to call this on every startup; ErrNoChange is ignored.
func RunMigrations(databaseURL string) error {
	source := migrationsSource()
	m, err := migrate.New(source, databaseURL)
	if err != nil {
		return err
	}

	err = m.Up()
	if err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return err
	}

	return nil
}

