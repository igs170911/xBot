// Package store 是 XBot 的 SQLite 資料庫：開檔、migration、各資料表的查詢函式（C01）。
//
// 寫入一律走單一連線（SQLite 同時只能有一個寫入者，排隊比互相搶鎖可靠），
// 讀取走另一組唯讀連線池；WAL 模式下讀和寫互不阻擋。
// 每張表的函式都掛在 *Queries 上；DB.Tx 會給一個交易內的 *Queries，可以組合多個寫入。
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite" // 純 Go 的 SQLite driver

	"xbot/internal/limits"
)

// ErrNotFound 表示要找的資料不存在。
var ErrNotFound = errors.New("找不到資料")

// FileName 是資料目錄裡的資料庫檔名。
const FileName = "xbot.db"

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Queries 提供各資料表的查詢函式。w 用來寫，r 用來讀；在交易內兩者是同一個交易。
type Queries struct {
	w querier
	r querier
}

// DB 是開好的資料庫。可以同時給很多 goroutine 使用。
type DB struct {
	*Queries
	write *sql.DB
	read  *sql.DB
	path  string

	readOnly bool // ReadOnly() 給的副本：共用連線池，不能關
}

// Open 開啟（或建立）path 的資料庫，並套用所有還沒套用的 migration。
func Open(ctx context.Context, path string) (*DB, error) {
	busy := limits.DBBusyTimeout.Milliseconds()
	pragmas := url.Values{}
	pragmas.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busy))
	pragmas.Add("_pragma", "foreign_keys(1)")
	pragmas.Add("_pragma", "journal_mode(WAL)")
	// WAL 下 NORMAL 仍能保持一致；FULL 會讓每個串流事件都 fsync（同 LazyBoy store.py）。
	pragmas.Add("_pragma", "synchronous(NORMAL)")
	// 交易一開始就拿寫入鎖，避免讀轉寫時才撞到 SQLITE_BUSY。
	pragmas.Add("_txlock", "immediate")

	w, err := sql.Open("sqlite", "file:"+path+"?"+pragmas.Encode())
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	w.SetConnMaxIdleTime(0)
	w.SetConnMaxLifetime(0)
	if err := w.PingContext(ctx); err != nil {
		w.Close()
		return nil, fmt.Errorf("開啟資料庫 %s 失敗：%w", path, err)
	}
	if err := migrate(ctx, w); err != nil {
		w.Close()
		return nil, err
	}

	ro := url.Values{}
	ro.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busy))
	ro.Add("_pragma", "foreign_keys(1)")
	ro.Add("_pragma", "query_only(1)")
	r, err := sql.Open("sqlite", "file:"+path+"?"+ro.Encode())
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(limits.DBReadConns)
	r.SetMaxIdleConns(limits.DBReadConns)
	if err := r.PingContext(ctx); err != nil {
		w.Close()
		r.Close()
		return nil, fmt.Errorf("開啟資料庫 %s 失敗：%w", path, err)
	}
	return &DB{Queries: &Queries{w: w, r: r}, write: w, read: r, path: path}, nil
}

// Path 回傳資料庫檔案位置。
func (db *DB) Path() string { return db.path }

// Close 關閉資料庫。
func (db *DB) Close() error {
	if db.readOnly {
		return nil
	}
	return errors.Join(db.read.Close(), db.write.Close())
}

// Tx 在一個寫入交易裡執行 fn；fn 回傳錯誤就整個復原。
// fn 裡只能用傳進來的 q，不要再用 db（寫入只有一條連線，會互等）。
func (db *DB) Tx(ctx context.Context, fn func(q *Queries) error) error {
	return db.atomic(ctx, fn)
}

// NewID 產生一個新的 id：32 位十六進位，前 12 位是毫秒時間，所以大致依建立時間排序。
func NewID() string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], uint64(time.Now().UnixMilli())<<16)
	if _, err := rand.Read(b[6:]); err != nil {
		panic(err) // crypto/rand 不會失敗
	}
	return hex.EncodeToString(b[:])
}

// nowMS 是目前時間（unix 毫秒）。測試可以替換。
var nowMS = func() int64 { return time.Now().UnixMilli() }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullStr 把空字串存成 NULL（給可為空的外鍵欄位用）。
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// affected 確認 UPDATE／DELETE 有改到資料，沒有就回 ErrNotFound。
func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

type scanner interface{ Scan(dest ...any) error }

// collect 把查詢結果逐列轉成 T。
func collect[T any](rows *sql.Rows, err error, scan func(scanner) (T, error)) ([]T, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// atomic 讓 fn 裡的多個寫入成為一個交易：已在交易裡就直接執行，否則開一個新的。
func (q *Queries) atomic(ctx context.Context, fn func(q *Queries) error) error {
	db, ok := q.w.(*sql.DB)
	if !ok {
		return fn(q)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&Queries{w: tx, r: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
