// Package sshtesthost 開一台測試用的「遠端主機」容器：自己的 Docker daemon（dind，privileged）＋ sshd，
// 只有 xbot 帳號（在 docker 群組）與測試產生的公鑰。給 go test -tags docker 驗證 SSH 主機（B11）用。
//
// 主機映像依 image/ 的內容取雜湊當標籤，內容不變就不重建；內層 Docker 的資料放在 volume
// xbot-test-sshhost-docker，Bot 電腦映像載入一次之後沿用（像建置快取，不會被測試刪掉）。
// 測試用的容器、網路，以及內層的 Bot 電腦與 volume 在測試結束時都會清掉。
package sshtesthost

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

//go:embed image
var files embed.FS

// CacheVolume 是內層 Docker 的資料（含載入的 Bot 電腦映像）。
const CacheVolume = "xbot-test-sshhost-docker"

// Label 標在測試建立的容器與網路上。
const Label = "xbot.test=sshhost"

// Host 是一台跑起來的測試主機。
type Host struct {
	// Container、Network 是外層的容器與網路名稱。
	Container, Network string
	// Addr 是 sshd 的位址（容器在測試網路上的 IP:22）。
	Addr string
	// Signer 是 xbot 帳號的私鑰。
	Signer ssh.Signer
}

// Tag 回傳依映像內容決定的標籤。
func Tag() (string, error) {
	h := sha256.New()
	err := fs.WalkDir(files, "image", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := files.ReadFile(p)
		fmt.Fprintf(h, "%s\x00%d\x00", p, len(b))
		h.Write(b)
		return err
	})
	return "xbot-test-sshhost:" + hex.EncodeToString(h.Sum(nil))[:12], err
}

func docker(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker %s：%w：%s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return strings.TrimSpace(string(out)), nil
}

// build 在映像不存在時建出它。
func build(ctx context.Context, tag string) error {
	if _, err := docker(ctx, "image", "inspect", tag); err == nil {
		return nil
	}
	dir, err := os.MkdirTemp("", "xbot-sshhost-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	err = fs.WalkDir(files, "image", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(p, ".sh") {
			mode = 0o755
		}
		return os.WriteFile(filepath.Join(dir, filepath.Base(p)), b, mode)
	})
	if err != nil {
		return err
	}
	_, err = docker(ctx, "build", "-q", "--label", Label, "-t", tag, dir)
	return err
}

// Start 開一台測試主機，把 boxImage 載入它的 Docker，並在測試結束時清掉。
func Start(ctx context.Context, t testing.TB, boxImage string) *Host {
	t.Helper()
	tag, err := Tag()
	if err != nil {
		t.Fatal(err)
	}
	if err := build(ctx, tag); err != nil {
		t.Fatalf("建測試主機映像：%v", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	// 上次中斷的測試留下的主機會佔著快取 volume，先清掉。
	if ids, _ := docker(ctx, "ps", "-aq", "--filter", "label="+Label); ids != "" {
		_, _ = docker(ctx, append([]string{"rm", "-f"}, strings.Fields(ids)...)...)
	}
	if ids, _ := docker(ctx, "network", "ls", "-q", "--filter", "label="+Label); ids != "" {
		_, _ = docker(ctx, append([]string{"network", "rm"}, strings.Fields(ids)...)...)
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	h := &Host{Container: "xbot-sshhost-" + hex.EncodeToString(suffix), Network: "xbot-sshhost-net-" + hex.EncodeToString(suffix), Signer: signer}
	if _, err := docker(ctx, "network", "create", "--label", Label, h.Network); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.cleanup() })
	if _, err := docker(ctx, "run", "-d", "--privileged", "--name", h.Container, "--network", h.Network, "--label", Label,
		"-e", "DOCKER_TLS_CERTDIR=", "-e", "XBOT_AUTHORIZED_KEY="+strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))),
		"-v", CacheVolume+":/var/lib/docker", tag); err != nil {
		t.Fatal(err)
	}
	ip, err := docker(ctx, "inspect", "-f", `{{(index .NetworkSettings.Networks "`+h.Network+`").IPAddress}}`, h.Container)
	if err != nil || ip == "" {
		t.Fatalf("取得測試主機 IP：%v", err)
	}
	h.Addr = net.JoinHostPort(ip, "22")

	// 等 dockerd 與 sshd。
	deadline := time.Now().Add(90 * time.Second)
	for {
		_, derr := h.Exec(ctx, "docker", "info")
		c, serr := net.DialTimeout("tcp", h.Addr, time.Second)
		if serr == nil {
			c.Close()
		}
		if derr == nil && serr == nil {
			break
		}
		if time.Now().After(deadline) {
			logs, _ := docker(ctx, "logs", "--tail", "30", h.Container)
			t.Fatalf("測試主機沒起來：%v %v\n%s", derr, serr, logs)
		}
		time.Sleep(500 * time.Millisecond)
	}
	h.removeInner(ctx)
	if err := h.load(ctx, boxImage); err != nil {
		t.Fatalf("把 %s 載入測試主機：%v", boxImage, err)
	}
	return h
}

// Exec 在測試主機（外層容器，root）執行指令。
func (h *Host) Exec(ctx context.Context, args ...string) (string, error) {
	return docker(ctx, append([]string{"exec", h.Container}, args...)...)
}

// Shell 在測試主機用 sh -c 執行一段指令。
func (h *Host) Shell(ctx context.Context, script string) (string, error) {
	return h.Exec(ctx, "sh", "-c", script)
}

// load 把本機的 Bot 電腦映像載入測試主機（已經有同一個映像就跳過）。B12 會改成主機自己拉映像。
func (h *Host) load(ctx context.Context, image string) error {
	want, err := docker(ctx, "image", "inspect", "-f", "{{.Id}}", image)
	if err != nil {
		return err
	}
	if got, err := h.Exec(ctx, "docker", "image", "inspect", "-f", "{{.Id}}", image); err == nil && got == want {
		return nil
	}
	save := exec.CommandContext(ctx, "docker", "save", image)
	load := exec.CommandContext(ctx, "docker", "exec", "-i", h.Container, "docker", "load", "-q")
	pipe, err := save.StdoutPipe()
	if err != nil {
		return err
	}
	load.Stdin = pipe
	var out bytes.Buffer
	load.Stdout, load.Stderr = &out, &out
	if err := save.Start(); err != nil {
		return err
	}
	if err := load.Run(); err != nil {
		_ = save.Wait()
		return fmt.Errorf("%w：%s", err, out.String())
	}
	return save.Wait()
}

// removeInner 清掉內層 Docker 裡 XBot 建立的容器與 volume（快取 volume 裡只留映像）。
func (h *Host) removeInner(ctx context.Context) {
	_, _ = h.Shell(ctx, `ids=$(docker ps -aq --filter label=xbot.managed); [ -z "$ids" ] || docker rm -f $ids >/dev/null;`+
		`vs=$(docker volume ls -q --filter label=xbot.managed); [ -z "$vs" ] || docker volume rm -f $vs >/dev/null; true`)
}

func (h *Host) cleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h.removeInner(ctx)
	_, _ = docker(ctx, "rm", "-f", "-v", h.Container)
	_, _ = docker(ctx, "network", "rm", h.Network)
}
