package screen

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/hosts"
	"xbot/internal/limits"
)

// 查完網頁沒關的分頁會一直開著（影片、會自己更新的頁面一直跑）：一鍵關掉，只留一個空白分頁。
// 經 Chromium 的 DevTools（容器裡的 127.0.0.1:9222，desktop_mcp 與 Hermes 的瀏覽器用的同一個）。

// closeTabsScript 先開一個空白分頁，再關掉其他所有分頁；印出關了幾個。
const closeTabsScript = `import json, urllib.request as u
B = "http://127.0.0.1:9222"
def call(path, method="GET"):
    return u.urlopen(u.Request(B + path, method=method), timeout=3).read()
pages = [t for t in json.loads(call("/json/list")) if t.get("type") == "page"]
keep = json.loads(call("/json/new?about:blank", "PUT"))["id"]
n = 0
for t in pages:
    if t["id"] != keep:
        call("/json/close/" + t["id"])
        n += 1
print(n)
`

// CloseTabs 關掉 Bot 電腦瀏覽器裡的分頁（留一個空白分頁），回傳關了幾個。電腦沒開機時回 ErrNotRunning，不會叫醒它。
func (s *Service) CloseTabs(ctx context.Context, botID string) (int, error) {
	st, err := s.o.Boxes.Status(ctx, botID)
	if err != nil {
		return 0, err
	}
	if !st.Running || st.ContainerID == "" {
		return 0, ErrNotRunning
	}
	h, err := s.o.Hosts.Get(st.HostID)
	if err != nil {
		return 0, fmt.Errorf("這個 Bot 的電腦所在的主機連不上：%w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, limits.SnapshotTimeout)
	defer cancel()
	return s.o.CloseTabs(ctx, h.Docker(), st.ContainerID)
}

func dockerCloseTabs(ctx context.Context, api hosts.DockerAPI, containerID string) (int, error) {
	user := strconv.Itoa(box.BotUID) + ":" + strconv.Itoa(box.BotGID)
	created, err := api.ExecCreate(ctx, containerID, client.ExecCreateOptions{User: user, AttachStdout: true, AttachStderr: true,
		Cmd: []string{"python3", "-c", closeTabsScript}})
	if err != nil {
		return 0, fmt.Errorf("關分頁失敗：%w", err)
	}
	att, err := api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return 0, fmt.Errorf("關分頁失敗：%w", err)
	}
	defer att.Close()
	var out, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &stderr, att.Reader); err != nil {
		return 0, fmt.Errorf("關分頁失敗：%w", err)
	}
	res, err := api.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return 0, err
	}
	if res.ExitCode != 0 {
		return 0, fmt.Errorf("瀏覽器沒有回應（%d）：%s", res.ExitCode, lastLine(stderr.String()))
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out.String()))
	return n, nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
