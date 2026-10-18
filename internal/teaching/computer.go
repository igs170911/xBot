package teaching

import (
	"context"
	"errors"
	"strconv"

	"xbot/internal/box"
	"xbot/internal/boxfs"
)

// Computer 是一台 Bot 的電腦：讀寫檔案，並以 Bot 使用者（1000:1000）執行指令。
type Computer interface {
	boxfs.FS
	// Run 在電腦裡以 Bot 使用者執行一個指令（不經 shell 解讀參數）；結束碼不是 0 時回錯誤。
	Run(ctx context.Context, cmd ...string) error
}

// Opener 找 Bot 的電腦。wake 為 true 時沒開機就先開機（還沒有電腦時先建立）；為 false 時不開機（讀寫檔案照常，Run 回
// boxfs.ErrNotRunning；還沒有電腦時回 boxfs.ErrNoComputer）。
type Opener func(ctx context.Context, botID string, wake bool) (Computer, error)

// DockerOpener 用 runtime.Computers.BotFS 找電腦；ready（可為 nil）在開機後等桌面就緒（computers.Service.Wake）。
func DockerOpener(locate boxfs.Locator, ready func(ctx context.Context, botID string) error) Opener {
	user := strconv.Itoa(box.BotUID) + ":" + strconv.Itoa(box.BotGID)
	return func(ctx context.Context, botID string, wake bool) (Computer, error) {
		if wake && ready != nil {
			// 開機（或已開機）並等桌面就緒；之後再找電腦（容器 id 可能換了）。
			if err := ready(ctx, botID); err != nil {
				return nil, err
			}
		}
		f, err := locate(ctx, botID, wake)
		if err != nil {
			return nil, err
		}
		dk, ok := f.(*boxfs.Docker)
		if !ok {
			return nil, errors.New("這台電腦不能執行指令")
		}
		if wake {
			if err := dk.EnsureRunning(ctx); err != nil {
				return nil, err
			}
		}
		return dockerComputer{Docker: dk, user: user}, nil
	}
}

type dockerComputer struct {
	*boxfs.Docker
	user string
}

func (d dockerComputer) Run(ctx context.Context, cmd ...string) error {
	_, err := d.Output(ctx, d.user, 64<<10, cmd...)
	return err
}
