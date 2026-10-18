package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migration 是一個 schema 版本：檔名 NNNN_名稱.sql。
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations 回傳所有內嵌的 migration，依版本排序。
func Migrations() ([]Migration, error) {
	return loadMigrations(migrationFS, "migrations")
}

func loadMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	var out []Migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		num, _, ok := strings.Cut(strings.TrimSuffix(name, ".sql"), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("migration 檔名要是 NNNN_名稱.sql：%s", name)
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("migration 版本 %d 重複：%s 和 %s", v, prev, name)
		}
		seen[v] = name
		body, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return nil, err
		}
		out = append(out, Migration{Version: v, Name: name, SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	ms, err := Migrations()
	if err != nil {
		return err
	}
	return applyMigrations(ctx, db, ms)
}

// applyMigrations 依序套用還沒套用的 migration，每個在自己的交易裡；已套用的跳過，所以重跑不會出錯。
func applyMigrations(ctx context.Context, db *sql.DB, ms []Migration) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("建立 schema_migrations 失敗：%w", err)
	}
	var current int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	if n := len(ms); n > 0 && current > ms[n-1].Version {
		return fmt.Errorf("資料庫的版本（%d）比這個 XBot 新（%d），請更新 XBot 後再開", current, ms[n-1].Version)
	}
	for _, m := range ms {
		if err := applyOne(ctx, db, m); err != nil {
			return fmt.Errorf("套用 migration %s 失敗：%w", m.Name, err)
		}
	}
	return nil
}

func applyOne(ctx context.Context, db *sql.DB, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // Commit 之後 Rollback 不會做事
	// 在交易裡再檢查一次，避免兩個程序同時開同一個檔案時重複套用。
	var done int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.Version).Scan(&done); err != nil {
		return err
	}
	if done > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`, m.Version, m.Name, nowMS()); err != nil {
		return err
	}
	return tx.Commit()
}

// SchemaVersion 回傳目前已套用的最新 migration 版本。
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := db.read.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}
