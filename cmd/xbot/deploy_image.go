package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/moby/moby/api/types/image"

	"xbot/internal/hosts"
	"xbot/internal/limits"
)

// make deploy 也把 Bot 電腦映像帶過去（V10）：那台沒有映像或和這台的不同（各層內容與設定的指紋，同 B12）時，
// 用你自己的 SSH 把 docker save 串流到那台的 docker load，不落地、不需要映像倉庫。

// imageRemote 是部署目的地（測試用假的）。
type imageRemote interface {
	output(remote string) (string, error)
	// pipe 執行遠端指令，r 接到它的 stdin。
	pipe(remote string, r io.Reader) error
}

func (t sshTarget) pipe(remote string, r io.Reader) error {
	cmd := t.command(false, remote)
	cmd.Stdin = r
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// imageSyncResult 是 syncImage 的結果。
type imageSyncResult struct {
	// State：sent（傳過去了）、same（已經是同一版）、skipped（沒傳，Note 是原因與怎麼補）。
	State string
	Note  string
	Bytes int64
}

// syncImage 讓 remote 有和這台一樣的電腦映像 name（ref 是這台的映像資訊，用 hosts.LocalImage 讀）。
func syncImage(ctx context.Context, saver hosts.ImageSaver, ref hosts.ImageRef, remoteArch string, remote imageRemote, out io.Writer) imageSyncResult {
	name := ref.Name
	if ref.ID == "" {
		return imageSyncResult{State: "skipped", Note: fmt.Sprintf("這台沒有 Bot 電腦映像 %s（先 make box 再部署一次）", name)}
	}
	if ref.Arch != "" && remoteArch != "" && ref.Arch != remoteArch {
		return imageSyncResult{State: "skipped", Note: fmt.Sprintf("這台的映像是 %s，那台是 %s，不能用；請在那台拿一份原始碼執行 make box", ref.Arch, remoteArch)}
	}
	raw, err := remote.output(remoteInspect(name))
	if err != nil {
		return imageSyncResult{State: "skipped", Note: "連不上那台：" + err.Error()}
	}
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "XBOT-NO-DOCKER"):
		return imageSyncResult{State: "skipped", Note: "那台沒有 Docker（在那台執行 make setup，或用 make host-add 把它加成 Bot 電腦主機）"}
	case strings.HasPrefix(raw, "XBOT-NO-ACCESS"):
		msg := strings.TrimSpace(strings.TrimPrefix(raw, "XBOT-NO-ACCESS"))
		if strings.Contains(msg, "permission denied") {
			return imageSyncResult{State: "skipped", Note: "那台的帳號不能用 Docker（加進 docker 群組：sudo usermod -aG docker <帳號>，重新登入後再部署一次）"}
		}
		return imageSyncResult{State: "skipped", Note: "那台的 Docker 沒有在執行（" + msg + "）"}
	}
	var got []image.InspectResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &got); err == nil && len(got) > 0 && hosts.SameImage(got[0], ref) {
		return imageSyncResult{State: "same"}
	}
	fmt.Fprintf(out, "▶ 傳 Bot 電腦映像 %s（約 %s，只有第一次或映像改過時要傳）…\n", name, humanSize(ref.Size))
	rd, err := saver.ImageSave(ctx, []string{name})
	if err != nil {
		return imageSyncResult{State: "skipped", Note: "匯出映像失敗：" + err.Error()}
	}
	defer rd.Close()
	cr := &countingReader{r: rd}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(limits.HostImageProgressEvery * 10)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				fmt.Fprintf(out, "   %s／%s\n", humanSize(cr.n.Load()), humanSize(ref.Size))
			}
		}
	}()
	err = remote.pipe("docker load -q", cr)
	close(stop)
	<-done
	if err != nil {
		return imageSyncResult{State: "skipped", Note: "傳映像失敗：" + err.Error()}
	}
	return imageSyncResult{State: "sent", Bytes: cr.n.Load()}
}

type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", n>>10)
}

// remoteInspect 是在目的地讀映像資訊的指令：沒有 Docker、不能用 Docker 時印出標記（一律成功結束）。
func remoteInspect(name string) string {
	return `command -v docker >/dev/null 2>&1 || { echo XBOT-NO-DOCKER; exit 0; }; ` +
		`docker info >/dev/null 2>&1 || { echo XBOT-NO-ACCESS $(docker info 2>&1 | tail -n 1); exit 0; }; ` +
		`docker image inspect ` + shellWord(name) + ` 2>/dev/null || echo XBOT-NO-IMAGE`
}

// deployImage 是 cmdDeploy 的映像步驟：讀這台的映像，必要時傳過去。失敗不擋部署（XBot 本身照樣能用），只提醒怎麼補。
func deployImage(ctx context.Context, t sshTarget, arch string) error {
	name := boxImage()
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		return err
	}
	defer cli.Close()
	ref, err := hosts.LocalImage(ctx, cli, name)
	if err != nil {
		fmt.Printf("⚠️  讀不到這台的 Bot 電腦映像（Docker 沒有在執行？）：%v\n   略過；之後 make deploy 會再試\n", err)
		return nil
	}
	res := syncImage(ctx, cli, ref, arch, t, os.Stdout)
	switch res.State {
	case "same":
		fmt.Printf("✅ 那台的 Bot 電腦映像 %s 已是最新\n", name)
	case "sent":
		fmt.Printf("✅ 已把 Bot 電腦映像 %s 傳過去（%s）\n", name, humanSize(res.Bytes))
	default:
		fmt.Printf("⚠️  沒有傳 Bot 電腦映像：%s\n   XBot 照樣部署；那台要開 Bot 的電腦前記得補上\n", res.Note)
	}
	return nil
}
