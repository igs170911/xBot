package screen

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/hosts"
	"xbot/internal/limits"
)

type snapshot struct {
	jpeg []byte
	at   time.Time
}

// ErrNotRunning 表示電腦沒有開機（休眠中或還沒建立），沒有畫面可以擷取。
var ErrNotRunning = errors.New("電腦沒有開機")

// Snapshot 回傳 Bot 電腦目前畫面的一張小 JPEG（最大 limits.SnapshotWidth×SnapshotHeight）。
// 不會叫醒電腦；limits.SnapshotFresh 內重複要求回同一張。
func (s *Service) Snapshot(ctx context.Context, botID string) ([]byte, time.Time, error) {
	s.mu.Lock()
	if c, ok := s.snaps[botID]; ok && s.o.Now().Sub(c.at) < limits.SnapshotFresh {
		s.mu.Unlock()
		return c.jpeg, c.at, nil
	}
	s.mu.Unlock()
	st, err := s.o.Boxes.Status(ctx, botID)
	if err != nil {
		return nil, time.Time{}, err
	}
	if !st.Running || st.ContainerID == "" {
		return nil, time.Time{}, ErrNotRunning
	}
	h, err := s.o.Hosts.Get(st.HostID)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("這個 Bot 的電腦所在的主機連不上：%w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, limits.SnapshotTimeout)
	defer cancel()
	img, err := s.o.Snapshot(ctx, h.Docker(), st.ContainerID)
	if err != nil {
		return nil, time.Time{}, err
	}
	now := s.o.Now()
	s.mu.Lock()
	s.snaps[botID] = snapshot{jpeg: img, at: now}
	s.mu.Unlock()
	return img, now, nil
}

// snapshotCmd 是在 Bot 電腦裡擷取畫面的指令：ImageMagick 的 import（desktop_mcp 截圖用的同一個工具），
// 等比例縮小後輸出 JPEG 到 stdout。
func snapshotCmd() []string {
	size := strconv.Itoa(limits.SnapshotWidth) + "x" + strconv.Itoa(limits.SnapshotHeight) + ">"
	return []string{"import", "-silent", "-window", "root", "-resize", size, "-quality", strconv.Itoa(limits.SnapshotQuality), "jpeg:-"}
}

// dockerSnapshot 用 docker exec 在電腦裡以 Bot 使用者執行 snapshotCmd（DISPLAY 來自映像的環境變數）。
func dockerSnapshot(ctx context.Context, api hosts.DockerAPI, containerID string) ([]byte, error) {
	user := strconv.Itoa(box.BotUID) + ":" + strconv.Itoa(box.BotGID)
	created, err := api.ExecCreate(ctx, containerID, client.ExecCreateOptions{User: user, AttachStdout: true, AttachStderr: true, Cmd: snapshotCmd()})
	if err != nil {
		return nil, fmt.Errorf("擷取畫面失敗：%w", err)
	}
	att, err := api.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("擷取畫面失敗：%w", err)
	}
	defer att.Close()
	var out, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&limitWriter{w: &out, n: limits.SnapshotMaxBytes}, &stderr, att.Reader); err != nil {
		return nil, fmt.Errorf("擷取畫面失敗：%w", err)
	}
	res, err := api.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("擷取畫面失敗（%d）：%s", res.ExitCode, strings.TrimSpace(stderr.String()))
	}
	b := out.Bytes()
	if len(b) < 3 || b[0] != 0xFF || b[1] != 0xD8 {
		return nil, errors.New("擷取畫面失敗：不是 JPEG")
	}
	return b, nil
}

// limitWriter 超過 n 位元組就回錯誤。
type limitWriter struct {
	w io.Writer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if len(p) > l.n {
		return 0, errors.New("畫面太大")
	}
	l.n -= len(p)
	return l.w.Write(p)
}
