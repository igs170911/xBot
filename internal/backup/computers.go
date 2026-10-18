package backup

import (
	"archive/tar"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"

	"xbot/internal/box"
	"xbot/internal/hosts"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// ComputerRef 是一個 Bot 與它的電腦所在的主機。
type ComputerRef struct {
	BotID, BotName string
	Host           store.Host
}

// VolumeSink 收一個 Bot 的電腦資料（還原用）。
type VolumeSink interface {
	Write(name string, h *tar.Header, r io.Reader) error
	// Close 結束（cause 不是 nil 時中止），回傳原本資料保留在哪些 volume。
	Close(ctx context.Context, cause error) (kept []string, err error)
}

// Computers 讀寫 Bot 電腦的資料（Docker volume）。DockerComputers 是真的實作，測試用假的。
type Computers interface {
	// Export 把 ref 的電腦資料串流出來（name 是「kind/相對路徑」）。沒有資料時回傳 box.ErrNoData；
	// 主機連不上時回傳 hosts.ErrHostOffline。
	Export(ctx context.Context, ref ComputerRef, includeLogins bool, emit func(name string, h *tar.Header, r io.Reader) error) error
	// Import 開始還原 ref 的電腦資料；主機連不上時回傳 hosts.ErrHostOffline。
	Import(ctx context.Context, ref ComputerRef, stamp string) (VolumeSink, error)
	// Close 關掉所有主機連線。
	Close() error
}

// ListComputers 從資料庫檔（備份時是快照、還原時是還原後的資料庫）列出所有 Bot 與它的電腦所在的主機：
// 有電腦時以電腦（box.host_id）為準，否則 bot.host_id，都沒有時是本機。只讀，不套用 migration。
func ListComputers(ctx context.Context, dbFile string) ([]ComputerRef, error) {
	db, err := sql.Open("sqlite", "file:"+dbFile+"?mode=ro")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	hostRows, err := db.QueryContext(ctx, `SELECT id, name, kind, address, port, ssh_user, known_hosts FROM host ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("讀主機清單失敗：%w", err)
	}
	byID := map[string]store.Host{}
	local := store.Host{}
	for hostRows.Next() {
		var h store.Host
		if err := hostRows.Scan(&h.ID, &h.Name, &h.Kind, &h.Address, &h.Port, &h.SSHUser, &h.KnownHosts); err != nil {
			hostRows.Close()
			return nil, err
		}
		byID[h.ID] = h
		if h.Kind == "local" && local.ID == "" {
			local = h
		}
	}
	hostRows.Close()
	rows, err := db.QueryContext(ctx, `SELECT b.id, b.name, COALESCE(NULLIF(x.host_id, ''), NULLIF(b.host_id, ''), '')
		FROM bot b LEFT JOIN box x ON x.bot_id = b.id ORDER BY b.created_at, b.id`)
	if err != nil {
		return nil, fmt.Errorf("讀 Bot 清單失敗：%w", err)
	}
	defer rows.Close()
	var out []ComputerRef
	for rows.Next() {
		var r ComputerRef
		var hostID string
		if err := rows.Scan(&r.BotID, &r.BotName, &hostID); err != nil {
			return nil, err
		}
		if h, ok := byID[hostID]; ok {
			r.Host = h
		} else {
			r.Host = local
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// backupComputers 把每個 Bot 的電腦資料寫進 tw（xbot-computers/<Bot id>/…）。
func backupComputers(ctx context.Context, tw *tar.Writer, dbSnap string, o Options, report func(string, ...any)) ([]ComputerEntry, error) {
	refs, err := ListComputers(ctx, dbSnap)
	if err != nil {
		return nil, err
	}
	var out []ComputerEntry
	for _, ref := range refs {
		e := ComputerEntry{BotID: ref.BotID, BotName: ref.BotName, HostID: ref.Host.ID, HostName: ref.Host.Name}
		if !safeID(ref.BotID) {
			e.Skipped = "Bot id 不合法"
			out = append(out, e)
			continue
		}
		prefix := ComputersDir + "/" + ref.BotID + "/"
		err := o.Computers.Export(ctx, ref, o.IncludeLogins, func(name string, h *tar.Header, r io.Reader) error {
			hdr := *h
			hdr.Name = prefix + name
			if hdr.Typeflag == tar.TypeDir {
				hdr.Name += "/"
			}
			if hdr.Typeflag == tar.TypeLink {
				hdr.Linkname = prefix + cleanRel(hdr.Linkname)
			}
			if err := tw.WriteHeader(&hdr); err != nil {
				return &writeErr{err}
			}
			if hdr.Typeflag == tar.TypeReg {
				rr := &readErr{r: r}
				n, err := io.Copy(tw, rr)
				if err != nil && rr.err == nil {
					return &writeErr{err} // 寫備份檔失敗：整份都不能用了
				}
				if err != nil {
					// 讀主機那邊斷了：用 0 補滿這個檔案，讓備份檔的格式還是完整的，這個 Bot 記成不完整。
					if _, werr := io.CopyN(tw, zeros{}, hdr.Size-n); werr != nil {
						return &writeErr{werr}
					}
					return rr.err
				}
				e.Bytes += n
				e.Files++
			}
			return nil
		})
		var we *writeErr
		switch {
		case errors.As(err, &we):
			return nil, fmt.Errorf("寫入 %s 的電腦資料失敗：%w", ref.BotName, we.err)
		case errors.Is(err, box.ErrNoData):
			e.Empty = true
		case err != nil:
			e.Skipped = err.Error()
			if e.Files > 0 {
				e.Skipped = "不完整：" + e.Skipped
			}
		default:
			e.Included = true
		}
		switch {
		case e.Included:
			report("   💻 %s（%s）：%d 個檔案、%s", ref.BotName, ref.Host.Name, e.Files, humanBytes(e.Bytes))
		case e.Skipped != "":
			report("   ⚠️  %s（%s）的電腦資料沒有備到：%s", ref.BotName, ref.Host.Name, e.Skipped)
		}
		out = append(out, e)
	}
	return out, nil
}

type writeErr struct{ err error }

type readErr struct {
	r   io.Reader
	err error
}

func (r *readErr) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err != nil && err != io.EOF {
		r.err = err
	}
	return n, err
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func (w *writeErr) Error() string { return w.err.Error() }

func cleanRel(n string) string { return strings.TrimPrefix(path.Clean("/"+n), "/") }

func safeID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// ComputerResult 是還原一個 Bot 電腦資料的結果。
type ComputerResult struct {
	ComputerEntry
	// Restored：資料已寫回那台主機。
	Restored bool
	// Kept 是原本資料保留的 volume。
	Kept []string
	// Error 是沒有還原的原因（例：主機不在線上；資料還在備份檔裡）。
	Error string
}

// RestoreComputers 把備份檔裡的 Bot 電腦資料寫回每個 Bot 的主機（主機依 dataDir 裡已還原的資料庫）。
// only 不是空的時只還原這些 Bot。主機連不上的 Bot 跳過並在結果裡說明（資料還在備份檔裡，之後可以再還原一次）。
// v1 的備份檔沒有電腦資料，回傳空的結果。
func RestoreComputers(ctx context.Context, file, dataDir string, comps Computers, only []string) ([]ComputerResult, error) {
	var m Manifest
	if err := readManifest(file, &m); err != nil {
		return nil, err
	}
	if len(m.Computers) == 0 {
		return nil, nil
	}
	refs, err := ListComputers(ctx, filepath.Join(dataDir, store.FileName))
	if err != nil {
		return nil, err
	}
	refByID := map[string]ComputerRef{}
	for _, r := range refs {
		refByID[r.BotID] = r
	}
	want := map[string]bool{}
	for _, id := range only {
		want[id] = true
	}
	results := map[string]*ComputerResult{}
	var order []string
	for _, e := range m.Computers {
		if !e.Included || (len(want) > 0 && !want[e.BotID]) {
			continue
		}
		r := &ComputerResult{ComputerEntry: e}
		if ref, ok := refByID[e.BotID]; ok {
			r.HostID, r.HostName = ref.Host.ID, ref.Host.Name
		} else {
			r.Error = "資料庫裡沒有這個 Bot"
		}
		results[e.BotID] = r
		order = append(order, e.BotID)
	}
	stamp := time.Now().Format("20060102-150405")
	var cur string
	var sink VolumeSink
	finish := func(cause error) {
		if sink == nil {
			return
		}
		kept, err := sink.Close(ctx, cause)
		r := results[cur]
		r.Kept = kept
		if err != nil {
			r.Error = "還原失敗：" + err.Error()
		} else {
			r.Restored = true
		}
		sink = nil
	}
	err = walkTar(file, func(h *tar.Header, rd io.Reader) error {
		name := cleanRel(h.Name)
		rest, ok := strings.CutPrefix(name, ComputersDir+"/")
		if !ok {
			return nil
		}
		botID, inner, _ := strings.Cut(rest, "/")
		if botID != cur {
			finish(nil)
			cur = botID
			r := results[botID]
			if r != nil && r.Error == "" {
				ref := refByID[botID]
				s, err := comps.Import(ctx, ref, stamp)
				if err != nil {
					r.Error = err.Error()
				} else {
					sink = s
				}
			}
		}
		if sink == nil || inner == "" {
			return nil
		}
		hdr := *h
		if hdr.Typeflag == tar.TypeLink {
			ln, ok := strings.CutPrefix(cleanRel(hdr.Linkname), ComputersDir+"/"+botID+"/")
			if !ok {
				return fmt.Errorf("備份檔裡有不安全的連結：%s", h.Name)
			}
			hdr.Linkname = ln
		}
		if err := sink.Write(inner, &hdr, rd); err != nil {
			finish(err)
		}
		return nil
	})
	finish(err)
	out := make([]ComputerResult, 0, len(order))
	for _, id := range order {
		r := results[id]
		if !r.Restored && r.Error == "" {
			r.Error = "備份檔裡找不到資料"
		}
		out = append(out, *r)
	}
	return out, err
}

// DockerComputers 經 Docker API 讀寫 Bot 電腦的 volume（本機與遠端主機）。
type DockerComputers struct {
	// DataDir 是 XBot 的資料目錄（連遠端主機用的 SSH 金鑰與 known_hosts）。
	DataDir string
	// Image 是 Bot 電腦映像（輔助容器用）。
	Image string
	// Open 連上一台主機；nil 時用 hosts.Open。
	Open func(ctx context.Context, row store.Host) (hosts.Host, error)

	conns map[string]hostConn
}

type hostConn struct {
	h   hosts.Host
	err error
}

func (d *DockerComputers) host(ctx context.Context, row store.Host) (hosts.Host, error) {
	if d.conns == nil {
		d.conns = map[string]hostConn{}
	}
	if c, ok := d.conns[row.ID]; ok {
		return c.h, c.err
	}
	open := d.Open
	if open == nil {
		open = func(ctx context.Context, row store.Host) (hosts.Host, error) {
			return hosts.Open(ctx, d.DataDir, row, hosts.OpenOptions{Wait: limits.BackupHostWait})
		}
	}
	h, err := open(ctx, row)
	if err != nil {
		err = fmt.Errorf("%w：%s（%v）", hosts.ErrHostOffline, row.Name, err)
	}
	d.conns[row.ID] = hostConn{h, err}
	return h, err
}

// Export 實作 Computers。
func (d *DockerComputers) Export(ctx context.Context, ref ComputerRef, includeLogins bool, emit func(string, *tar.Header, io.Reader) error) error {
	h, err := d.host(ctx, ref.Host)
	if err != nil {
		return err
	}
	return box.ExportVolumes(ctx, h.Docker(), d.Image, ref.BotID, includeLogins, emit)
}

// Import 實作 Computers。
func (d *DockerComputers) Import(ctx context.Context, ref ComputerRef, stamp string) (VolumeSink, error) {
	h, err := d.host(ctx, ref.Host)
	if err != nil {
		return nil, err
	}
	vi, err := box.OpenImport(ctx, h.Docker(), d.Image, ref.BotID, stamp)
	if err != nil {
		return nil, err
	}
	return importSink{vi}, nil
}

type importSink struct{ vi *box.VolumeImport }

func (s importSink) Write(name string, h *tar.Header, r io.Reader) error {
	return s.vi.Write(name, h, r)
}

func (s importSink) Close(ctx context.Context, cause error) ([]string, error) {
	res, err := s.vi.Close(ctx, cause)
	return res.Kept, err
}

// Close 實作 Computers。
func (d *DockerComputers) Close() error {
	var errs []error
	for _, c := range d.conns {
		if c.h != nil {
			errs = append(errs, c.h.Close())
		}
	}
	d.conns = nil
	return errors.Join(errs...)
}
