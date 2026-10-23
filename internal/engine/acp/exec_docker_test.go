//go:build docker

package acp_test

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/engine/acp"
	"xbot/internal/hosts"
	"xbot/internal/limits"
)

// 關掉不理 EOF 的 agent（例：Gemini CLI 讀到 EOF 不會結束）：過了 ACPExitGrace，在電腦裡照記號強制結束它和它開的子行程。
// Docker 回的 exec Pid 是主機上的編號，容器裡用不了；用 Bot 使用者、CapDrop ALL 也要做得到。
// 需要 Docker 與 make box 建好的映像（BOX_IMAGE，預設 xbot-box:dev）；沒有時略過。
func TestDockerExecReapsAnAgentThatIgnoresEOF(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	if _, err := cli.ImageInspect(ctx, image); err != nil {
		t.Skipf("沒有映像 %s（先執行 make box）：%v", image, err)
	}
	user := "1000:1000"
	created, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:     &container.Config{Image: image, User: user, Entrypoint: []string{"sleep", "300"}, NetworkDisabled: true},
		HostConfig: &container.HostConfig{NetworkMode: "none", CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := created.ID
	defer func() {
		_, _ = cli.ContainerRemove(context.Background(), id, client.ContainerRemoveOptions{Force: true})
	}()
	if _, err := cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		out, _ := exec.CommandContext(ctx, "docker", "exec", id, "sh", "-c", "ps -eo args | grep -cx 'sleep 600'").Output()
		n, err := strconv.Atoi(strings.TrimSpace(string(out)))
		if err != nil {
			return -1
		}
		return n
	}
	// 不讀 stdin 的「agent」，還開了兩個子行程。
	p, err := acp.Exec(ctx, cli, acp.ExecSpec{ContainerID: id, User: user, Home: "/tmp"},
		acp.Agent{Label: "test", Command: []string{"sh", "-c", "cd /tmp; sleep 600 & sleep 600 & wait"}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for count() != 2 {
		if time.Now().After(deadline) {
			t.Fatalf("子行程沒有起來：%d", count())
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = p.Close()
	time.Sleep(limits.ACPExitGrace / 2)
	if count() != 2 {
		t.Fatal("寬限時間內不強制結束")
	}
	deadline = time.Now().Add(limits.ACPExitGrace + 20*time.Second)
	for count() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("過了寬限時間還有 %d 個行程", count())
		}
		time.Sleep(300 * time.Millisecond)
	}
}
