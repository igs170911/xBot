package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// TestH1MigrationDedupes tests that migration 0030 deduplicates existing open cards
func TestH1MigrationDedupes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// Create a v29 DB (before migration 0030)
	dbPath := filepath.Join(dir, FileName)
	rawDB, err := sql.Open("sqlite", dbPath+"?_journal=WAL&_busy_timeout=5000&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()

	// Create schema_migrations table (created by migration runner)
	_, err = rawDB.ExecContext(ctx, `CREATE TABLE schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at INTEGER NOT NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}

	// Run migrations up to 0029
	migrations, err := Migrations()
	if err != nil {
		t.Fatal(err)
	}
	migrationTime := time.Now().UnixMilli()
	for _, m := range migrations {
		if m.Version >= 30 {
			break
		}
		if _, err := rawDB.ExecContext(ctx, m.SQL); err != nil {
			t.Fatalf("migration %d failed: %v", m.Version, err)
		}
		// Mark as applied
		_, err = rawDB.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			m.Version, m.Name, migrationTime)
		if err != nil {
			t.Fatalf("Failed to mark migration %d as applied: %v", m.Version, err)
		}
	}

	// Insert duplicate open cards for the same room
	roomRef := "room_test123"
	now := time.Now().UnixMilli()
	_, err = rawDB.ExecContext(ctx, `INSERT INTO inbox_item
		(id, kind, ref, title, body, payload, status, resolution, resolved_by, resolved_at, read_at, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		NewID(), "mention", roomRef, "Card 1", "", "{}", "open", "", "", 0, 0, 0, now-2000, now-2000)
	if err != nil {
		t.Fatal(err)
	}

	_, err = rawDB.ExecContext(ctx, `INSERT INTO inbox_item
		(id, kind, ref, title, body, payload, status, resolution, resolved_by, resolved_at, read_at, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		NewID(), "mention", roomRef, "Card 2 (newer)", "", "{}", "open", "", "", 0, 0, 0, now-1000, now-1000)
	if err != nil {
		t.Fatal(err)
	}

	rawDB.Close()

	// Open with migration 0030
	db, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open after migration failed: %v", err)
	}
	defer db.Close()

	// Verify exactly 1 open card remains
	var count int
	err = db.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM inbox_item WHERE kind = ? AND ref = ? AND status = 'open'`,
		"mention", roomRef).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("Expected 1 open card after migration, got %d", count)
	}

	// Verify the newer card was kept
	var title string
	err = db.r.QueryRowContext(ctx, `SELECT title FROM inbox_item WHERE kind = ? AND ref = ? AND status = 'open'`,
		"mention", roomRef).Scan(&title)
	if err != nil {
		t.Fatal(err)
	}
	if title != "Card 2 (newer)" {
		t.Errorf("Expected newer card to be kept, got: %s", title)
	}

	// Verify old card was dismissed
	err = db.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM inbox_item WHERE kind = ? AND ref = ? AND status = 'dismissed'`,
		"mention", roomRef).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("Expected 1 dismissed card, got %d", count)
	}
}
