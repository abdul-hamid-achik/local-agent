// Command session-receipts seeds the deterministic SQLite state used by the
// saved-session Glyphrun contract without depending on a system sqlite3 CLI.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "seed session receipts: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) (resultErr error) {
	if len(args) != 2 {
		return errors.New("usage: session-receipts DATABASE PROJECT_ROOT")
	}
	databasePath, projectRoot := args[0], args[1]
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return err
	}
	database, err := sql.Open("sqlite", databasePath+"?_foreign_keys=ON")
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, database.Close())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, path := range []string{
		"internal/db/migrations/001_init.sql",
		"internal/db/migrations/002_checkpoints.sql",
		"internal/db/migrations/003_session_state.sql",
		"internal/db/migrations/004_execution_events.sql",
		"specs/fixtures/session_receipts.sql",
	} {
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		statement := strings.ReplaceAll(string(contents), "__PROJECT_ROOT__", projectRoot)
		if _, execErr := database.ExecContext(ctx, statement); execErr != nil {
			return fmt.Errorf("apply %s: %w", path, execErr)
		}
	}
	return nil
}
