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
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, fmt.Errorf("secure config dir: %w", err)
	}

	return OpenPath(filepath.Join(dir, "local-agent.db"))
}

// OpenPath creates or opens the SQLite database at the given path and runs migrations.
func OpenPath(path string) (*Store, error) {
	if path != ":memory:" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("create db: %w", err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("close db bootstrap file: %w", err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("secure db: %w", err)
		}
	}

	conn, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// Keep migration PRAGMAs and DDL on one connection. modernc SQLite does
	// not honor the mattn-style _busy_timeout DSN option on every version, so
	// set it explicitly before concurrent first-open migration attempts.
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec(`PRAGMA busy_timeout = 5000; PRAGMA foreign_keys = ON;`); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("configure sqlite connection: %w", err)
	}

	// Run migrations.
	if err := runMigrations(conn); err != nil {
		if closeErr := conn.Close(); closeErr != nil {
			return nil, fmt.Errorf("migrations: %w (close db: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("migrations: %w", err)
	}
	if path != ":memory:" {
		for _, sqlitePath := range []string{path, path + "-wal", path + "-shm"} {
			if err := os.Chmod(sqlitePath, 0o600); err != nil && !os.IsNotExist(err) {
				_ = conn.Close()
				return nil, fmt.Errorf("secure sqlite file %s: %w", filepath.Base(sqlitePath), err)
			}
		}
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
	if err := ensureSessionWorkspaceColumn(conn); err != nil {
		return err
	}
	return ensureCheckpointWorkspaceColumn(conn)
}

// ensureSessionWorkspaceColumn upgrades databases created before workspace
// scoping. Migration files are intentionally idempotent CREATE statements, so
// this PRAGMA-gated ALTER is kept in Go for safe repeated startup.
func ensureSessionWorkspaceColumn(conn *sql.DB) error {
	found, err := sessionWorkspaceColumnExists(conn)
	if err != nil {
		return err
	}
	if !found {
		if _, err := conn.Exec(`ALTER TABLE sessions ADD COLUMN workspace_id TEXT NOT NULL DEFAULT ''`); err != nil {
			// Another local-agent process may have won the first-open race after
			// our PRAGMA check. Re-inspect and accept only the desired end state.
			addedByPeer, inspectErr := sessionWorkspaceColumnExists(conn)
			if inspectErr != nil {
				return fmt.Errorf("add sessions workspace identity: %v (re-inspect: %w)", err, inspectErr)
			}
			if !addedByPeer {
				return fmt.Errorf("add sessions workspace identity: %w", err)
			}
		}
	}
	if _, err := conn.Exec(`CREATE INDEX IF NOT EXISTS idx_sessions_workspace_updated ON sessions(workspace_id, updated_at DESC)`); err != nil {
		return fmt.Errorf("index sessions workspace identity: %w", err)
	}
	return nil
}

func sessionWorkspaceColumnExists(conn *sql.DB) (bool, error) {
	rows, err := conn.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return false, fmt.Errorf("inspect sessions schema: %w", err)
	}
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("scan sessions schema: %w", err)
		}
		if name == "workspace_id" {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("close sessions schema rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read sessions schema: %w", err)
	}
	return found, nil
}

func ensureCheckpointWorkspaceColumn(conn *sql.DB) error {
	found, err := tableColumnExists(conn, "checkpoints", "workspace_id")
	if err != nil {
		return err
	}
	if !found {
		if _, err := conn.Exec(`ALTER TABLE checkpoints ADD COLUMN workspace_id TEXT NOT NULL DEFAULT ''`); err != nil {
			addedByPeer, inspectErr := tableColumnExists(conn, "checkpoints", "workspace_id")
			if inspectErr != nil {
				return fmt.Errorf("add checkpoints workspace identity: %v (re-inspect: %w)", err, inspectErr)
			}
			if !addedByPeer {
				return fmt.Errorf("add checkpoints workspace identity: %w", err)
			}
		}
	}
	if _, err := conn.Exec(`CREATE INDEX IF NOT EXISTS idx_checkpoints_workspace_session ON checkpoints(workspace_id, session_id, id DESC)`); err != nil {
		return fmt.Errorf("index checkpoints workspace identity: %w", err)
	}
	return nil
}

func tableColumnExists(conn *sql.DB, table, column string) (bool, error) {
	rows, err := conn.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		return false, fmt.Errorf("inspect %s schema: %w", table, err)
	}
	found := false
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return false, fmt.Errorf("scan %s schema: %w", table, err)
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return false, fmt.Errorf("close %s schema rows: %w", table, err)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read %s schema: %w", table, err)
	}
	return found, nil
}
