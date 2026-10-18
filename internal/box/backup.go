package box

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"xbot/internal/hosts"
	"xbot/internal/limits"
)

// 備份與還原 Bot 電腦的資料（V10：make backup 連電腦的 volume 一起備份）。
//
// 和搬家（move.go）一樣用一個不開機的輔助容器（同一個 Bot 電腦映像、沒有網路）掛上 home／workspace／hermes，
// 經 Docker API 的 CopyFromContainer 串流 tar（本機與 SSH 主機都一樣），資料只經過 XBot 的記憶體。
// 訂閱 CLI 憑證（cred volume）永遠不備份：每次開機前會依同意紀錄重新寫入（EN-2b）。
// 瀏覽器的登入資料（home 裡的 chrome-profile）預設不備份，IncludeLogins 才包含。
//
// 還原：先刪掉這個 Bot 的容器（下次開機重建），原本的 volume 有資料時先整份複製到
// <volume>-before-restore-<時間> 保留，再刪掉重建、把備份串流回去。檔案的擁有者與權限照 tar 還原。

// BackupKinds 是會備份的 volume（輔助容器裡的資料夾名稱）。
var BackupKinds = []string{"home", "workspace", "hermes"}

// LoginPaths 是 home volume 裡的瀏覽器登入資料（相對於 home），預設不備份。
var LoginPaths = []string{"chrome-profile", ".config/chromium"}

// ErrNoData 表示這個 Bot 在這台主機上沒有電腦資料（還沒建過電腦）。
var ErrNoData = errors.New("這個 Bot 還沒有電腦資料")

// IsLoginPath 回答 kind／rel（rel 相對於 volume 根目錄）是不是瀏覽器登入資料。
func IsLoginPath(kind, rel string) bool {
	if kind != "home" {
		return false
	}
	for _, p := range LoginPaths {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

func backupHelper(botID string) string { return "xbot-backup-" + botID }

// ExportVolumes 把 Bot 在 api 這台主機上的 home／workspace／hermes 串流出來：每個項目呼叫一次 emit，
// name 是「kind/相對路徑」（kind 本身的資料夾是「kind」）。includeLogins 為 false 時略過瀏覽器登入資料。
// 電腦開著也可以備份（不會停下它；正在寫的檔案可能不是完整的一刻）。沒有任何資料時回傳 ErrNoData。
func ExportVolumes(ctx context.Context, api hosts.DockerAPI, image, botID string, includeLogins bool,
	emit func(name string, h *tar.Header, r io.Reader) error) error {
	has, err := hasVolumes(ctx, api, botID)
	if err != nil {
		return err
	}
	if !has {
		return ErrNoData
	}
	// 只有部分 volume 存在時（例：舊版沒有 hermes）補建成空的，讓輔助容器掛得上。
	for _, v := range movedVolumes(botID) {
		if err := ensureVolume(ctx, api, v.Name, botID); err != nil {
			return err
		}
	}
	name := backupHelper(botID)
	removeContainer(ctx, api, name)
	if _, err := api.ContainerCreate(ctx, volumeHelper(image, botID, name, "backup", true, nil, "true")); err != nil {
		return fmt.Errorf("建立備份用的輔助容器失敗：%w", dockerErr(err))
	}
	defer removeContainer(context.WithoutCancel(ctx), api, name)
	for _, kind := range BackupKinds {
		if err := exportKind(ctx, api, name, kind, includeLogins, emit); err != nil {
			return fmt.Errorf("讀取 %s 失敗：%w", kind, err)
		}
	}
	return nil
}

func exportKind(ctx context.Context, api hosts.DockerAPI, helper, kind string, includeLogins bool,
	emit func(string, *tar.Header, io.Reader) error) error {
	rd, err := api.CopyFromContainer(ctx, helper, client.CopyFromContainerOptions{SourcePath: path.Join(moveRoot, kind)})
	if err != nil {
		return dockerErr(err)
	}
	defer rd.Content.Close()
	tr := tar.NewReader(rd.Content)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := cleanName(h.Name)
		k, rel, _ := strings.Cut(name, "/")
		if k != kind {
			continue // CopyFromContainer 的 tar 一律以 kind 開頭；不是的話不要
		}
		if !includeLogins && IsLoginPath(kind, rel) {
			continue
		}
		if h.Typeflag == tar.TypeLink {
			ln := cleanName(h.Linkname)
			if lk, lrel, _ := strings.Cut(ln, "/"); lk != kind || (!includeLogins && IsLoginPath(kind, lrel)) {
				continue // 連到略過的檔案：不要
			}
			h.Linkname = ln
		}
		h.Name = name
		if err := emit(name, h, tr); err != nil {
			return err
		}
	}
}

// ImportResult 是還原一個 Bot 電腦資料的結果。
type ImportResult struct {
	// Kept 是原本資料保留的 volume 名稱（原本沒有資料時空白）。
	Kept []string
	// Files 是寫回去的項目數。
	Files int
}

// VolumeImport 把備份串流回一個 Bot 的 volume（OpenImport → Write… → Close）。
type VolumeImport struct {
	api    hosts.DockerAPI
	botID  string
	helper string
	pw     *io.PipeWriter
	tw     *tar.Writer
	done   chan error
	res    ImportResult
}

// OpenImport 準備還原：刪掉 Bot 的容器（下次開機重建），有資料的 volume 先複製到 <volume>-before-restore-<stamp>，
// 刪掉重建，然後開始串流（Write）。stamp 例：20261019-090000。
func OpenImport(ctx context.Context, api hosts.DockerAPI, image, botID, stamp string) (*VolumeImport, error) {
	if _, err := api.ImageInspect(ctx, image); err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, fmt.Errorf("這台主機上沒有 Bot 電腦映像 %s（先 make box 或重新檢查主機）", image)
		}
		return nil, dockerErr(err)
	}
	if c, err := api.ContainerInspect(ctx, ContainerName(botID), client.ContainerInspectOptions{}); err == nil {
		if c.Container.Config == nil || c.Container.Config.Labels[LabelBot] != botID {
			return nil, fmt.Errorf("%w：%s", ErrNameTaken, ContainerName(botID))
		}
		if _, err := api.ContainerRemove(ctx, c.Container.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			return nil, fmt.Errorf("刪除原本的電腦失敗：%w", dockerErr(err))
		}
	} else if !cerrdefs.IsNotFound(err) {
		return nil, dockerErr(err)
	}
	vi := &VolumeImport{api: api, botID: botID, helper: backupHelper(botID)}
	kept, err := keepVolumes(ctx, api, image, botID, stamp)
	if err != nil {
		return nil, err
	}
	vi.res.Kept = kept
	for _, v := range movedVolumes(botID) {
		if _, err := api.VolumeRemove(ctx, v.Name, client.VolumeRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			return nil, fmt.Errorf("清掉原本的資料 %s 失敗：%w", v.Name, dockerErr(err))
		}
		if err := ensureVolume(ctx, api, v.Name, botID); err != nil {
			return nil, err
		}
	}
	removeContainer(ctx, api, vi.helper)
	if _, err := api.ContainerCreate(ctx, volumeHelper(image, botID, vi.helper, "restore", false, nil, "true")); err != nil {
		return nil, fmt.Errorf("建立還原用的輔助容器失敗：%w", dockerErr(err))
	}
	pr, pw := io.Pipe()
	vi.pw, vi.tw, vi.done = pw, tar.NewWriter(pw), make(chan error, 1)
	go func() {
		_, err := api.CopyToContainer(ctx, vi.helper, client.CopyToContainerOptions{DestinationPath: moveRoot, Content: pr})
		if err != nil {
			pr.CloseWithError(err)
		} else {
			_, _ = io.Copy(io.Discard, pr)
		}
		vi.done <- dockerErr(err)
	}()
	return vi, nil
}

// Write 寫一個項目；name 是 ExportVolumes 給的「kind/相對路徑」。
func (v *VolumeImport) Write(name string, h *tar.Header, r io.Reader) error {
	name = cleanName(name)
	kind, _, _ := strings.Cut(name, "/")
	if !isBackupKind(kind) {
		return fmt.Errorf("備份裡有不認得的電腦資料：%s", name)
	}
	hdr := *h
	hdr.Name = name
	if hdr.Typeflag == tar.TypeDir {
		hdr.Name += "/"
	}
	if hdr.Typeflag == tar.TypeLink {
		ln := cleanName(hdr.Linkname)
		if lk, _, _ := strings.Cut(ln, "/"); !isBackupKind(lk) {
			return fmt.Errorf("備份裡有不安全的連結：%s", name)
		}
		hdr.Linkname = ln
	}
	if err := v.tw.WriteHeader(&hdr); err != nil {
		return v.fail(err)
	}
	if hdr.Typeflag == tar.TypeReg {
		if _, err := io.Copy(v.tw, r); err != nil {
			return v.fail(err)
		}
	}
	v.res.Files++
	return nil
}

func (v *VolumeImport) fail(err error) error {
	select {
	case derr := <-v.done:
		v.done <- derr
		if derr != nil {
			return derr
		}
	default:
	}
	return err
}

// Close 結束串流並刪掉輔助容器；cause 不是 nil 時中止。
func (v *VolumeImport) Close(ctx context.Context, cause error) (ImportResult, error) {
	if cause == nil {
		cause = v.tw.Close()
	}
	if cause != nil {
		v.pw.CloseWithError(cause)
	} else {
		v.pw.Close()
	}
	err := <-v.done
	removeContainer(context.WithoutCancel(ctx), v.api, v.helper)
	if cause != nil {
		return v.res, cause
	}
	return v.res, err
}

func isBackupKind(k string) bool {
	for _, b := range BackupKinds {
		if k == b {
			return true
		}
	}
	return false
}

// keepVolumes 把有資料的 home／workspace／hermes 複製到 <volume>-before-restore-<stamp>（同一台主機上，cp -a）。
func keepVolumes(ctx context.Context, api hosts.DockerAPI, image, botID, stamp string) ([]string, error) {
	var have []moveVol
	for _, v := range movedVolumes(botID) {
		if _, err := api.VolumeInspect(ctx, v.Name, client.VolumeInspectOptions{}); err == nil {
			have = append(have, v)
		} else if !cerrdefs.IsNotFound(err) {
			return nil, dockerErr(err)
		}
	}
	if len(have) == 0 {
		return nil, nil
	}
	var kept []string
	extra := make([]mount.Mount, 0, len(have))
	var script strings.Builder
	script.WriteString("set -e\n")
	for _, v := range have {
		keep := v.Name + "-before-restore-" + stamp
		_, _ = api.VolumeRemove(ctx, keep, client.VolumeRemoveOptions{})
		if _, err := api.VolumeCreate(ctx, client.VolumeCreateOptions{Name: keep,
			Labels: map[string]string{LabelManaged: "true", LabelBot: botID, LabelKeptAt: stamp}}); err != nil {
			return nil, fmt.Errorf("建立保留原本資料的 %s 失敗：%w", keep, dockerErr(err))
		}
		kept = append(kept, keep)
		extra = append(extra, mount.Mount{Type: mount.TypeVolume, Source: keep, Target: path.Join(keepRoot, v.Kind)})
		fmt.Fprintf(&script, "cp -a %s/%s/. %s/%s/\n", moveRoot, v.Kind, keepRoot, v.Kind)
	}
	name := backupHelper(botID) + "-keep"
	removeContainer(ctx, api, name)
	opts := volumeHelper(image, botID, name, "keep", true, extra, script.String())
	// cp -a 要讀所有檔案、保留擁有者與權限。
	opts.HostConfig.CapAdd = []string{"DAC_READ_SEARCH", "DAC_OVERRIDE", "CHOWN", "FOWNER", "FSETID"}
	if _, err := api.ContainerCreate(ctx, opts); err != nil {
		return nil, fmt.Errorf("建立保留原本資料的輔助容器失敗：%w", dockerErr(err))
	}
	defer removeContainer(context.WithoutCancel(ctx), api, name)
	if _, err := runHelperTimeout(ctx, api, name, limits.BackupKeepTimeout); err != nil {
		return nil, fmt.Errorf("保留原本的電腦資料失敗：%w", err)
	}
	return kept, nil
}

// StopUsingDir 停掉（不刪）這台主機上正在跑、而且掛著 dir 底下資料夾的 Bot 電腦，回傳停了哪些 Bot。
// 還原整個資料目錄後用：原本的資料目錄改名保留了，開著的電腦掛的 product.sock 資料夾還是改名前那一個，
// 要重開才接得回新的服務。只看掛載來源，同一台 Docker 上別的 XBot（開發版、測試）的電腦不受影響。
func StopUsingDir(ctx context.Context, api hosts.DockerAPI, dir string) ([]string, error) {
	dir = strings.TrimRight(dir, "/") + "/"
	list, err := api.ContainerList(ctx, client.ContainerListOptions{Filters: client.Filters{}.Add("label", LabelManaged+"=true")})
	if err != nil {
		return nil, dockerErr(err)
	}
	var stopped []string
	for _, c := range list.Items {
		botID := c.Labels[LabelBot]
		if botID == "" || c.Labels[LabelHelper] != "" {
			continue
		}
		uses := false
		for _, mp := range c.Mounts {
			if mp.Type == mount.TypeBind && strings.HasPrefix(mp.Source+"/", dir) {
				uses = true
			}
		}
		if !uses {
			continue
		}
		t := int(limits.BoxStopTimeout.Seconds())
		if _, err := api.ContainerStop(ctx, c.ID, client.ContainerStopOptions{Timeout: &t}); err != nil && !cerrdefs.IsNotFound(err) {
			return stopped, fmt.Errorf("停止 Bot 電腦失敗：%w", dockerErr(err))
		}
		stopped = append(stopped, botID)
	}
	return stopped, nil
}

// LabelKeptAt 標在還原前保留的 volume 上（值是還原的時間）。
const LabelKeptAt = "xbot.kept-before-restore"

const keepRoot = "/xbot-keep"

// volumeHelper 是備份與還原用的輔助容器（同 helperOptions：Bot 電腦映像、沒有網路、root 但拿掉所有 capability）。
func volumeHelper(image, botID, name, role string, readOnly bool, extra []mount.Mount, script string) client.ContainerCreateOptions {
	o := helperOptions(image, botID, "", readOnly, script)
	o.Name = name
	o.Config.Labels[LabelHelper] = role
	o.HostConfig.Mounts = append(o.HostConfig.Mounts, extra...)
	return o
}

// runHelperTimeout 開機輔助容器並等它結束（不讀輸出）。
func runHelperTimeout(ctx context.Context, api hosts.DockerAPI, name string, limit time.Duration) (*container.State, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	if _, err := api.ContainerStart(ctx, name, client.ContainerStartOptions{}); err != nil {
		return nil, dockerErr(err)
	}
	for {
		res, err := api.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
		if err != nil {
			return nil, dockerErr(err)
		}
		st := res.Container.State
		if st != nil && !st.Running && !st.Restarting && st.Status != "created" {
			if st.ExitCode != 0 {
				return st, fmt.Errorf("輔助容器結束碼 %d %s", st.ExitCode, st.Error)
			}
			return st, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
