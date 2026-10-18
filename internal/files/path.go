package files

import (
	"errors"
	"path"
	"strings"
	"unicode/utf8"

	"xbot/internal/collab"
	"xbot/internal/limits"
)

// Root 是 Bot 電腦裡給你看的資料夾。檔案面板只能碰它底下的東西（LazyBoy files.contained）。
const Root = "/workspace"

// ErrOutside 表示路徑不在 /workspace 裡面。
var ErrOutside = errors.New("檔案必須位於這個 Bot 的工作目錄（/workspace）")

// Rel 把呼叫端給的路徑整理成 /workspace 底下的相對路徑；"" 表示 /workspace 本身。
// 可以是完整路徑（/workspace/報告.md）或相對路徑（報告.md）。有 ..、NUL、控制字元、太長或太深的一律拒絕：
// 不靠「整理後還在裡面」放行，免得 a/../../x 這類寫法在別的地方被解讀成不同的路徑。
func Rel(p string) (string, error) {
	p = strings.TrimSpace(p)
	if len(p) > limits.FilePathMax || !utf8.ValidString(p) {
		return "", errors.New("檔案路徑太長或不是有效的文字")
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("檔案路徑不能有控制字元")
		}
	}
	switch {
	case p == Root || p == Root+"/":
		return "", nil
	case strings.HasPrefix(p, Root+"/"):
		p = p[len(Root)+1:]
	case strings.HasPrefix(p, "/"):
		return "", ErrOutside
	}
	parts := strings.Split(p, "/")
	for _, s := range parts {
		if s == ".." {
			return "", ErrOutside
		}
		if len(s) > 255 {
			return "", errors.New("檔名太長")
		}
	}
	c := strings.TrimPrefix(path.Clean("/"+p), "/")
	if strings.Count(c, "/") >= limits.FilePathDepth {
		return "", errors.New("資料夾太深")
	}
	return c, nil
}

// Abs 是相對路徑在 Bot 電腦裡的完整路徑。
func Abs(rel string) string {
	if rel == "" {
		return Root
	}
	return Root + "/" + rel
}

// Filename 把上傳的檔名整理成一段安全的名稱（LazyBoy files.filename）。
func Filename(name string) string {
	// 瀏覽器在 Windows 可能送來完整路徑：只留最後一段。
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	return collab.Filename(name)
}

// split 把相對路徑拆成每一段；"" 是空的。
func split(rel string) []string {
	if rel == "" {
		return nil
	}
	return strings.Split(rel, "/")
}
