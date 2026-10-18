package collab

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
)

// ErrNoFile 表示檔案不存在（或不是一般檔案）。
var ErrNoFile = errors.New("沒有這個檔案")

// Files 讀寫 Bot 電腦裡 /workspace 的檔案（帶檔傳話 AA-7、太長的結果存檔 AA-8）。rel 是 /workspace 底下的相對路徑。
type Files interface {
	// Stat 回傳一般檔案的大小；不存在、是資料夾或連結時回 ErrNoFile。
	Stat(ctx context.Context, botID, rel string) (int64, error)
	Read(ctx context.Context, botID, rel string) ([]byte, error)
	// Write 寫入檔案（需要的資料夾一起建），擁有者是 Bot 的使用者。
	Write(ctx context.Context, botID, rel string, data []byte) error
}

// cleanRel 確認路徑留在 /workspace 裡面（LazyBoy files.contained）。
func cleanRel(rel string) (string, error) {
	if rel == "" || strings.ContainsRune(rel, 0) {
		return "", errors.New("無效的檔案路徑")
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return "", errors.New("檔案必須位於這個 Bot 的工作目錄")
		}
	}
	c := strings.TrimPrefix(path.Clean("/"+rel), "/")
	if c == "" {
		return "", errors.New("檔案必須位於這個 Bot 的工作目錄")
	}
	return c, nil
}

// DirFiles 把每個 Bot 的 /workspace 放在 Root/<botID>/workspace（測試、沒有 Docker 時用）。
type DirFiles struct{ Root string }

// Dir 是 Bot 的 /workspace 在這台電腦的位置。
func (d DirFiles) Dir(botID string) string { return filepath.Join(d.Root, botID, "workspace") }

func (d DirFiles) path(botID, rel string) (string, error) {
	rel, err := cleanRel(rel)
	if err != nil {
		return "", err
	}
	return filepath.Join(d.Dir(botID), filepath.FromSlash(rel)), nil
}

// Stat 實作 Files。
func (d DirFiles) Stat(_ context.Context, botID, rel string) (int64, error) {
	p, err := d.path(botID, rel)
	if err != nil {
		return 0, err
	}
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return 0, ErrNoFile
	}
	return fi.Size(), nil
}

// Read 實作 Files。
func (d DirFiles) Read(_ context.Context, botID, rel string) ([]byte, error) {
	p, err := d.path(botID, rel)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// Write 實作 Files。
func (d DirFiles) Write(_ context.Context, botID, rel string, data []byte) error {
	p, err := d.path(botID, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// CopyAPI 是讀寫容器檔案用到的 Docker API；hosts.DockerAPI 符合。
type CopyAPI interface {
	CopyToContainer(ctx context.Context, id string, o client.CopyToContainerOptions) (client.CopyToContainerResult, error)
	CopyFromContainer(ctx context.Context, id string, o client.CopyFromContainerOptions) (client.CopyFromContainerResult, error)
}

// DockerFiles 經 Docker 讀寫 Bot 電腦（容器，關機中也可以）的 /workspace。
type DockerFiles struct {
	// Locate 找 Bot 的電腦；沒有電腦時 id 是空白。
	Locate func(ctx context.Context, botID string) (api CopyAPI, id string, err error)
	// UID、GID 是寫入檔案的擁有者（Bot 電腦裡的使用者）。
	UID, GID int
}

const workspace = "/workspace"

func (d DockerFiles) container(ctx context.Context, botID string) (CopyAPI, string, error) {
	api, id, err := d.Locate(ctx, botID)
	if err != nil {
		return nil, "", err
	}
	if id == "" {
		return nil, "", fmt.Errorf("這個 Bot 還沒有電腦：%w", ErrNoFile)
	}
	return api, id, nil
}

func (d DockerFiles) open(ctx context.Context, botID, rel string) (client.CopyFromContainerResult, error) {
	rel, err := cleanRel(rel)
	if err != nil {
		return client.CopyFromContainerResult{}, err
	}
	api, id, err := d.container(ctx, botID)
	if err != nil {
		return client.CopyFromContainerResult{}, err
	}
	res, err := api.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: workspace + "/" + rel})
	if cerrdefs.IsNotFound(err) {
		return res, ErrNoFile
	}
	if err != nil {
		return res, err
	}
	if !res.Stat.Mode.IsRegular() || res.Stat.LinkTarget != "" {
		res.Content.Close()
		return res, ErrNoFile
	}
	return res, nil
}

// Stat 實作 Files。
func (d DockerFiles) Stat(ctx context.Context, botID, rel string) (int64, error) {
	res, err := d.open(ctx, botID, rel)
	if err != nil {
		return 0, err
	}
	res.Content.Close()
	return res.Stat.Size, nil
}

// Read 實作 Files。
func (d DockerFiles) Read(ctx context.Context, botID, rel string) ([]byte, error) {
	res, err := d.open(ctx, botID, rel)
	if err != nil {
		return nil, err
	}
	defer res.Content.Close()
	tr := tar.NewReader(res.Content)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, ErrNoFile
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
}

// Write 實作 Files。
func (d DockerFiles) Write(ctx context.Context, botID, rel string, data []byte) error {
	rel, err := cleanRel(rel)
	if err != nil {
		return err
	}
	api, id, err := d.container(ctx, botID)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	now := time.Now()
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		if err := tw.WriteHeader(&tar.Header{Name: strings.Join(parts[:i], "/") + "/", Mode: 0o755, Uid: d.UID, Gid: d.GID, ModTime: now,
			Typeflag: tar.TypeDir}); err != nil {
			return err
		}
	}
	if err := tw.WriteHeader(&tar.Header{Name: rel, Mode: 0o644, Size: int64(len(data)), Uid: d.UID, Gid: d.GID, ModTime: now,
		Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if _, err := api.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: workspace, Content: &buf, CopyUIDGID: true}); err != nil {
		return fmt.Errorf("寫入 Bot 電腦的檔案失敗：%w", err)
	}
	return nil
}
