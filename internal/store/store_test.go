package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestOpenCreatesPrivateFilesAndMigrates(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "nested", "data")
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	// Force WAL files to exist.
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS t (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, dir); got != 0o700 {
		t.Errorf("data dir mode %04o, want 0700", got)
	}
	for _, f := range []string{FileName, FileName + "-wal", FileName + "-shm"} {
		p := filepath.Join(dir, f)
		if _, err := os.Stat(p); err != nil {
			continue // -shm/-wal may not exist on every platform
		}
		if got := mode(t, p); got&0o077 != 0 {
			t.Errorf("%s mode %04o, want no group/other access", f, got)
		}
	}
	v, err := Version(ctx, db.DB)
	if err != nil || v != len(Migrations) {
		t.Fatalf("version %d err %v", v, err)
	}
	var journal string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal); err != nil || journal != "wal" {
		t.Errorf("journal_mode = %q (%v)", journal, err)
	}
	var fk int
	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d (%v)", fk, err)
	}
	db.Close()

	// Re-opening is idempotent.
	db2, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var rows int
	if err := db2.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&rows); err != nil || rows != len(Migrations) {
		t.Fatalf("migration rows %d (%v)", rows, err)
	}
	if err := Migrate(ctx, db2.DB); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestOpenRefusesLoosePermissions(t *testing.T) {
	ctx := context.Background()
	loose := t.TempDir()
	if err := os.Chmod(loose, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, loose); err == nil {
		t.Error("0755 data dir accepted")
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, FileName), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, dir); err == nil {
		t.Error("0644 database file accepted")
	}

	if _, err := Open(ctx, "relative/dir"); err == nil {
		t.Error("relative data dir accepted")
	}

	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "data")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, link); err == nil {
		t.Error("symlinked data dir accepted")
	}
}

func TestMigrationSequenceEnforced(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "d"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bad := append(append([]Migration{}, Migrations...), Migration{Version: len(Migrations) + 2, Name: "gap", SQL: "SELECT 1"})
	if err := migrate(ctx, db.DB, bad); err == nil {
		t.Fatal("gap in migration sequence accepted")
	}
	failing := append(append([]Migration{}, Migrations...), Migration{Version: len(Migrations) + 1, Name: "broken", SQL: "NOT SQL"})
	if err := migrate(ctx, db.DB, failing); err == nil {
		t.Fatal("broken migration accepted")
	}
	if v, _ := Version(ctx, db.DB); v != len(Migrations) {
		t.Fatalf("failed migration was recorded: version %d", v)
	}
}

func TestOpenWithURLSpecialCharactersInPath(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	dir := filepath.Join(parent, "d#x?y=1%20z")
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE t (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName+"-wal")); err != nil {
		t.Fatalf("WAL not inside the data directory: %v", err)
	}
	db.Close()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(dir) {
		t.Fatalf("files escaped the data directory: %v", entries)
	}
}
