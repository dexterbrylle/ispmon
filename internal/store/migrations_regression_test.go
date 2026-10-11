package store

import (
	"database/sql"
	"errors"
	"testing"
)

func TestMigrationCrashRollsBackSchemaAndVersion(t *testing.T) {
	db, err := sql.Open("sqlite", t.TempDir()+"/db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	original := migrations
	defer func() { migrations = original }()
	crash := errors.New("simulated crash before commit")
	migrations = []migrationFunc{func(tx *sql.Tx) error {
		var v int
		if err := tx.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
			return err
		}
		if v != 1 {
			t.Errorf("in-transaction version = %d, want 1", v)
		}
		if err := migrateV1(tx); err != nil {
			return err
		}
		return nil
	}, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version != 2 {
			t.Errorf("version %d", version)
		}
		_, err := tx.Exec("CREATE TABLE crash_test (id INTEGER)")
		if err != nil {
			return err
		}
		return crash
	}}
	if !errors.Is(ApplyMigrations(db), crash) {
		t.Fatal("expected crash")
	}
	migrations = original
	if err := ApplyMigrations(db); err != nil {
		t.Fatal(err)
	}
	if err := ApplyMigrations(db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='crash_test'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("partial migration survived")
	}
}
