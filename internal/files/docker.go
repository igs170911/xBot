package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"

	"xbot/internal/boxfs"
	"xbot/internal/limits"
)

// Docker 是 Bot 電腦（容器）裡的 /workspace。
//
// 讀寫走 Docker 的檔案 API（關機中也可以；本機與 SSH 遠端主機一樣），每一段路徑先看過不是連結才開。
// 列資料夾、搬移、刪除要在電腦裡執行指令（Docker 沒有這些 API），一律用 Bot 自己的使用者執行：
// 就算 Bot 在檢查之後偷換成連結，也只碰得到它自己本來就碰得到的東西。
type Docker struct {
	FS *boxfs.Docker
	// User 是在電腦裡執行指令的使用者（uid:gid）。
	User string
}

// DockerLocator 用 boxfs.Locator（runtime.Computers.BotFS）找 Bot 的 /workspace。
func DockerLocator(loc boxfs.Locator, user string) Locator {
	return func(ctx context.Context, botID string, wake bool) (Workspace, error) {
		f, err := loc(ctx, botID, false)
		if err != nil {
			return nil, err
		}
		d, ok := f.(*boxfs.Docker)
		if !ok {
			return nil, fmt.Errorf("不支援的 Bot 電腦：%T", f)
		}
		if wake {
			if err := d.EnsureRunning(ctx); err != nil {
				return nil, err
			}
		}
		return &Docker{FS: d, User: user}, nil
	}
}

func isLink(st container.PathStat) bool {
	return st.LinkTarget != "" || st.Mode&fs.ModeSymlink != 0
}

// check 一段一段看路徑（ContainerStatPath 只會告訴你最後一段是不是連結）。
// 回傳最後一段的資訊與存在的段數；allowMissing 為 false 時不存在回 ErrNotFound。
func (d *Docker) check(ctx context.Context, rel string, allowMissing bool) (*container.PathStat, int, error) {
	parts := split(rel)
	var last *container.PathStat
	for i := range parts {
		st, err := d.FS.Stat(ctx, Abs(strings.Join(parts[:i+1], "/")))
		if errors.Is(err, boxfs.ErrNotExist) {
			if allowMissing {
				return nil, i, nil
			}
			return nil, i, ErrNotFound
		}
		if err != nil {
			return nil, i, err
		}
		if isLink(st) {
			return nil, i, ErrLink
		}
		if i < len(parts)-1 && !st.Mode.IsDir() {
			return nil, i, ErrNotDir
		}
		last = &st
	}
	if last == nil { // /workspace 本身
		st, err := d.FS.Stat(ctx, Root)
		if err != nil {
			return nil, 0, err
		}
		last = &st
	}
	return last, len(parts), nil
}

func statInfo(rel string, st container.PathStat) Info {
	return Info{Rel: rel, Dir: st.Mode.IsDir(), Size: st.Size, Modified: st.Mtime}
}

// Stat 實作 Workspace。
func (d *Docker) Stat(ctx context.Context, rel string) (Info, error) {
	st, _, err := d.check(ctx, rel, false)
	if err != nil {
		return Info{}, err
	}
	return statInfo(rel, *st), nil
}

// Open 實作 Workspace。
func (d *Docker) Open(ctx context.Context, rel string) (io.ReadCloser, Info, error) {
	st, _, err := d.check(ctx, rel, false)
	if err != nil {
		return nil, Info{}, err
	}
	if st.Mode.IsDir() {
		return nil, Info{}, ErrIsDir
	}
	if !st.Mode.IsRegular() {
		return nil, Info{}, ErrNotFound
	}
	rc, st2, err := d.FS.Open(ctx, Abs(rel))
	if errors.Is(err, boxfs.ErrNotExist) {
		return nil, Info{}, ErrNotFound
	}
	if err != nil {
		return nil, Info{}, err
	}
	return rc, statInfo(rel, st2), nil
}

// Write 實作 Workspace。
func (d *Docker) Write(ctx context.Context, rel string, size int64, r io.Reader) error {
	if rel == "" {
		return ErrIsDir
	}
	st, n, err := d.check(ctx, rel, true)
	if err != nil {
		return err
	}
	if st != nil && st.Mode.IsDir() {
		return ErrIsDir
	}
	parts := split(rel)
	if n == len(parts) { // 檔案已經存在：寫到它所在的資料夾
		n--
	}
	return d.FS.WriteStream(ctx, Abs(strings.Join(parts[:n], "/")), strings.Join(parts[n:], "/"), size, 0o644, r)
}

// Mkdir 實作 Workspace。
func (d *Docker) Mkdir(ctx context.Context, rel string) error {
	st, n, err := d.check(ctx, rel, true)
	if err != nil {
		return err
	}
	parts := split(rel)
	if n == len(parts) {
		if st.Mode.IsDir() {
			return nil
		}
		return ErrExists
	}
	return d.FS.Mkdir(ctx, Abs(strings.Join(parts[:n], "/")), strings.Join(parts[n:], "/"))
}

// 在電腦裡執行的小程式。$1、$2 是完整路徑（以參數傳入，不會被當成指令）。
// 結束碼：3 不存在、4 經過連結、5 目的地已經存在、6 不是資料夾。
const (
	// nolink：從這個路徑往上到 /workspace，每一段都不能是連結（用 lstat，不跟著連結走）。
	shNoLink = `nolink() { p=$1; while [ "$p" != /workspace ] && [ "$p" != / ] && [ "$p" != . ]; do [ -L "$p" ] && exit 4; p=$(dirname -- "$p"); done; }; `

	shList = shNoLink + `nolink "$1"; [ -e "$1" ] || exit 3; [ -d "$1" ] || exit 6; cd -- "$1" || exit 3; ` +
		`find . -mindepth 1 -maxdepth 1 \( -type f -o -type d \) -printf '%y\t%s\t%T@\t%f\0' | head -z -n "$2"`

	shWalk = `cd -- /workspace || exit 3; find . -mindepth 1 \( -type d \( -name '.*' ` + prune + ` \) -prune \) ` +
		`-o \( -type f ! -name '.*' -printf 'f\t%s\t%T@\t%P\0' \) | head -z -n "$1"`

	shMove = shNoLink + `nolink "$1"; [ -e "$1" ] || exit 3; { [ -e "$2" ] || [ -L "$2" ]; } && exit 5; ` +
		`q=$(dirname -- "$2"); nolink "$q"; mkdir -p -- "$q" || exit 6; nolink "$q"; [ -d "$q" ] || exit 6; mv -T -n -- "$1" "$2"`

	shRemove = shNoLink + `nolink "$(dirname -- "$1")"; { [ -e "$1" ] || [ -L "$1" ]; } || exit 3; rm -rf -- "$1"`
)

// prune 是 walk 不進去的套件資料夾（同 pruneDirs）。
const prune = `-o -iname node_modules -o -iname __pycache__ -o -iname venv -o -iname env -o -iname site-packages ` +
	`-o -iname bower_components -o -iname target`

func (d *Docker) sh(ctx context.Context, script string, args ...string) ([]byte, error) {
	out, err := d.FS.Output(ctx, d.User, limits.FilesListOutputBytes, append([]string{"sh", "-c", script, "sh"}, args...)...)
	var ee *boxfs.ExitError
	if errors.As(err, &ee) {
		switch ee.Code {
		case 3:
			return nil, ErrNotFound
		case 4:
			return nil, ErrLink
		case 5:
			return nil, ErrExists
		case 6:
			return nil, ErrNotDir
		}
	}
	return out, err
}

// parse 讀 find -printf '%y\t%s\t%T@\t<名稱>\0' 的輸出。
func parse(out []byte, dir string) []Info {
	var list []Info
	for rec := range bytes.SplitSeq(out, []byte{0}) {
		f := strings.SplitN(string(rec), "\t", 4)
		if len(f) != 4 || f[3] == "" {
			continue
		}
		size, _ := strconv.ParseInt(f[1], 10, 64)
		var mt time.Time
		if s, err := strconv.ParseFloat(f[2], 64); err == nil {
			mt = time.UnixMilli(int64(s * 1000))
		}
		list = append(list, Info{Rel: join(dir, f[3]), Dir: f[0] == "d", Size: size, Modified: mt})
	}
	return list
}

// List 實作 Workspace。
func (d *Docker) List(ctx context.Context, rel string) ([]Info, error) {
	out, err := d.sh(ctx, shList, Abs(rel), strconv.Itoa(limits.FilesFolderScanMax))
	if err != nil {
		return nil, err
	}
	return parse(out, rel), nil
}

// Walk 實作 Workspace。
func (d *Docker) Walk(ctx context.Context) ([]Info, error) {
	out, err := d.sh(ctx, shWalk, strconv.Itoa(limits.FilesWalkScanMax))
	if err != nil {
		return nil, err
	}
	return parse(out, ""), nil
}

// Move 實作 Workspace。
func (d *Docker) Move(ctx context.Context, from, to string) error {
	_, err := d.sh(ctx, shMove, Abs(from), Abs(to))
	return err
}

// Remove 實作 Workspace。
func (d *Docker) Remove(ctx context.Context, rel string) error {
	if rel == "" {
		return errors.New("不能刪除整個工作目錄")
	}
	_, err := d.sh(ctx, shRemove, Abs(rel))
	return err
}
