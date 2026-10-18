//go:build docker

// 需要 Docker 和 make box 建好的映像：go test -tags docker ./internal/box/...（或 make test-docker）。
// 映像用 BOX_IMAGE 指定，預設 xbot-box:dev；沒有 Docker 或映像時略過。
package box

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/hosts"
	"xbot/internal/store"
)

func TestDockerLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	image := os.Getenv("BOX_IMAGE")
	if image == "" {
		image = DefaultImage
	}
	cli, err := hosts.NewLocalDocker()
	if err != nil {
		t.Skipf("沒有 Docker：%v", err)
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		t.Skipf("連不上 Docker：%v", err)
	}
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("沒有映像 %s（先執行 make box）：%v", image, err)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("沒有 docker 指令（測試用它在容器裡寫檔）")
	}

	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	host, err := hosts.EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })
	if row, _ := db.GetHost(ctx, host.ID()); row.Status != "online" {
		t.Fatalf("本機主機應該在線：%+v", row)
	}
	bot, err := db.CreateBot(ctx, store.Bot{Name: "整合測試"})
	if err != nil {
		t.Fatal(err)
	}
	m := New(db, hosts.NewRegistry(host), Options{Image: image})
	t.Cleanup(func() {
		// 不論成敗都把容器和 volume 清掉。
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := m.Remove(ctx, bot.ID, true); err != nil {
			t.Errorf("清理失敗：%v", err)
		}
	})
	logs := func() string {
		out, _ := exec.Command("docker", "logs", "--tail", "40", ContainerName(bot.ID)).CombinedOutput()
		return string(out)
	}

	// 建立 → 開機 → 等到 noVNC 與 Hermes 就緒。
	if s, err := m.Create(ctx, bot.ID); err != nil || s.State != StateSleeping {
		t.Fatalf("建立：%+v %v", s, err)
	}
	if _, err := m.Start(ctx, bot.ID); err != nil {
		t.Fatalf("開機：%v", err)
	}
	s, err := m.WaitReady(ctx, bot.ID)
	if err != nil {
		t.Fatalf("等待就緒：%v\n%s", err, logs())
	}
	t.Logf("就緒：noVNC %s、Hermes %s", s.NoVNCAddr, s.HermesAddr)
	row, err := db.GetBox(ctx, bot.ID)
	if err != nil || row.State != StateRunning || row.NoVNCPort != s.NoVNCPort || row.HermesPort != s.HermesPort {
		t.Fatalf("box 表：%+v %v", row, err)
	}

	// 實際的容器設定符合 SEC-3。
	got, err := cli.ContainerInspect(ctx, s.ContainerID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	hc := got.Container.HostConfig
	if len(hc.CapDrop) != 1 || hc.CapDrop[0] != "ALL" || hc.PidsLimit == nil || *hc.PidsLimit != DefaultLimits().Pids ||
		hc.Memory != 3<<30 || hc.ShmSize != 512<<20 {
		t.Fatalf("容器設定：%+v", hc)
	}

	// 在 workspace 寫檔（以 Bot 使用者身分），桌面使用者也確實降權了。
	writer := exec.CommandContext(ctx, "docker", "exec", "-u", "1000:1000", ContainerName(bot.ID), "sh", "-c", "echo 留著 > /workspace/keep.txt")
	if out, err := writer.CombinedOutput(); err != nil {
		t.Fatalf("寫檔：%v %s", err, out)
	}
	ps, _ := exec.CommandContext(ctx, "docker", "exec", ContainerName(bot.ID), "ps", "-o", "user=", "-C", "Xtigervnc").CombinedOutput()
	if u := strings.TrimSpace(string(ps)); u == "" || u == "root" {
		t.Fatalf("桌面應該以 Bot 使用者執行，實際：%q", u)
	}

	// 停止 → 再開機，資料保留。
	if s, err = m.Stop(ctx, bot.ID); err != nil || s.State != StateSleeping || s.Running {
		t.Fatalf("停止：%+v %v", s, err)
	}
	// 正常收尾（exit 0）：tini 要有 KILL 才能把 SIGTERM 轉給已降權的子行程，否則會 FATAL 結束（exit 1）。
	if got, err := cli.ContainerInspect(ctx, s.ContainerID, client.ContainerInspectOptions{}); err != nil || got.Container.State.ExitCode != 0 {
		t.Fatalf("停止應該正常收尾：%+v %v\n%s", got.Container.State, err, logs())
	}
	if _, err := m.Start(ctx, bot.ID); err != nil {
		t.Fatalf("再開機：%v", err)
	}
	if s, err = m.WaitReady(ctx, bot.ID); err != nil {
		t.Fatalf("再開機就緒：%v\n%s", err, logs())
	}
	readFile := func() string {
		out, err := exec.CommandContext(ctx, "docker", "exec", ContainerName(bot.ID), "cat", "/workspace/keep.txt").CombinedOutput()
		if err != nil {
			t.Fatalf("讀檔：%v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := readFile(); got != "留著" {
		t.Fatalf("重開後的檔案：%q", got)
	}

	// 刪除容器但保留 volume；重建後資料還在。
	if _, err := m.Remove(ctx, bot.ID, false); err != nil {
		t.Fatalf("刪除：%v", err)
	}
	for _, v := range VolumeNames(bot.ID).list() {
		if _, err := cli.VolumeInspect(ctx, v, client.VolumeInspectOptions{}); err != nil {
			t.Fatalf("volume %s 應該保留：%v", v, err)
		}
	}
	if _, err := m.Start(ctx, bot.ID); err != nil {
		t.Fatalf("重新建立：%v", err)
	}
	if got := readFile(); got != "留著" {
		t.Fatalf("重新建立後的檔案：%q", got)
	}

	// purge：容器與 volume 都刪掉。
	if _, err := m.Remove(ctx, bot.ID, true); err != nil {
		t.Fatalf("purge：%v", err)
	}
	for _, v := range VolumeNames(bot.ID).list() {
		if _, err := cli.VolumeInspect(ctx, v, client.VolumeInspectOptions{}); err == nil {
			t.Fatalf("volume %s 應該被刪除", v)
		}
	}
}
