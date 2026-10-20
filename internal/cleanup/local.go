package cleanup

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"

	"xbot/internal/box"
	"xbot/internal/boxfs"
)

// Local 把這台機器上的資料夾 root 當成一台電腦：root/hermes、root/home、root/workspace 是三個 volume，
// 指令直接在這台機器上執行（測試用，同 collab.DirFiles）。要和 Bot 電腦一樣的 GNU 工具（見 LocalSupported）。
func Local(root string) box.Shell {
	return box.Shell{Run: runLocal, Hermes: filepath.Join(root, "hermes"), Home: filepath.Join(root, "home"),
		Workspace: filepath.Join(root, "workspace")}
}

// LocalSupported 回答這台機器能不能跑 Local 的指令：find 的 -printf、-fprintf、-newermt，以及 head、sort、grep、xargs、rmdir 的 GNU 參數。
func LocalSupported() error {
	probe := `find . -maxdepth 0 -newermt @0 -printf '%s\n' -fprintf /dev/null '%h\0' >/dev/null && ` +
		`printf 'a\0b\0' | head -z -n 2 | sort -zur | grep -zvxF -- a | xargs -0r echo >/dev/null`
	if out, err := exec.Command("sh", "-c", probe).CombinedOutput(); err != nil {
		return errors.New("這台機器的 find 等工具不支援電腦裡用到的參數：" + string(out))
	}
	return nil
}

func runLocal(ctx context.Context, max int64, cmd ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	var out, stderr bytes.Buffer
	c.Stdout, c.Stderr = &out, &stderr
	err := c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.Bytes(), &boxfs.ExitError{Cmd: cmd[0], Code: ee.ExitCode(), Stderr: stderr.String()}
	}
	if err != nil {
		return nil, err
	}
	if int64(out.Len()) > max {
		return nil, errors.New("輸出太多")
	}
	return out.Bytes(), nil
}
