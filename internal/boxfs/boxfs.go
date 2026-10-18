// Package boxfs 讀寫 Bot 電腦裡的檔案（C31：技能、記憶、SOUL.md）。
//
// 一律走 Docker API（hosts.Host 的 Docker()）：本機與 SSH 遠端主機都一樣，電腦沒開機也能讀寫
// （docker cp 對停止的容器也有效）。只有刪除要在電腦裡執行 rm，需要開機。
package boxfs

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// File 是一個檔案。Name 是相對路徑（用 /）。
type File struct {
	Name string
	Data []byte
	Mode int64 // 0 表示 0644
}

// FS 是 Bot 電腦裡的檔案。路徑都是容器裡的絕對路徑。
type FS interface {
	// ReadFile 讀一個檔案；不存在時回 ErrNotExist。
	ReadFile(ctx context.Context, p string) ([]byte, error)
	// ReadDir 讀資料夾底下所有一般檔案（遞迴；Name 相對於 dir）。資料夾不存在時回空的。總大小超過 max 時回錯誤。
	ReadDir(ctx context.Context, dir string, max int64) ([]File, error)
	// WriteFiles 把檔案寫到 dir 底下（中間的資料夾會建立），檔案屬於 Bot 使用者。
	WriteFiles(ctx context.Context, dir string, files []File) error
	// Remove 刪掉一個檔案或整個資料夾（不存在不算錯）。
	Remove(ctx context.Context, p string) error
}

// ErrNotExist 表示檔案不存在。
var ErrNotExist = errors.New("檔案不存在")

// ErrNoComputer 表示 Bot 還沒有電腦（從來沒開過機）。
var ErrNoComputer = errors.New("這個 Bot 還沒有電腦")

// API 是用到的 Docker API；hosts.DockerAPI 符合。
type API interface {
	CopyToContainer(ctx context.Context, id string, o client.CopyToContainerOptions) (client.CopyToContainerResult, error)
	CopyFromContainer(ctx context.Context, id string, o client.CopyFromContainerOptions) (client.CopyFromContainerResult, error)
	ExecCreate(ctx context.Context, containerID string, options client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(ctx context.Context, execID string, options client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecInspect(ctx context.Context, execID string, options client.ExecInspectOptions) (client.ExecInspectResult, error)
}

// Docker 是一台 Bot 電腦（容器）的檔案。
type Docker struct {
	API API
	ID  string
	// UID、GID 是寫入檔案的擁有者（容器裡的 Bot 使用者）。
	UID, GID int
	// Running 表示容器正在跑；沒在跑時 Remove 先呼叫 Wake 開機。
	Running bool
	// Wake 開機並回傳（可能換了的）容器 id。可為 nil（沒開機就不能刪）。
	Wake func(ctx context.Context) (string, error)
}

// Clean 檢查並整理容器裡的路徑：要是絕對路徑、不能有 ..。
func Clean(p string) (string, error) {
	if !strings.HasPrefix(p, "/") || slices.Contains(strings.Split(p, "/"), "..") {
		return "", fmt.Errorf("路徑不對：%q", p)
	}
	return path.Clean(p), nil
}

// ReadFile 實作 FS。
func (d *Docker) ReadFile(ctx context.Context, p string) ([]byte, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, err
	}
	res, err := d.API.CopyFromContainer(ctx, d.ID, client.CopyFromContainerOptions{SourcePath: p})
	if cerrdefs.IsNotFound(err) {
		return nil, ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("讀取 Bot 電腦裡的 %s 失敗：%w", p, err)
	}
	defer res.Content.Close()
	tr := tar.NewReader(res.Content)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, ErrNotExist
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 16<<20))
		}
		if h.Typeflag == tar.TypeDir {
			return nil, fmt.Errorf("%s 是資料夾", p)
		}
	}
}

// ReadDir 實作 FS。
func (d *Docker) ReadDir(ctx context.Context, dir string, max int64) ([]File, error) {
	dir, err := Clean(dir)
	if err != nil {
		return nil, err
	}
	res, err := d.API.CopyFromContainer(ctx, d.ID, client.CopyFromContainerOptions{SourcePath: dir})
	if cerrdefs.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("讀取 Bot 電腦裡的 %s 失敗：%w", dir, err)
	}
	defer res.Content.Close()
	return untar(res.Content, max)
}

// untar 讀出 tar 裡的一般檔案。docker cp <dir> 的項目以資料夾名稱開頭，去掉這一層。
func untar(r io.Reader, max int64) ([]File, error) {
	tr := tar.NewReader(r)
	var out []File
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(path.Clean("/"+h.Name), "/")
		if _, rest, ok := strings.Cut(name, "/"); ok {
			name = rest
		} else {
			continue // 不在資料夾裡（不會發生）
		}
		total += h.Size
		if max > 0 && total > max {
			return nil, fmt.Errorf("檔案太多太大（超過 %d MB）", max>>20)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		out = append(out, File{Name: name, Data: data, Mode: h.Mode})
	}
}

// Tar 把檔案包成 tar（含中間的資料夾），擁有者是 uid:gid。
func Tar(files []File, uid, gid int) (*bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	dirs := map[string]bool{}
	for _, f := range files {
		name := path.Clean(f.Name)
		if name == "." || strings.HasPrefix(name, "/") || slices.Contains(strings.Split(name, "/"), "..") {
			return nil, fmt.Errorf("檔名不對：%q", f.Name)
		}
		parts := strings.Split(name, "/")
		for i := 1; i < len(parts); i++ {
			dir := strings.Join(parts[:i], "/") + "/"
			if dirs[dir] {
				continue
			}
			dirs[dir] = true
			if err := tw.WriteHeader(&tar.Header{Name: dir, Typeflag: tar.TypeDir, Mode: 0o755, Uid: uid, Gid: gid, ModTime: now}); err != nil {
				return nil, err
			}
		}
		mode := f.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: int64(len(f.Data)), Uid: uid, Gid: gid, ModTime: now}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.Data); err != nil {
			return nil, err
		}
	}
	return &buf, tw.Close()
}

// WriteFiles 實作 FS。
func (d *Docker) WriteFiles(ctx context.Context, dir string, files []File) error {
	dir, err := Clean(dir)
	if err != nil {
		return err
	}
	// 目的資料夾要已經存在：不存在就往上一層寫，把缺的資料夾放進 tar（例：第一次寫 /hermes/skills）。
	target, prefix := dir, ""
	for {
		named := files
		if prefix != "" {
			named = make([]File, len(files))
			for i, f := range files {
				named[i] = File{Name: prefix + "/" + f.Name, Data: f.Data, Mode: f.Mode}
			}
		}
		buf, err := Tar(named, d.UID, d.GID)
		if err != nil {
			return err
		}
		_, err = d.API.CopyToContainer(ctx, d.ID, client.CopyToContainerOptions{DestinationPath: target, Content: buf, CopyUIDGID: true})
		if err == nil {
			return nil
		}
		if !cerrdefs.IsNotFound(err) || target == "/" {
			return fmt.Errorf("寫入 Bot 電腦的 %s 失敗：%w", dir, err)
		}
		prefix = path.Join(path.Base(target), prefix)
		target = path.Dir(target)
	}
}

// Remove 實作 FS：在電腦裡執行 rm -rf（Docker 沒有刪檔的 API，所以要開機）。
func (d *Docker) Remove(ctx context.Context, p string) error {
	p, err := Clean(p)
	if err != nil {
		return err
	}
	if p == "/" {
		return errors.New("不能刪除整台電腦")
	}
	if !d.Running {
		if d.Wake == nil {
			return errors.New("這個 Bot 的電腦沒有開機，刪不了檔案")
		}
		id, err := d.Wake(ctx)
		if err != nil {
			return fmt.Errorf("刪除前開機失敗：%w", err)
		}
		d.ID, d.Running = id, true
	}
	return Run(ctx, d.API, d.ID, "0", "rm", "-rf", "--", p)
}

// Run 在容器裡執行一個指令（不接 stdin），等它結束；結束碼不是 0 時回錯誤（附 stderr 的最後一段）。
func Run(ctx context.Context, api API, containerID, user string, cmd ...string) error {
	created, err := api.ExecCreate(ctx, containerID, client.ExecCreateOptions{User: user, AttachStdout: true, AttachStderr: true, Cmd: cmd})
	if err != nil {
		return fmt.Errorf("無法在 Bot 的電腦裡執行 %s：%w", cmd[0], err)
	}
	att, err := api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("無法在 Bot 的電腦裡執行 %s：%w", cmd[0], err)
	}
	var stderr bytes.Buffer
	_, err = stdcopy.StdCopy(io.Discard, &stderr, att.Reader)
	att.Close()
	if err != nil {
		return err
	}
	res, err := api.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		return fmt.Errorf("%s 失敗（%d）：%s", cmd[0], res.ExitCode, msg)
	}
	return nil
}

// Mem 是記憶體裡的 FS（測試用；路徑是絕對路徑）。
type Mem struct {
	Files map[string][]byte
}

// NewMem 建立空的 Mem。
func NewMem() *Mem { return &Mem{Files: map[string][]byte{}} }

// ReadFile 實作 FS。
func (m *Mem) ReadFile(_ context.Context, p string) ([]byte, error) {
	b, ok := m.Files[path.Clean(p)]
	if !ok {
		return nil, ErrNotExist
	}
	return slices.Clone(b), nil
}

// ReadDir 實作 FS。
func (m *Mem) ReadDir(_ context.Context, dir string, max int64) ([]File, error) {
	prefix := path.Clean(dir) + "/"
	var out []File
	var total int64
	for _, k := range sortedKeys(m.Files) {
		if strings.HasPrefix(k, prefix) {
			total += int64(len(m.Files[k]))
			if max > 0 && total > max {
				return nil, fmt.Errorf("檔案太多太大（超過 %d MB）", max>>20)
			}
			out = append(out, File{Name: strings.TrimPrefix(k, prefix), Data: slices.Clone(m.Files[k]), Mode: 0o644})
		}
	}
	return out, nil
}

// WriteFiles 實作 FS。
func (m *Mem) WriteFiles(_ context.Context, dir string, files []File) error {
	for _, f := range files {
		name := path.Clean(f.Name)
		if strings.HasPrefix(name, "/") || slices.Contains(strings.Split(name, "/"), "..") {
			return fmt.Errorf("檔名不對：%q", f.Name)
		}
		m.Files[path.Join(dir, name)] = slices.Clone(f.Data)
	}
	return nil
}

// Remove 實作 FS。
func (m *Mem) Remove(_ context.Context, p string) error {
	p = path.Clean(p)
	for k := range m.Files {
		if k == p || strings.HasPrefix(k, p+"/") {
			delete(m.Files, k)
		}
	}
	return nil
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Locator 找 Bot 的電腦。create 為 false 且還沒有電腦時回 ErrNoComputer；為 true 時先建立（不開機）。
type Locator func(ctx context.Context, botID string, create bool) (FS, error)
