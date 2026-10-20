//go:build docker

// 需要 Docker 和 make box 建好的映像：go test -tags docker ./internal/cleanup/...（或 make test-docker）。
package cleanup

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/hosts"
	"xbot/internal/store"
)

// 真的 Bot 電腦映像：沒開機時經輔助容器清（以 Bot 使用者、不叫醒電腦），開機後在電腦裡清，
// 開著的瀏覽器收得到「清快取」（/opt/xbot/playwright 與 DevTools 9222）。
func TestDockerCleanup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	image := os.Getenv("BOX_IMAGE")
	if image == "" {
		image = box.DefaultImage
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
		t.Skip("沒有 docker 指令（測試用它在 volume 裡放檔案）")
	}
	dir := t.TempDir()
	db, err := store.Open(ctx, dir+"/"+store.FileName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	host, err := hosts.EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })
	bot, err := db.CreateBot(ctx, store.Bot{Name: "清理測試"})
	if err != nil {
		t.Fatal(err)
	}
	m := box.New(db, hosts.NewRegistry(host), box.Options{Image: image, NoNetGuard: true})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := m.Remove(ctx, bot.ID, true); err != nil {
			t.Errorf("刪除測試電腦失敗：%v", err)
		}
	})
	if _, err := m.Create(ctx, bot.ID); err != nil {
		t.Fatal(err)
	}

	// 以 root 在 volume 裡放好檔案，再交給 Bot 使用者（和真的電腦一樣）。
	vols := box.VolumeNames(bot.ID)
	old, recent := time.Now().Add(-40*day).Unix(), time.Now().Add(-day).Unix()
	setup := fmt.Sprintf(`set -e
mkdir -p /hermes/cache/scratch/lane /home/box/chrome-profile/Default/Cache /workspace/uploads/舊資料夾 /workspace/tmp
head -c 300 /dev/zero > /hermes/cache/scratch/lane/a.bin
head -c 70 /dev/zero > /hermes/cache/scratch/new.bin
head -c 500 /dev/zero > /workspace/uploads/舊資料夾/IMG.jpeg
head -c 40 /dev/zero > /workspace/uploads/10月訂單.csv
head -c 30 /dev/zero > /workspace/uploads/新的.txt
head -c 80 /dev/zero > /workspace/tmp/page.html
head -c 50 /dev/zero > /workspace/報告.md
head -c 20 /dev/zero > /home/box/chrome-profile/Default/Cookies
truncate -s 210M /home/box/chrome-profile/Default/Cache/data_1
touch -d @%[1]d /hermes/cache/scratch/lane/a.bin /workspace/uploads/舊資料夾/IMG.jpeg /workspace/uploads/10月訂單.csv /workspace/tmp/page.html /workspace/報告.md
touch -d @%[2]d /hermes/cache/scratch/new.bin /workspace/uploads/新的.txt
chown -R 1000:1000 /hermes /home/box /workspace
`, old, recent)
	inVolumes := func(script string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "-u", "0", "--network", "none",
			"-v", vols.Hermes+":/hermes", "-v", vols.Home+":/home/box", "-v", vols.Workspace+":/workspace",
			"--entrypoint", "sh", image, "-c", script).CombinedOutput()
		if err != nil {
			t.Fatalf("%v：%s", err, out)
		}
		return string(out)
	}
	inVolumes(setup)

	// 沒開機：輔助容器。
	err = m.WithShell(ctx, bot.ID, true, func(ctx context.Context, sh box.Shell) error {
		if sh.Live || !strings.HasPrefix(sh.Hermes, "/xbot-move/") {
			t.Errorf("沒開機時應該在輔助容器裡：%+v", sh)
		}
		u, err := Measure(ctx, sh)
		if err != nil {
			return err
		}
		if u.Workspace != 50 || u.Uploads != 570 || u.Caches != 300+70+80+210<<20 || u.Browser != 20 {
			t.Errorf("用量：%+v", u)
		}
		res, err := Clean(ctx, sh, time.Now(), Options{Uploads: true})
		if err != nil {
			return err
		}
		if res != (Result{Scratch: 380, Uploads: 540, Browser: 210 << 20, Freed: 380 + 540 + 210<<20}) {
			t.Errorf("清理結果：%+v", res)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.ContainerInspect(ctx, "xbot-clean-"+bot.ID, client.ContainerInspectOptions{}); !cerrdefs.IsNotFound(err) {
		t.Fatalf("輔助容器要刪掉：%v", err)
	}
	if st, err := m.Status(ctx, bot.ID); err != nil || st.Running {
		t.Fatalf("不該叫醒電腦：%+v %v", st, err)
	}
	left := inVolumes(`cd / && find hermes home/box workspace -type f | LC_ALL=C sort`)
	want := "hermes/cache/scratch/new.bin\nhome/box/chrome-profile/Default/Cookies\nworkspace/uploads/新的.txt\nworkspace/報告.md\n"
	for _, f := range strings.Split(strings.TrimSpace(want), "\n") {
		if !strings.Contains(left, f+"\n") {
			t.Errorf("%s 不該被刪；剩下：\n%s", f, left)
		}
	}
	for _, f := range []string{"lane", "IMG.jpeg", "10月訂單.csv", "page.html", "data_1"} {
		if strings.Contains(left, f) {
			t.Errorf("%s 應該被刪；剩下：\n%s", f, left)
		}
	}

	// 開機：在電腦裡以 Bot 使用者執行；開著的瀏覽器收得到「清快取」。
	if _, err := m.Start(ctx, bot.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.WaitReady(ctx, bot.ID); err != nil {
		t.Fatal(err)
	}
	err = m.WithShell(ctx, bot.ID, true, func(ctx context.Context, sh box.Shell) error {
		if !sh.Live || sh.Workspace != box.WorkspaceDir {
			t.Errorf("開機時應該在電腦裡：%+v", sh)
		}
		if _, err := Measure(ctx, sh); err != nil {
			return err
		}
		var err error
		for i := range 30 { // 桌面就緒後瀏覽器還要幾秒才開好
			if _, err = sh.Run(ctx, 4096, "node", "--input-type=module", "-e", clearLiveCache); err == nil {
				t.Logf("第 %d 次就清好了", i+1)
				return nil
			}
			time.Sleep(time.Second)
		}
		return fmt.Errorf("開著的瀏覽器沒有清快取：%w", err)
	})
	if err != nil {
		t.Fatal(err)
	}
}
