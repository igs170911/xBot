package box

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"xbot/internal/hosts"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// 搬家（CL-5，B31）：把 Bot 的電腦從一台主機搬到另一台，登入狀態（/home/box 裡的瀏覽器設定檔）跟著走。
//
//  1. 停止原主機上的電腦（不刪）。
//  2. 原主機：一個輔助容器（同一個 Bot 電腦映像、沒有網路、唯讀掛上 home／workspace／hermes 三個 volume）量出資料大小。
//  3. 新主機：建好三個 volume 與一個輔助容器（沒開機）；每個 volume 經 Docker API 的 CopyFromContainer（原主機）→
//     CopyToContainer（新主機）串流 tar，資料只經過 XBot 的記憶體，不落地。檔案的擁有者與權限照 tar 還原。
//     串流時順便算每個檔案的 SHA-256。
//  4. 驗證：新主機的輔助容器開機算出每個檔案的 SHA-256 與檔案清單，和傳過去的逐一比對（數量、種類、大小、檢查碼）。
//  5. 切換：bot.host_id 與 box 列改成新主機（一個交易），刪掉原主機的容器、放掉那邊的 product.sock 轉發與網路防護規則，
//     在原主機留一個標記 volume（xbot.moved-to 標籤）；原本開著的在新主機開機，沒開的先建好容器。
//  6. 原主機的 volume 再保留 limits.BoxMoveKeep（24 小時）才刪（DropMoved，由 computers 的背景工作呼叫）。
//
// 1～4 任何一步失敗或被取消：刪掉新主機上建的東西，原主機的資料完全沒動，原本開著的電腦再開回來。
// 訂閱 CLI 憑證（cred volume）不搬：每次開機前會依同意紀錄重新寫入（EN-2b），不必讓憑證多經過一台主機。
// Docker 的 volume 標籤建好就不能改，所以「已搬走」的標記放在另一個小 volume（MovedMarker）上。

// 搬家用到的標籤與名稱。
const (
	// LabelMovedTo、LabelMovedAt 標在原主機的標記 volume 上：搬到哪台主機（host id）、什麼時候（毫秒）。
	LabelMovedTo = "xbot.moved-to"
	LabelMovedAt = "xbot.moved-at"
	// LabelHelper 標在搬家用的輔助容器上。
	LabelHelper = "xbot.helper"

	moveRoot = "/xbot-move"
	moveOut  = "/xbot-out"
)

// ErrMoving 表示電腦正在搬家，這時不能開機、重建或刪除。
var ErrMoving = errors.New("這個 Bot 的電腦正在搬家，搬完再試")

// ErrSameHost 表示要搬去的就是現在這台主機。
var ErrSameHost = errors.New("電腦已經在這台主機上了")

// MovedMarker 是原主機上「這個 Bot 的資料已搬走」的標記 volume。
func MovedMarker(botID string) string { return ContainerName(botID) + "-moved" }

func helperName(botID, side string) string { return "xbot-move-" + botID + "-" + side }

// movedVolumes 是要搬的 volume：kind（輔助容器裡的資料夾名稱）→ volume 名稱。cred 不搬（見上）。
func movedVolumes(botID string) []moveVol {
	v := VolumeNames(botID)
	return []moveVol{{"home", v.Home}, {"workspace", v.Workspace}, {"hermes", v.Hermes}}
}

type moveVol struct{ Kind, Name string }

// Moving 回答電腦是不是正在搬家。
func (m *Manager) Moving(botID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.moving[botID]
}

func (m *Manager) setMoving(botID string, on bool) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if on && m.moving[botID] {
		return false
	}
	if on {
		m.moving[botID] = true
	} else {
		delete(m.moving, botID)
	}
	return true
}

// MoveProgress 是搬家的進度。
type MoveProgress struct {
	// Phase：stop（停止電腦）、measure（量資料）、copy（傳資料）、verify（驗證）、switch（切換）、start（在新主機開機）。
	Phase string
	// Volume 是正在傳的資料（home、workspace、hermes）。
	Volume string
	// Done、Total 是已傳與全部的位元組（Total 是估計值）。
	Done, Total int64
}

// 搬家的步驟。
const (
	PhaseStop    = "stop"
	PhaseMeasure = "measure"
	PhaseCopy    = "copy"
	PhaseVerify  = "verify"
	PhaseSwitch  = "switch"
	PhaseStart   = "start"
)

// MoveResult 是一次搬家的結果。
type MoveResult struct {
	From, To   string
	WasRunning bool
	// Empty：Bot 還沒有任何資料（沒建過電腦），只改了放哪台主機。
	Empty bool
	// Files、Bytes 是搬過去並驗證過的檔案數與大小。
	Files int
	Bytes int64
	// Kept：原主機上的資料還留著（等保留期過了再刪）。
	Kept bool
	// StartErr 是在新主機開機失敗的原因（資料已經搬好，Bot 已經在新主機上）。
	StartErr error
}

// Move 把 Bot 的電腦搬到 toHost（見檔案開頭）。呼叫端要先確認 Bot 沒有工作（Activity.Quiesce），並確保新主機上有電腦映像。
// ctx 取消＝中止：還沒切換前都會復原，原主機的資料不動。
func (m *Manager) Move(ctx context.Context, botID, toHost string, report func(MoveProgress)) (MoveResult, error) {
	if report == nil {
		report = func(MoveProgress) {}
	}
	if !m.setMoving(botID, true) {
		return MoveResult{}, ErrMoving
	}
	switched := false
	defer func() {
		if !switched {
			m.setMoving(botID, false)
		}
	}()
	unlock := m.lock(botID)
	b, src, err := m.load(ctx, botID)
	if err != nil {
		unlock()
		return MoveResult{}, err
	}
	dst, err := m.hosts.Get(toHost)
	if err != nil {
		unlock()
		return MoveResult{}, err
	}
	res := MoveResult{From: src.ID(), To: dst.ID()}
	if src.ID() == dst.ID() {
		unlock()
		return res, ErrSameHost
	}
	report(MoveProgress{Phase: PhaseStop})
	c, err := inspect(ctx, src, botID)
	if err != nil {
		unlock()
		return res, err
	}
	res.WasRunning = running(c)
	if res.WasRunning {
		if err := stop(ctx, src, c.ID); err != nil {
			unlock()
			return res, m.fail(ctx, b, err)
		}
		if c, err = inspect(ctx, src, botID); err != nil {
			unlock()
			return res, err
		}
	}
	if _, err := m.sync(ctx, b, src, c); err != nil {
		unlock()
		return res, err
	}
	unlock()

	has, err := hasVolumes(ctx, src.Docker(), botID)
	if err != nil {
		return res, m.restartAfterFailedMove(ctx, botID, res, err)
	}
	if !has {
		// 還沒有任何資料：只改放哪台主機（容器若有也是空的，刪掉）。
		res.Empty = true
		switched = true
		return res, m.switchHost(context.WithoutCancel(ctx), botID, src, dst, &res, false)
	}
	if _, err := dst.Docker().ImageInspect(ctx, m.image); err != nil {
		return res, m.restartAfterFailedMove(ctx, botID, res, fmt.Errorf("新主機上沒有 Bot 電腦映像 %s：%w", m.image, dockerErr(err)))
	}
	files, bytes, err := m.copyVolumes(ctx, botID, src, dst, report)
	if err != nil {
		return res, m.restartAfterFailedMove(ctx, botID, res, err)
	}
	res.Files, res.Bytes = files, bytes
	report(MoveProgress{Phase: PhaseSwitch, Done: bytes, Total: bytes})
	switched = true
	if err := m.switchHost(context.WithoutCancel(ctx), botID, src, dst, &res, true); err != nil {
		return res, err
	}
	return res, nil
}

// restartAfterFailedMove 在搬家失敗後把原本開著的電腦開回來，回傳原本的錯誤。
func (m *Manager) restartAfterFailedMove(ctx context.Context, botID string, res MoveResult, cause error) error {
	if !res.WasRunning {
		return cause
	}
	m.setMoving(botID, false)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	if _, err := m.Start(ctx, botID); err != nil {
		m.log.Warn("搬家失敗後重新開機也失敗", "bot", botID, "err", err)
	}
	return cause
}

func hasVolumes(ctx context.Context, api hosts.DockerAPI, botID string) (bool, error) {
	for _, v := range movedVolumes(botID) {
		_, err := api.VolumeInspect(ctx, v.Name, client.VolumeInspectOptions{})
		if err == nil {
			return true, nil
		}
		if !cerrdefs.IsNotFound(err) {
			return false, dockerErr(err)
		}
	}
	return false, nil
}

// copyVolumes 把三個 volume 從 src 傳到 dst 並驗證；失敗時刪掉 dst 上建的東西。回傳檔案數與大小。
func (m *Manager) copyVolumes(ctx context.Context, botID string, src, dst hosts.Host, report func(MoveProgress)) (files int, total int64, err error) {
	sapi, dapi := src.Docker(), dst.Docker()
	cleanup := func(c context.Context) {
		removeContainer(c, sapi, helperName(botID, "src"))
		removeContainer(c, dapi, helperName(botID, "dst"))
	}
	clean := context.WithoutCancel(ctx)
	defer cleanup(clean)
	// 新主機上可能有上次中斷的搬家或更早搬走後保留的舊資料：不是這個 Bot 現在的資料（它在 src），清掉重來。
	if err := m.clearTarget(ctx, dst, botID); err != nil {
		return 0, 0, err
	}
	created := false
	defer func() {
		if err != nil && created {
			c, cancel := context.WithTimeout(clean, 2*time.Minute)
			defer cancel()
			removeContainer(c, dapi, helperName(botID, "dst"))
			for _, v := range VolumeNames(botID).list() {
				if _, rerr := dapi.VolumeRemove(c, v, client.VolumeRemoveOptions{Force: true}); rerr != nil && !cerrdefs.IsNotFound(rerr) {
					m.log.Warn("搬家失敗，刪除新主機上的資料區失敗", "volume", v, "err", rerr)
				}
			}
		}
	}()

	// ② 量原主機上的資料（給進度條用）。
	report(MoveProgress{Phase: PhaseMeasure})
	removeContainer(ctx, sapi, helperName(botID, "src"))
	if _, err := sapi.ContainerCreate(ctx, helperOptions(m.image, botID, "src", true, measureScript)); err != nil {
		return 0, 0, fmt.Errorf("在原主機建立搬家用的輔助容器失敗：%w", dockerErr(err))
	}
	out, err := runHelper(ctx, sapi, helperName(botID, "src"), 1<<20)
	if err != nil {
		return 0, 0, fmt.Errorf("量原主機上的資料失敗：%w", err)
	}
	total = parseMeasure(out["sizes"])

	// ③ 傳資料。
	created = true
	for _, v := range VolumeNames(botID).list() {
		if err := ensureVolume(ctx, dapi, v, botID); err != nil {
			return 0, 0, err
		}
	}
	removeContainer(ctx, dapi, helperName(botID, "dst"))
	if _, err := dapi.ContainerCreate(ctx, helperOptions(m.image, botID, "dst", false, verifyScript)); err != nil {
		return 0, 0, fmt.Errorf("在新主機建立搬家用的輔助容器失敗：%w", dockerErr(err))
	}
	sent := newManifest()
	var done atomic.Int64
	for _, v := range movedVolumes(botID) {
		stop := ticker(func() { report(MoveProgress{Phase: PhaseCopy, Volume: v.Kind, Done: done.Load(), Total: total}) })
		err := streamVolume(ctx, sapi, dapi, botID, v.Kind, sent, &done)
		stop()
		if err != nil {
			return 0, 0, fmt.Errorf("傳送 %s 失敗：%w", v.Kind, err)
		}
	}
	report(MoveProgress{Phase: PhaseVerify, Done: done.Load(), Total: max(total, done.Load())})

	// ④ 驗證：新主機上算出的清單與檢查碼要和傳過去的一樣。
	out, err = runHelper(ctx, dapi, helperName(botID, "dst"), limits.BoxMoveManifestBytes)
	if err != nil {
		return 0, 0, fmt.Errorf("驗證新主機上的資料失敗：%w", err)
	}
	got, err := parseManifest(out["tree"], out["sums"])
	if err != nil {
		return 0, 0, fmt.Errorf("驗證新主機上的資料失敗：%w", err)
	}
	if err := sent.Compare(got); err != nil {
		return 0, 0, err
	}
	n, size := sent.Totals()
	return n, size, nil
}

// clearTarget 清掉新主機上這個 Bot 留下的舊容器、輔助容器與 volume。
func (m *Manager) clearTarget(ctx context.Context, h hosts.Host, botID string) error {
	api := h.Docker()
	removeContainer(ctx, api, helperName(botID, "dst"))
	c, err := inspect(ctx, h, botID)
	if err != nil {
		return err
	}
	if c != nil {
		if _, err := api.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("刪除新主機上的舊電腦失敗：%w", dockerErr(err))
		}
	}
	for _, v := range append(VolumeNames(botID).list(), MovedMarker(botID)) {
		if _, err := api.VolumeRemove(ctx, v, client.VolumeRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("刪除新主機上的舊資料 %s 失敗：%w", v, dockerErr(err))
		}
	}
	return nil
}

// switchHost 把 Bot 換到新主機（資料已經在那邊）：改資料庫、收掉原主機的容器與轉發、留下標記，再在新主機建好（或開機）。
func (m *Manager) switchHost(ctx context.Context, botID string, src, dst hosts.Host, res *MoveResult, kept bool) error {
	defer m.lock(botID)()
	defer m.setMoving(botID, false)
	old, err := m.db.GetBox(ctx, botID)
	if errors.Is(err, store.ErrNotFound) {
		old, err = store.Box{BotID: botID, State: StateNone}, nil
	}
	if err != nil {
		return err
	}
	nb := old
	nb.HostID, nb.ContainerID, nb.State, nb.Error, nb.NetIP, nb.NoVNCPort, nb.HermesPort = dst.ID(), "", StateNone, "", "", 0, 0
	err = m.db.Tx(ctx, func(q *store.Queries) error {
		if err := q.SetBotHost(ctx, botID, dst.ID()); err != nil {
			return err
		}
		return q.UpsertBox(ctx, nb)
	})
	if err != nil {
		return fmt.Errorf("切換到新主機失敗（資料兩邊都在，原主機的沒動）：%w", err)
	}
	m.log.Info("電腦搬家：已切換到新主機", "bot", botID, "from", src.ID(), "to", dst.ID(), "files", res.Files, "bytes", res.Bytes)
	// 收掉原主機這邊（失敗只記錄：資料已經在新主機，Bot 已經換過去）。
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	sapi := src.Docker()
	if c, err := inspect(cctx, src, botID); err == nil && c != nil {
		if _, err := sapi.ContainerRemove(cctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			m.log.Warn("搬家後刪除原主機上的電腦失敗", "bot", botID, "host", src.ID(), "err", err)
		}
	}
	m.releasePorts(cctx, src, botID, []int{old.NoVNCPort, old.HermesPort})
	if f, ok := src.(interface{ ReleaseIPC(botID string) }); ok {
		f.ReleaseIPC(botID) // 遠端主機：停掉 product.sock 的反向轉發（本機的 socket 本身要留著，新主機也接它）
	}
	if !m.noGuard && old.NetIP != "" {
		if boxes, err := m.hostRules(cctx, src.ID()); err == nil {
			if _, err := m.ensureNet(cctx, src, boxes); err != nil {
				m.log.Warn("搬家後更新原主機的網路防護規則失敗", "host", src.ID(), "err", err)
			}
		}
	}
	if kept {
		res.Kept = true
		labels := map[string]string{LabelManaged: "true", LabelBot: botID, LabelMovedTo: dst.ID(), LabelMovedAt: strconv.FormatInt(time.Now().UnixMilli(), 10)}
		_, _ = sapi.VolumeRemove(cctx, MovedMarker(botID), client.VolumeRemoveOptions{})
		if _, err := sapi.VolumeCreate(cctx, client.VolumeCreateOptions{Name: MovedMarker(botID), Labels: labels}); err != nil {
			m.log.Warn("在原主機留下搬家標記失敗", "bot", botID, "err", err)
		}
	} else {
		for _, v := range VolumeNames(botID).list() {
			_, _ = sapi.VolumeRemove(cctx, v, client.VolumeRemoveOptions{})
		}
	}
	if m.notify != nil {
		m.notify(status(nb, dst, false))
	}
	// 在新主機：原本開著的開機（不等桌面就緒），沒開的先建好容器。
	var c *container.InspectResponse
	if res.WasRunning {
		_, res.StartErr = m.start(ctx, nb, dst)
	} else if c, res.StartErr = m.ensure(ctx, &nb, dst); res.StartErr == nil {
		_, res.StartErr = m.sync(ctx, nb, dst, c)
	}
	if res.StartErr != nil {
		m.log.Warn("搬家後在新主機開機失敗", "bot", botID, "host", dst.ID(), "err", res.StartErr)
	}
	return nil
}

// DropMoved 刪掉搬走後留在 hostID 上的舊資料（保留期過了；computers 的背景工作呼叫）。
// Bot 現在的電腦就在這台時什麼都不刪（又搬回來了，那些就是現在的資料）。
func (m *Manager) DropMoved(ctx context.Context, hostID, botID string) error {
	defer m.lock(botID)()
	if m.Moving(botID) {
		return ErrMoving
	}
	if b, err := m.db.GetBox(ctx, botID); err == nil && b.HostID == hostID {
		return nil
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	h, err := m.hosts.Get(hostID)
	if err != nil {
		return err
	}
	api := h.Docker()
	removeContainer(ctx, api, helperName(botID, "src"))
	removeContainer(ctx, api, helperName(botID, "dst"))
	if c, err := inspect(ctx, h, botID); err == nil && c != nil {
		if _, err := api.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true}); err != nil && !cerrdefs.IsNotFound(err) {
			return dockerErr(err)
		}
	}
	for _, v := range append(VolumeNames(botID).list(), MovedMarker(botID)) {
		if _, err := api.VolumeRemove(ctx, v, client.VolumeRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("刪除舊資料 %s 失敗：%w", v, dockerErr(err))
		}
	}
	return nil
}

func removeContainer(ctx context.Context, api hosts.DockerAPI, name string) {
	_, _ = api.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true})
}

// ticker 每 limits.BoxMoveProgressEvery 呼叫一次 f，直到回傳的 stop 被呼叫（stop 會再呼叫最後一次）。
func ticker(f func()) (stop func()) {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(limits.BoxMoveProgressEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				f()
			}
		}
	}()
	return func() {
		close(done)
		<-finished
		f()
	}
}

// ── 輔助容器 ─────────────────────────────────────────

// measureScript 在原主機量資料：每個一般檔案的大小一行。
const measureScript = `set -e
mkdir -p ` + moveOut + `
cd ` + moveRoot + `
find home workspace hermes -xdev -type f -printf '%s\n' > ` + moveOut + `/sizes`

// verifyScript 在新主機算檔案清單（種類、大小、路徑）與每個一般檔案的 SHA-256（NUL 分隔，檔名不跳脫）。
const verifyScript = `set -e
mkdir -p ` + moveOut + `
cd ` + moveRoot + `
find home workspace hermes -xdev -printf '%y %s %p\0' > ` + moveOut + `/tree
find home workspace hermes -xdev -type f -print0 | xargs -0r sha256sum -z > ` + moveOut + `/sums`

// helperOptions 是搬家用的輔助容器：Bot 電腦映像、沒有網路、只多一個「讀任何檔案」的 capability（算檢查碼要讀
// Bot 使用者 0600 的檔案），把三個 volume 掛在 /xbot-move/<kind>（原主機唯讀）。容器裡只跑上面固定的腳本。
func helperOptions(image, botID, side string, readOnly bool, script string) client.ContainerCreateOptions {
	pids := int64(256)
	var mounts []mount.Mount
	for _, v := range movedVolumes(botID) {
		mounts = append(mounts, mount.Mount{Type: mount.TypeVolume, Source: v.Name, Target: path.Join(moveRoot, v.Kind), ReadOnly: readOnly})
	}
	return client.ContainerCreateOptions{
		Name: helperName(botID, side),
		Config: &container.Config{
			Image:           image,
			User:            "0:0",
			Entrypoint:      []string{"/bin/sh", "-c"},
			Cmd:             []string{script},
			NetworkDisabled: true,
			Labels:          map[string]string{LabelManaged: "true", LabelBot: botID, LabelHelper: "move-" + side},
		},
		HostConfig: &container.HostConfig{
			NetworkMode: "none",
			CapDrop:     []string{"ALL"},
			CapAdd:      []string{"DAC_READ_SEARCH"},
			SecurityOpt: secOpt,
			Mounts:      mounts,
			Resources:   container.Resources{Memory: 512 << 20, PidsLimit: &pids},
		},
	}
}

// runHelper 開機輔助容器、等它跑完，讀回 /xbot-out 裡的檔案（每個最多 max 位元組）。
func runHelper(ctx context.Context, api hosts.DockerAPI, name string, max int64) (map[string][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, limits.BoxMoveHelperTimeout)
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
				return nil, fmt.Errorf("輔助容器結束碼 %d %s", st.ExitCode, st.Error)
			}
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	rd, err := api.CopyFromContainer(ctx, name, client.CopyFromContainerOptions{SourcePath: moveOut})
	if err != nil {
		return nil, dockerErr(err)
	}
	defer rd.Content.Close()
	out := map[string][]byte{}
	tr := tar.NewReader(rd.Content)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if h.Size > max {
			return nil, fmt.Errorf("%s 太大（%d 位元組）", h.Name, h.Size)
		}
		b, err := io.ReadAll(io.LimitReader(tr, max))
		if err != nil {
			return nil, err
		}
		out[path.Base(h.Name)] = b
	}
}

func parseMeasure(b []byte) int64 {
	var total int64
	for _, line := range strings.Split(string(b), "\n") {
		if n, err := strconv.ParseInt(strings.TrimSpace(line), 10, 64); err == nil {
			total += n
		}
	}
	return total
}

// streamVolume 把一個 volume 從原主機的輔助容器串到新主機的輔助容器，途中記下每個項目與一般檔案的 SHA-256。
func streamVolume(ctx context.Context, sapi, dapi hosts.DockerAPI, botID, kind string, sent *Manifest, done *atomic.Int64) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rd, err := sapi.CopyFromContainer(ctx, helperName(botID, "src"), client.CopyFromContainerOptions{SourcePath: path.Join(moveRoot, kind)})
	if err != nil {
		return dockerErr(err)
	}
	defer rd.Content.Close()
	pr, pw := io.Pipe()
	parsed := make(chan error, 1)
	go func() {
		err := sent.Read(io.TeeReader(&counter{r: rd.Content, n: done}, pw))
		pw.CloseWithError(err)
		parsed <- err
	}()
	_, err = dapi.CopyToContainer(ctx, helperName(botID, "dst"), client.CopyToContainerOptions{DestinationPath: moveRoot, Content: pr})
	if err != nil {
		pr.CloseWithError(err)
		cancel()
		<-parsed
		return dockerErr(err)
	}
	// 新主機讀完 tar 結尾就回應了：把剩下的（結尾的補零）讀完，才知道整份都正確。
	_, _ = io.Copy(io.Discard, pr)
	return <-parsed
}

type counter struct {
	r io.Reader
	n *atomic.Int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// ── 清單比對 ─────────────────────────────────────────

// Entry 是清單裡的一個項目：種類（f 一般檔案、d 資料夾、l 符號連結）、大小（只對一般檔案有意義）、SHA-256。
type Entry struct {
	Type byte
	Size int64
	Sum  string
}

// Manifest 是一份檔案清單（路徑 → 項目），路徑相對於 /xbot-move（例：home/chrome-profile/Default/Cookies）。
type Manifest struct {
	Entries map[string]Entry
	links   map[string]string // 硬連結：路徑 → 連到的路徑（檢查碼等讀完再補）
}

func newManifest() *Manifest {
	return &Manifest{Entries: map[string]Entry{}, links: map[string]string{}}
}

func cleanName(n string) string {
	return strings.TrimSuffix(strings.TrimPrefix(path.Clean("/"+n), "/"), "/")
}

// Read 讀一份 tar（docker cp 的格式），記下每個項目與一般檔案的 SHA-256。讀到 tar 結尾後把剩下的資料也讀完。
func (m *Manifest) Read(r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := cleanName(h.Name)
		switch h.Typeflag {
		case tar.TypeReg:
			sum := sha256.New()
			n, err := io.Copy(sum, tr)
			if err != nil {
				return err
			}
			m.Entries[name] = Entry{Type: 'f', Size: n, Sum: hex.EncodeToString(sum.Sum(nil))}
		case tar.TypeDir:
			m.Entries[name] = Entry{Type: 'd'}
		case tar.TypeSymlink:
			m.Entries[name] = Entry{Type: 'l'}
		case tar.TypeLink:
			m.links[name] = cleanName(h.Linkname)
		}
	}
	for name, target := range m.links {
		if e, ok := m.Entries[target]; ok {
			m.Entries[name] = e
		}
	}
	clear(m.links)
	_, err := io.Copy(io.Discard, r)
	return err
}

// parseManifest 讀新主機輔助容器算出的清單：tree 是「種類 大小 路徑\0」，sums 是 sha256sum -z 的輸出。
func parseManifest(tree, sums []byte) (*Manifest, error) {
	m := newManifest()
	for _, rec := range bytes.Split(tree, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		f := strings.SplitN(string(rec), " ", 3)
		if len(f) != 3 || len(f[0]) != 1 {
			return nil, fmt.Errorf("看不懂的清單：%q", rec)
		}
		size, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("看不懂的清單：%q", rec)
		}
		t := f[0][0]
		if t != 'f' && t != 'd' && t != 'l' {
			continue
		}
		e := Entry{Type: t}
		if t == 'f' {
			e.Size = size
		}
		m.Entries[cleanName(f[2])] = e
	}
	for _, rec := range bytes.Split(sums, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		s := string(rec)
		if len(s) < 67 || s[64:66] != "  " {
			return nil, fmt.Errorf("看不懂的檢查碼：%q", rec)
		}
		name := cleanName(s[66:])
		e, ok := m.Entries[name]
		if !ok || e.Type != 'f' {
			return nil, fmt.Errorf("檢查碼裡有清單沒有的檔案：%s", name)
		}
		e.Sum = s[:64]
		m.Entries[name] = e
	}
	return m, nil
}

// Totals 回傳一般檔案的數量與總大小。
func (m *Manifest) Totals() (files int, size int64) {
	for _, e := range m.Entries {
		if e.Type == 'f' {
			files++
			size += e.Size
		}
	}
	return files, size
}

// ErrVerify 表示新主機上的資料和傳過去的不一樣。
var ErrVerify = errors.New("新主機上的資料和原本的不一樣")

// Compare 確認 got（新主機上的）和 m（傳過去的）完全一樣：同樣的項目、種類、檔案大小與 SHA-256。
func (m *Manifest) Compare(got *Manifest) error {
	var diffs []string
	for _, name := range sortedNames(m.Entries) {
		want := m.Entries[name]
		g, ok := got.Entries[name]
		switch {
		case !ok:
			diffs = append(diffs, "少了 "+name)
		case g.Type != want.Type:
			diffs = append(diffs, fmt.Sprintf("%s 種類不同（%c → %c）", name, want.Type, g.Type))
		case want.Type == 'f' && g.Size != want.Size:
			diffs = append(diffs, fmt.Sprintf("%s 大小不同（%d → %d）", name, want.Size, g.Size))
		case want.Type == 'f' && g.Sum != want.Sum:
			diffs = append(diffs, name+" 內容不同")
		}
	}
	for _, name := range sortedNames(got.Entries) {
		if _, ok := m.Entries[name]; !ok {
			diffs = append(diffs, "多了 "+name)
		}
	}
	if len(diffs) == 0 {
		return nil
	}
	n := len(diffs)
	if n > 5 {
		diffs = append(diffs[:5], fmt.Sprintf("……共 %d 處", n))
	}
	return fmt.Errorf("%w：%s", ErrVerify, strings.Join(diffs, "；"))
}

func sortedNames(m map[string]Entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return slices.Clip(out)
}
