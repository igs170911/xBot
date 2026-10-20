package box

import (
	"context"
	"fmt"
	"path"
	"strconv"

	"github.com/moby/moby/client"

	"xbot/internal/boxfs"
	"xbot/internal/hosts"
	"xbot/internal/limits"
)

// Shell 在一個 Bot 的電腦資料上執行固定的小指令（例行清理與量用量，BX-14）。
//
// LazyBoy 直接讀寫主機上的資料夾；XBot 的資料在 volume 裡、可能在遠端主機，所以一律經 Docker API 在容器裡執行：
// 電腦開著時在它自己的容器裡，沒開機時在一個輔助容器裡（同一個映像、沒有網路、沒有 capability，
// 三個 volume 掛在 /xbot-move/<kind>），不會為了清理把電腦叫醒。兩種都以 Bot 使用者執行：
// 就算 Bot 在電腦裡放了連結，也只碰得到它自己本來就碰得到的東西。
type Shell struct {
	// Run 執行一個指令，回傳 stdout（超過 max 位元組時回錯誤）；結束碼不是 0 時回 *boxfs.ExitError。
	Run func(ctx context.Context, max int64, cmd ...string) ([]byte, error)
	// Hermes、Home、Workspace 是三個 volume 在這個容器裡的位置。
	Hermes, Home, Workspace string
	// Live：在 Bot 自己開著的電腦裡（瀏覽器可能正在用它的快取）。
	Live bool
}

func cleanHelper(botID string) string { return "xbot-clean-" + botID }

// WithShell 找到 Bot 的電腦資料並呼叫 fn（見 Shell）。同一個 Bot 一次只有一個 fn 在跑。
// write 為 true 而電腦沒開機時，fn 執行期間電腦不會開機（Start 等它做完，避免邊開機邊刪瀏覽器快取）；
// 電腦開著時不擋（LazyBoy 也是只在開始前看 Bot 有沒有在工作）。
// 還沒有任何電腦資料時回 ErrNoData；正在搬家時回 ErrMoving。
func (m *Manager) WithShell(ctx context.Context, botID string, write bool, fn func(context.Context, Shell) error) error {
	defer m.keyLock(m.shells, botID)()
	unlock := m.lock(botID)
	locked := true
	release := func() {
		if locked {
			unlock()
			locked = false
		}
	}
	defer release()
	if m.Moving(botID) {
		return ErrMoving
	}
	_, h, err := m.load(ctx, botID)
	if err != nil {
		return err
	}
	c, err := inspect(ctx, h, botID)
	if err != nil {
		return err
	}
	api := h.Docker()
	if running(c) {
		release()
		return fn(ctx, Shell{Run: runIn(api, c.ID), Hermes: HermesDir, Home: HomeDir, Workspace: WorkspaceDir, Live: true})
	}
	if !write {
		release()
	}
	has, err := hasVolumes(ctx, api, botID)
	if err != nil {
		return err
	}
	if !has {
		return ErrNoData
	}
	for _, v := range movedVolumes(botID) { // 只有部分 volume 時補建成空的（同 ExportVolumes）
		if err := ensureVolume(ctx, api, v.Name, botID); err != nil {
			return err
		}
	}
	name := cleanHelper(botID)
	removeContainer(ctx, api, name)
	if _, err := api.ContainerCreate(ctx, shellHelperOptions(m.image, botID, name, !write)); err != nil {
		return fmt.Errorf("建立清理用的輔助容器失敗：%w", dockerErr(err))
	}
	defer removeContainer(context.WithoutCancel(ctx), api, name)
	if _, err := api.ContainerStart(ctx, name, client.ContainerStartOptions{}); err != nil {
		return fmt.Errorf("開啟清理用的輔助容器失敗：%w", dockerErr(err))
	}
	return fn(ctx, Shell{Run: runIn(api, name), Hermes: path.Join(moveRoot, "hermes"), Home: path.Join(moveRoot, "home"),
		Workspace: path.Join(moveRoot, "workspace")})
}

// shellHelperOptions 是 WithShell 的輔助容器：和搬家的一樣沒有網路，但以 Bot 使用者執行、一個 capability 也不給，
// 根檔案系統唯讀（/tmp 是小 tmpfs）。它只等著被 exec，最多 limits.CleanupBotTimeout 就自己結束。
func shellHelperOptions(image, botID, name string, readOnly bool) client.ContainerCreateOptions {
	o := helperOptions(image, botID, "", readOnly, "exec sleep "+strconv.Itoa(int(limits.CleanupBotTimeout.Seconds())))
	o.Name = name
	o.Config.User = strconv.Itoa(BotUID) + ":" + strconv.Itoa(BotGID)
	o.Config.Labels[LabelHelper] = "clean"
	o.HostConfig.CapAdd = nil
	o.HostConfig.ReadonlyRootfs = true
	o.HostConfig.Tmpfs = map[string]string{"/tmp": fmt.Sprintf("rw,noexec,nosuid,nodev,mode=1777,size=%d", limits.CleanupHelperTmpBytes)}
	return o
}

// runIn 在容器 id 裡以 Bot 使用者執行指令。
func runIn(api hosts.DockerAPI, id string) func(ctx context.Context, max int64, cmd ...string) ([]byte, error) {
	d := &boxfs.Docker{API: api, ID: id, UID: BotUID, GID: BotGID, Running: true}
	user := strconv.Itoa(BotUID) + ":" + strconv.Itoa(BotGID)
	return func(ctx context.Context, max int64, cmd ...string) ([]byte, error) {
		return d.Output(ctx, user, max, cmd...)
	}
}
