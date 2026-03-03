package db

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store wraps the sqlc Queries with the underlying database connection.
type Store struct {
	*Queries
	db *sql.DB
}

// Open creates or opens the SQLite database at the default location
// (~/.config/local-agent/local-agent.db) and runs migrations.
func Open() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("home dir: %w", err)
	}

	dir := filepath.Join(home, ".config", "local-agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	return OpenPath(filepath.Join(dir, "local-agent.db"))
}

// OpenPath creates or opens the SQLite database at the given path and runs migrations.
func OpenPath(path string) (*Store, error) {
	conn, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	// Run migrations.
	if err := runMigrations(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrations: %w", err)
	}

	return &Store{
		Queries: New(conn),
		db:      conn,
	}, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB for direct access.
func (s *Store) DB() *sql.DB {
	return s.db
}

func runMigrations(conn *sql.DB) error {
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		if _, err := conn.Exec(string(data)); err != nil {
			return fmt.Errorf("exec migration %s: %w", entry.Name(), err)
		}
	}

	return nil
}
