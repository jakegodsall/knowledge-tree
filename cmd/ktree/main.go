package main

import (
	"database/sql"
	"embed"
	"fmt"
	"jakegodsall/knowledge-tree/src/repository/sqlite"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Config struct {
	DBPath string
}

func defaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		DBPath: filepath.Join(home, ".ktree", "ktree.db"),
	}
}


//go:embed migrations
var migrations embed.FS

func main() {
	config := defaultConfig()

	if err := os.MkdirAll(filepath.Dir(config.DBPath), 0755); err != nil {
		fmt.Fprintf(os.Stderr, "could not create data directory: %v\n", err)
		os.Exit(1)
	}

	// WAL and a busy timeout let the CLI and the MCP server share the file
	// without "database is locked" errors.
	// SetMaxIdleConns(0) ensures connections are closed when idle so the WAL
	// shared-memory lock is released, allowing Google Drive to sync the file
	// between machines.
	db, err := sql.Open("sqlite3", config.DBPath+"?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not open database: %v\n", err)
		os.Exit(1)
	}
	db.SetMaxIdleConns(0)
	defer func() {
		db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		db.Close()
	}()

	err = runMigrations(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error running migrations: %v\n", err)
		os.Exit(1)
	}

	a := &app{
		trees:         sqlite.NewKnowledgeTreeRepository(db),
		nodes:         sqlite.NewNodeRepository(db),
		prerequisites: sqlite.NewPrerequisiteRepository(db),
		tags:          sqlite.NewNodeTagRepository(db),
	}

	commands := map[string]func([]string) error{
		"list":        a.runList,
		"create":      a.runCreate,
		"show":        a.runShow,
		"delete-tree": a.runDeleteTree,
		"demote-tree": a.runDemoteTree,
		"add-node":    a.runAddNode,
		"move-node":   a.runMoveNode,
		"update-node": a.runUpdateNode,
		"delete-node": a.runDeleteNode,
		"complete":    a.runComplete,
		"require":     a.runRequire,
		"tag":         a.runTag,
		"import":      a.runImport,
		"mcp":         a.runMCP,
	}

	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}

	command, ok := commands[os.Args[1]]

	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s\n", os.Args[1], usage)
		os.Exit(1)
	}

	if err := command(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// schema_migrations records which migrations have been applied, keyed by the
// migration file name without its .up.sql / .down.sql suffix.
const createSchemaMigrationsTable = `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY NOT NULL,
		applied_at DATETIME NOT NULL
	)
`

func runMigrations(db *sql.DB) error {
	if _, err := db.Exec(createSchemaMigrationsTable); err != nil {
		return fmt.Errorf("could not create schema_migrations table: %w", err)
	}

	applied, err := appliedMigrations(db)

	if err != nil {
		return err
	}

	// embed.FS returns entries sorted by file name, so migrations run in order
	dirs, err := migrations.ReadDir("migrations")

	if err != nil {
		return err
	}

	for _, entry := range dirs {
		version, isUp := strings.CutSuffix(entry.Name(), ".up.sql")

		if !isUp || applied[version] {
			continue
		}

		fileName := "migrations/" + entry.Name()
		content, err := migrations.ReadFile(fileName)

		if err != nil {
			return fmt.Errorf("could not read migration file %s: %w", fileName, err)
		}

		err = execMigration(
			db,
			string(content),
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
			version,
			time.Now(),
		)

		if err != nil {
			return fmt.Errorf("could not execute migration %s: %w", fileName, err)
		}
	}

	return nil
}

func rollbackMigrations(db *sql.DB) error {
	if _, err := db.Exec(createSchemaMigrationsTable); err != nil {
		return fmt.Errorf("could not create schema_migrations table: %w", err)
	}

	applied, err := appliedMigrations(db)

	if err != nil {
		return err
	}

	dirs, err := migrations.ReadDir("migrations")

	if err != nil {
		return err
	}

	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}

	for _, entry := range dirs {
		version, isDown := strings.CutSuffix(entry.Name(), ".down.sql")

		if !isDown || !applied[version] {
			continue
		}

		fileName := "migrations/" + entry.Name()
		content, err := migrations.ReadFile(fileName)

		if err != nil {
			return fmt.Errorf("could not read migration file %s: %w", fileName, err)
		}

		err = execMigration(
			db,
			string(content),
			"DELETE FROM schema_migrations WHERE version = ?",
			version,
		)

		if err != nil {
			return fmt.Errorf("could not execute migration %s: %w", fileName, err)
		}
	}

	return nil
}

func appliedMigrations(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query("SELECT version FROM schema_migrations")

	if err != nil {
		return nil, fmt.Errorf("could not read schema_migrations: %w", err)
	}

	defer rows.Close()

	applied := map[string]bool{}

	for rows.Next() {
		var version string

		if err := rows.Scan(&version); err != nil {
			return nil, err
		}

		applied[version] = true
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return applied, nil
}

// execMigration runs a migration's SQL and updates schema_migrations in a
// single transaction, so a failed migration is never recorded as applied.
func execMigration(db *sql.DB, migrationSQL string, recordQuery string, recordArgs ...any) error {
	tx, err := db.Begin()

	if err != nil {
		return err
	}

	defer tx.Rollback()

	if _, err := tx.Exec(migrationSQL); err != nil {
		return err
	}

	if _, err := tx.Exec(recordQuery, recordArgs...); err != nil {
		return err
	}

	return tx.Commit()
}
