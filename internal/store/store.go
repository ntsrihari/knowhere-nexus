// Package store owns the Nexus SQLite database: file permissions, connection
// settings and forward-only migrations.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (P-02)
)

// FileName is the database file inside the data directory.
const FileName = "nexus.db"

// DB wraps the database handle.
type DB struct {
	*sql.DB
	Path string
}

// Open prepares dataDir (mode 0700), creates or opens the database file (mode
// 0600) and applies migrations. It refuses a data directory or database file
// that is accessible by group or others, or that is a symbolic link.
func Open(ctx context.Context, dataDir string) (*DB, error) {
	if err := prepareDir(dataDir); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, FileName)
	if err := prepareFile(path); err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// One connection serialises writes and avoids SQLITE_BUSY; Nexus's write
	// volume is tiny. Revisit if reads become a bottleneck.
	db.SetMaxOpenConns(1)
	db.SetConnMaxIdleTime(0)
	s := &DB{DB: db, Path: path}
	if err := s.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	if err := Migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func prepareDir(dir string) error {
	if dir == "" || !filepath.IsAbs(dir) {
		return errors.New("store: data directory must be an absolute path")
	}
	st, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("store: create data directory: %w", err)
		}
		// MkdirAll applies the umask; set the mode explicitly.
		return os.Chmod(dir, 0o700)
	case err != nil:
		return fmt.Errorf("store: data directory: %w", err)
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("store: data directory %s must be a real directory", dir)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("store: data directory %s has mode %04o; it must not be accessible by group or others (0700)", dir, st.Mode().Perm())
	}
	return nil
}

func prepareFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("store: database file: %w", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("store: database file: %w", err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("store: %s is not a regular file", path)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("store: database file %s has mode %04o; it must be 0600", path, st.Mode().Perm())
	}
	return nil
}

// Migration is one forward-only schema step.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

const bootstrapSQL = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT    NOT NULL,
	applied_at TEXT    NOT NULL
) STRICT`

// Migrations is the ordered schema history. Never edit an applied entry;
// append a new one. Session and audit tables arrive with M2.
var Migrations = []Migration{
	{Version: 1, Name: "schema_migrations", SQL: bootstrapSQL},
}

// Migrate applies every migration not yet recorded, each in its own
// transaction. Running it again is a no-op.
func Migrate(ctx context.Context, db *sql.DB) error {
	return migrate(ctx, db, Migrations)
}

func migrate(ctx context.Context, db *sql.DB, ms []Migration) error {
	if _, err := db.ExecContext(ctx, bootstrapSQL); err != nil {
		return fmt.Errorf("store: bootstrap migrations table: %w", err)
	}
	last := 0
	for _, m := range ms {
		if m.Version != last+1 {
			return fmt.Errorf("store: migration %d (%s) out of sequence", m.Version, m.Name)
		}
		last = m.Version
		if err := applyOne(ctx, db, m); err != nil {
			return err
		}
	}
	return nil
}

func applyOne(ctx context.Context, db *sql.DB, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: migration %d: %w", m.Version, err)
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.Version).Scan(&n); err != nil {
		return fmt.Errorf("store: migration %d: %w", m.Version, err)
	}
	if n > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return fmt.Errorf("store: migration %d (%s): %w", m.Version, m.Name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.Version, m.Name, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("store: record migration %d: %w", m.Version, err)
	}
	return tx.Commit()
}

// Version returns the highest applied migration.
func Version(ctx context.Context, db *sql.DB) (int, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	return int(v.Int64), err
}
