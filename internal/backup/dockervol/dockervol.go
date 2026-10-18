// Package dockervol 是 backup.Computers 的真實作：經 Docker API（本機與 SSH 主機）讀寫 Bot 電腦的 volume。
// 和 backup 分開，因為 store 的測試會用到 backup，backup 不能引用 store／hosts／box。
package dockervol

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"

	"xbot/internal/backup"
	"xbot/internal/box"
	"xbot/internal/hosts"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// Computers 經 Docker API 讀寫 Bot 電腦的 volume（本機與遠端主機）。
type Computers struct {
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

func (d *Computers) host(ctx context.Context, ref backup.Host) (hosts.Host, error) {
	row := store.Host{ID: ref.ID, Name: ref.Name, Kind: ref.Kind, Address: ref.Address, Port: ref.Port, SSHUser: ref.SSHUser, KnownHosts: ref.KnownHosts}
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

// Export 實作 backup.Computers。
func (d *Computers) Export(ctx context.Context, ref backup.ComputerRef, includeLogins bool, emit func(string, *tar.Header, io.Reader) error) error {
	h, err := d.host(ctx, ref.Host)
	if err != nil {
		return err
	}
	err = box.ExportVolumes(ctx, h.Docker(), d.Image, ref.BotID, includeLogins, emit)
	if errors.Is(err, box.ErrNoData) {
		return backup.ErrNoData
	}
	return err
}

// Import 實作 backup.Computers。
func (d *Computers) Import(ctx context.Context, ref backup.ComputerRef, stamp string) (backup.VolumeSink, error) {
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

// Close 實作 backup.Computers。
func (d *Computers) Close() error {
	var errs []error
	for _, c := range d.conns {
		if c.h != nil {
			errs = append(errs, c.h.Close())
		}
	}
	d.conns = nil
	return errors.Join(errs...)
}
