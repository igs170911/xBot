// Package backup 把 XBot 的資料目錄與每個 Bot 電腦的資料（Docker volume）打包成一個 .tar.gz，並能還原回來。
//
// SQLite 資料庫用 VACUUM INTO 做一致的快照，所以服務執行中也能備份；
// 瀏覽器登入資料（Bot 的 chrome-profile）預設不備份，要用 IncludeLogins 才會包含。
//
// 備份檔格式（Manifest.Format）：
//   - 1（v1.0 之前）：只有資料目錄，檔案放在 tar 的根目錄，最後是 xbot-backup.json。
//   - 2：資料目錄一樣放在根目錄；每個 Bot 電腦的 home／workspace／hermes 放在
//     xbot-computers/<Bot id>/<home|workspace|hermes>/…（擁有者與權限照原樣）；xbot-backup.json 的 computers 列出每個 Bot
//     有沒有備到、為什麼沒有。訂閱憑證（cred volume）永遠不備份。
//
// 還原兩種格式都可以；v1 沒有電腦資料就只還原資料目錄。
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 純 Go 的 SQLite driver

	"xbot/internal/version"
)

// ManifestName 是備份檔裡描述內容的檔案。
const ManifestName = "xbot-backup.json"

// DBFile 是資料目錄裡的資料庫檔名（同 store.FileName；store 的測試會用到這個套件，所以不能反過來引用 store）。
const DBFile = "xbot.db"

// FormatVersion 是現在寫出的備份檔格式（見套件說明）。v1 的備份檔沒有 format 欄位（讀成 0，當成 1）。
const FormatVersion = 2

// ComputersDir 是備份檔裡放 Bot 電腦資料的資料夾。
const ComputersDir = "xbot-computers"

// Manifest 記錄這份備份的資訊。
type Manifest struct {
	Format        int       `json:"format,omitempty"`
	Version       string    `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	IncludeLogins bool      `json:"include_logins"`
	Files         int       `json:"files"`
	// Computers 是每個 Bot 電腦資料的備份情形（format 2）。
	Computers []ComputerEntry `json:"computers,omitempty"`
}

// ComputerEntry 是一個 Bot 電腦資料的備份情形。
type ComputerEntry struct {
	BotID    string `json:"bot_id"`
	BotName  string `json:"bot_name"`
	HostID   string `json:"host_id"`
	HostName string `json:"host_name"`
	// Included：資料完整地在備份檔裡。
	Included bool  `json:"included"`
	Files    int   `json:"files"`
	Bytes    int64 `json:"bytes"`
	// Skipped 是沒有備到（或不完整）的原因；Empty 表示還沒有電腦資料（不算問題）。
	Skipped string `json:"skipped,omitempty"`
	Empty   bool   `json:"empty,omitempty"`
}

// loginPatterns 是登入資料的位置（相對於資料目錄）；預設不備份。
var loginPatterns = []string{
	"bots/*/home/chrome-profile",
	"bots/*/home/.config/chromium",
}

// skip 是永遠不備份的檔案（SQLite 的暫存檔由快照取代）。
func skip(rel string) bool {
	return strings.HasSuffix(rel, ".db-wal") || strings.HasSuffix(rel, ".db-shm") || strings.HasSuffix(rel, ".db-journal")
}

func isLogin(rel string) bool {
	for _, p := range loginPatterns {
		n := strings.Count(p, "/") + 1
		parts := strings.Split(rel, "/")
		if len(parts) < n {
			continue
		}
		if ok, _ := path.Match(p, strings.Join(parts[:n], "/")); ok {
			return true
		}
	}
	return false
}

// Options 是一次備份的參數。
type Options struct {
	DataDir       string
	Out           string // 備份檔路徑
	IncludeLogins bool
	// Computers 讀 Bot 電腦的資料；nil 時只備份資料目錄（沒有 Docker 的環境、測試）。
	Computers Computers
	// Report 印出進度（可為 nil）。
	Report func(format string, a ...any)
}

// Create 建立備份檔，回傳寫入的檔案數。
func Create(o Options) (Manifest, error) {
	return CreateContext(context.Background(), o)
}

// CreateContext 同 Create，可以取消。
func CreateContext(ctx context.Context, o Options) (Manifest, error) {
	m := Manifest{Format: FormatVersion, Version: version.Version, CreatedAt: time.Now().UTC(), IncludeLogins: o.IncludeLogins}
	report := o.Report
	if report == nil {
		report = func(string, ...any) {}
	}
	if _, err := os.Stat(o.DataDir); err != nil {
		return m, fmt.Errorf("找不到資料目錄 %s：%w", o.DataDir, err)
	}
	if err := os.MkdirAll(filepath.Dir(o.Out), 0o700); err != nil {
		return m, err
	}
	tmpOut := o.Out + ".partial"
	f, err := os.OpenFile(tmpOut, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return m, err
	}
	defer os.Remove(tmpOut)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	snapDir, err := os.MkdirTemp("", "xbot-snap-")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(snapDir)

	err = filepath.WalkDir(o.DataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(o.DataDir, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if rel == ComputersDir && d.IsDir() {
			return filepath.SkipDir // 備份檔裡這個名字留給 Bot 電腦的資料
		}
		if !o.IncludeLogins && isLogin(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if skip(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // 不備份符號連結，避免還原時指到資料目錄外
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil // socket（例：Bot 的 ipc/*.sock）、pipe、裝置檔是執行中才有的，tar 也存不了
		}
		src := p
		if !d.IsDir() && strings.HasSuffix(rel, ".db") {
			snap := filepath.Join(snapDir, strings.ReplaceAll(rel, "/", "_"))
			if err := snapshotSQLite(p, snap); err != nil {
				return fmt.Errorf("資料庫 %s 快照失敗：%w", rel, err)
			}
			src = snap
			if info, err = os.Stat(snap); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if d.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		if _, err := io.Copy(tw, in); err != nil {
			return err
		}
		m.Files++
		return nil
	})
	if err != nil {
		return m, err
	}
	if o.Computers != nil {
		snap := filepath.Join(snapDir, DBFile)
		if _, err := os.Stat(snap); err == nil {
			if m.Computers, err = backupComputers(ctx, tw, snap, o, report); err != nil {
				return m, err
			}
		}
	}
	mb, _ := json.MarshalIndent(m, "", "  ")
	if err := tw.WriteHeader(&tar.Header{Name: ManifestName, Mode: 0o600, Size: int64(len(mb)), ModTime: m.CreatedAt}); err != nil {
		return m, err
	}
	if _, err := tw.Write(mb); err != nil {
		return m, err
	}
	if err := tw.Close(); err != nil {
		return m, err
	}
	if err := gz.Close(); err != nil {
		return m, err
	}
	if err := f.Close(); err != nil {
		return m, err
	}
	return m, os.Rename(tmpOut, o.Out)
}

func snapshotSQLite(src, dst string) error {
	db, err := sql.Open("sqlite", "file:"+src+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("VACUUM INTO ?", dst)
	return err
}

// Restore 把備份還原到 dataDir。原本的資料會先改名保留（回傳保留的位置）。
func Restore(file, dataDir string) (Manifest, string, error) {
	var m Manifest
	if err := readManifest(file, &m); err != nil {
		return m, "", err
	}
	if m.Format > FormatVersion {
		return m, "", fmt.Errorf("這份備份是較新版的 XBot（%s）做的（格式 %d），請先更新 XBot 再還原", m.Version, m.Format)
	}
	parent := filepath.Dir(dataDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return m, "", err
	}
	staging, err := os.MkdirTemp(parent, ".xbot-restore-")
	if err != nil {
		return m, "", err
	}
	defer os.RemoveAll(staging)
	if err := extract(file, staging); err != nil {
		return m, "", err
	}
	_ = os.Remove(filepath.Join(staging, ManifestName))
	_ = os.RemoveAll(filepath.Join(staging, ComputersDir))

	kept := ""
	if _, err := os.Stat(dataDir); err == nil {
		kept = fmt.Sprintf("%s.before-restore-%s", dataDir, time.Now().Format("20060102-150405"))
		if err := os.Rename(dataDir, kept); err != nil {
			return m, "", fmt.Errorf("保留原本的資料失敗：%w", err)
		}
	}
	if err := os.Rename(staging, dataDir); err != nil {
		if kept != "" {
			_ = os.Rename(kept, dataDir)
		}
		return m, "", err
	}
	_ = os.Chmod(dataDir, 0o700)
	return m, kept, nil
}

func readManifest(file string, m *Manifest) error {
	found := false
	err := walkTar(file, func(h *tar.Header, r io.Reader) error {
		if h.Name != ManifestName {
			return nil
		}
		found = true
		return json.NewDecoder(r).Decode(m)
	})
	if err != nil {
		return fmt.Errorf("讀不到備份檔 %s：%w", file, err)
	}
	if !found {
		return errors.New("這不是 XBot 的備份檔（找不到 " + ManifestName + "）")
	}
	return nil
}

func extract(file, dest string) error {
	return walkTar(file, func(h *tar.Header, r io.Reader) error {
		name := path.Clean(h.Name)
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return fmt.Errorf("備份檔裡有不安全的路徑：%s", h.Name)
		}
		if name == ComputersDir || strings.HasPrefix(name, ComputersDir+"/") {
			return nil // Bot 電腦的資料另外還原（RestoreComputers）
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			return os.MkdirAll(target, 0o700)
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(h.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, r); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		default:
			return nil // 連結等其他類型一律略過
		}
	})
}

func walkTar(file string, fn func(*tar.Header, io.Reader) error) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(h, tr); err != nil {
			return err
		}
	}
}

// DefaultDir 是備份檔預設放的位置：資料目錄旁邊的 xbot-backups。
func DefaultDir(dataDir string) string {
	return filepath.Join(filepath.Dir(dataDir), filepath.Base(dataDir)+"-backups")
}

// DefaultName 是備份檔的預設檔名。
func DefaultName(now time.Time) string {
	return "xbot-backup-" + now.Format("20060102-150405") + ".tar.gz"
}

// ReadManifest 讀備份檔的說明（不解開其他內容）。
func ReadManifest(file string) (Manifest, error) {
	var m Manifest
	err := readManifest(file, &m)
	return m, err
}

// RestoreDB 只把備份裡的資料庫（xbot.db）換回去，其他檔案不動（make update 換回舊版時用；服務要先停）。
// 換下來的資料庫（連同 -wal、-shm）改名保留，回傳保留的位置。
func RestoreDB(file, dataDir string) (string, error) {
	var m Manifest
	if err := readManifest(file, &m); err != nil {
		return "", err
	}
	tmp := filepath.Join(dataDir, DBFile+".restoring")
	found := false
	err := walkTar(file, func(h *tar.Header, r io.Reader) error {
		if path.Clean(h.Name) != DBFile || h.Typeflag != tar.TypeReg {
			return nil
		}
		found = true
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, r); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if !found {
		return "", errors.New("備份檔裡沒有資料庫")
	}
	db := filepath.Join(dataDir, DBFile)
	kept := ""
	if _, err := os.Stat(db); err == nil {
		kept = fmt.Sprintf("%s.replaced-%s", db, time.Now().Format("20060102-150405"))
		if err := os.Rename(db, kept); err != nil {
			_ = os.Remove(tmp)
			return "", fmt.Errorf("保留目前的資料庫失敗：%w", err)
		}
		for _, suf := range []string{"-wal", "-shm"} {
			if _, err := os.Stat(db + suf); err == nil {
				_ = os.Rename(db+suf, kept+suf)
			}
		}
	}
	if err := os.Rename(tmp, db); err != nil {
		return kept, err
	}
	return kept, nil
}
