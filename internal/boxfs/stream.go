package boxfs

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// 串流讀寫（C33 檔案面板）：大檔不整個讀進記憶體。

// ErrNotRunning 表示要開機才能做（列資料夾、搬移、刪除），而呼叫端沒有要開機。
var ErrNotRunning = errors.New("這個 Bot 的電腦沒有開機")

// Stat 看容器裡一個路徑（不跟著連結：最後一段是連結時 Mode 有 ModeSymlink、LinkTarget 有值）。不存在時回 ErrNotExist。
func (d *Docker) Stat(ctx context.Context, p string) (container.PathStat, error) {
	p, err := Clean(p)
	if err != nil {
		return container.PathStat{}, err
	}
	res, err := d.API.ContainerStatPath(ctx, d.ID, client.ContainerStatPathOptions{Path: p})
	if cerrdefs.IsNotFound(err) {
		return container.PathStat{}, ErrNotExist
	}
	if err != nil {
		return container.PathStat{}, fmt.Errorf("讀取 Bot 電腦裡的 %s 失敗：%w", p, err)
	}
	return res.Stat, nil
}

type tarFile struct {
	io.Reader
	body io.Closer
}

func (t tarFile) Close() error { return t.body.Close() }

// Open 串流讀一個一般檔案。不存在回 ErrNotExist；是資料夾、連結或其他種類時回錯誤。呼叫端要 Close。
func (d *Docker) Open(ctx context.Context, p string) (io.ReadCloser, container.PathStat, error) {
	p, err := Clean(p)
	if err != nil {
		return nil, container.PathStat{}, err
	}
	res, err := d.API.CopyFromContainer(ctx, d.ID, client.CopyFromContainerOptions{SourcePath: p})
	if cerrdefs.IsNotFound(err) {
		return nil, container.PathStat{}, ErrNotExist
	}
	if err != nil {
		return nil, container.PathStat{}, fmt.Errorf("讀取 Bot 電腦裡的 %s 失敗：%w", p, err)
	}
	if !res.Stat.Mode.IsRegular() || res.Stat.LinkTarget != "" {
		res.Content.Close()
		return nil, res.Stat, fmt.Errorf("%s 不是一般檔案", p)
	}
	tr := tar.NewReader(res.Content)
	for {
		h, err := tr.Next()
		if err != nil {
			res.Content.Close()
			if err == io.EOF {
				return nil, res.Stat, ErrNotExist
			}
			return nil, res.Stat, err
		}
		if h.Typeflag == tar.TypeReg {
			return tarFile{Reader: io.LimitReader(tr, h.Size), body: res.Content}, res.Stat, nil
		}
		if h.Typeflag == tar.TypeDir || h.Typeflag == tar.TypeSymlink {
			res.Content.Close()
			return nil, res.Stat, fmt.Errorf("%s 不是一般檔案", p)
		}
	}
}

// WriteStream 在已經存在的資料夾 dir 底下寫入 rel（中間缺的資料夾一起建），內容是 r 的 size 個位元組。
// 擁有者是 Bot 使用者；已經有同名檔案時換掉。r 少於 size 個位元組時回錯誤。
func (d *Docker) WriteStream(ctx context.Context, dir, rel string, size, mode int64, r io.Reader) error {
	dir, err := Clean(dir)
	if err != nil {
		return err
	}
	name := path.Clean(rel)
	if name == "." || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || name == ".." || strings.Contains(name, "/../") {
		return fmt.Errorf("檔名不對：%q", rel)
	}
	if mode == 0 {
		mode = 0o644
	}
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		now := time.Now()
		parts := strings.Split(name, "/")
		for i := 1; i < len(parts); i++ {
			if err := tw.WriteHeader(&tar.Header{Name: strings.Join(parts[:i], "/") + "/", Typeflag: tar.TypeDir, Mode: 0o755,
				Uid: d.UID, Gid: d.GID, ModTime: now}); err != nil {
				pw.CloseWithError(err)
				return
			}
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: mode, Size: size, Uid: d.UID, Gid: d.GID, ModTime: now}); err != nil {
			pw.CloseWithError(err)
			return
		}
		if n, err := io.CopyN(tw, r, size); err != nil {
			if err == io.EOF {
				err = fmt.Errorf("內容不完整（%d／%d 位元組）", n, size)
			}
			pw.CloseWithError(err)
			return
		}
		pw.CloseWithError(tw.Close())
	}()
	_, err = d.API.CopyToContainer(ctx, d.ID, client.CopyToContainerOptions{DestinationPath: dir, Content: pr})
	pr.CloseWithError(errors.New("寫入結束"))
	if err != nil {
		return fmt.Errorf("寫入 Bot 電腦的 %s 失敗：%w", path.Join(dir, name), err)
	}
	return nil
}

// Mkdir 在已經存在的資料夾 dir 底下建立 rel（可以多層），擁有者是 Bot 使用者。
func (d *Docker) Mkdir(ctx context.Context, dir, rel string) error {
	dir, err := Clean(dir)
	if err != nil {
		return err
	}
	name := path.Clean(rel)
	if name == "." || strings.HasPrefix(name, "/") || strings.Contains("/"+name+"/", "/../") {
		return fmt.Errorf("資料夾名稱不對：%q", rel)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	parts := strings.Split(name, "/")
	for i := 1; i <= len(parts); i++ {
		if err := tw.WriteHeader(&tar.Header{Name: strings.Join(parts[:i], "/") + "/", Typeflag: tar.TypeDir, Mode: 0o755,
			Uid: d.UID, Gid: d.GID, ModTime: now}); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if _, err := d.API.CopyToContainer(ctx, d.ID, client.CopyToContainerOptions{DestinationPath: dir, Content: &buf}); err != nil {
		return fmt.Errorf("在 Bot 電腦建立資料夾失敗：%w", err)
	}
	return nil
}

// EnsureRunning 確認電腦開機中；沒開機時用 Wake 開機（Wake 是 nil 時回 ErrNotRunning）。
func (d *Docker) EnsureRunning(ctx context.Context) error {
	if d.Running {
		return nil
	}
	if d.Wake == nil {
		return ErrNotRunning
	}
	id, err := d.Wake(ctx)
	if err != nil {
		return fmt.Errorf("開機失敗：%w", err)
	}
	d.ID, d.Running = id, true
	return nil
}

// ExitError 是電腦裡的指令結束碼不是 0。
type ExitError struct {
	Cmd    string
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("%s 失敗（%d）：%s", e.Cmd, e.Code, e.Stderr)
}

// Output 在開機中的電腦裡以 user 執行指令，回傳 stdout（超過 max 位元組時回錯誤）。結束碼不是 0 時回 *ExitError。
// 電腦沒開機時回 ErrNotRunning（先呼叫 EnsureRunning）。
func (d *Docker) Output(ctx context.Context, user string, max int64, cmd ...string) ([]byte, error) {
	if !d.Running {
		return nil, ErrNotRunning
	}
	var out bytes.Buffer
	err := run(ctx, d.API, d.ID, user, &limitWriter{w: &out, n: max}, cmd...)
	return out.Bytes(), err
}

// limitWriter 超過 n 個位元組時回錯誤（不默默截斷）。
type limitWriter struct {
	w io.Writer
	n int64
}

var errTooMuch = errors.New("輸出太多")

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n > 0 && int64(len(p)) > l.n {
		return 0, errTooMuch
	}
	l.n -= int64(len(p))
	return l.w.Write(p)
}

func run(ctx context.Context, api API, containerID, user string, stdout io.Writer, cmd ...string) error {
	created, err := api.ExecCreate(ctx, containerID, client.ExecCreateOptions{User: user, AttachStdout: true, AttachStderr: true, Cmd: cmd})
	if err != nil {
		return fmt.Errorf("無法在 Bot 的電腦裡執行 %s：%w", cmd[0], err)
	}
	att, err := api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("無法在 Bot 的電腦裡執行 %s：%w", cmd[0], err)
	}
	var stderr bytes.Buffer
	_, err = stdcopy.StdCopy(stdout, &limitWriter{w: &stderr, n: 64 << 10}, att.Reader)
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
		return &ExitError{Cmd: cmd[0], Code: res.ExitCode, Stderr: msg}
	}
	return nil
}
