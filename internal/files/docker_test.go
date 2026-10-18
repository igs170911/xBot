//go:build docker

package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"xbot/internal/box"
	"xbot/internal/boxfs"
	"xbot/internal/hosts"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// TestDockerRoundTrip：真的 Bot 電腦映像（make box），/workspace 是 volume（和 Box Manager 一樣）。
// 上傳 → 列出 → 下載 → 搬移 → 刪除，連結擋掉，關機中也能讀寫但列資料夾要開機。
func TestDockerRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
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
	name := fmt.Sprintf("xbot-files-test-%d", time.Now().UnixNano())
	vol := name + "-ws"
	res, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{Name: name,
		Config:     &container.Config{Image: image, Entrypoint: []string{"sleep"}, Cmd: []string{"infinity"}},
		HostConfig: &container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: vol, Target: box.WorkspaceDir}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cli.ContainerRemove(context.Background(), res.ID, client.ContainerRemoveOptions{Force: true})
		cli.VolumeRemove(context.Background(), vol, client.VolumeRemoveOptions{Force: true})
	}()
	if _, err := cli.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatal(err)
	}
	user := fmt.Sprintf("%d:%d", box.BotUID, box.BotGID)
	if err := boxfs.Run(ctx, cli, res.ID, "0", "sh", "-c", "chown "+user+" /workspace && ln -s /etc /workspace/etc-link && "+
		"mkdir -p /root/secret && echo SECRET > /root/secret/key && ln -s /root/secret/key /workspace/key-link"); err != nil {
		t.Fatal(err)
	}
	fs := &boxfs.Docker{API: cli, ID: res.ID, UID: box.BotUID, GID: box.BotGID, Running: true}
	ws := &Docker{FS: fs, User: user}

	// 寫入（缺的資料夾一起建）→ 看 → 讀。
	content := []byte("商品,價格\n帆布包,1280\n")
	if err := ws.Write(ctx, "報告/價格.csv", int64(len(content)), bytes.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	info, err := ws.Stat(ctx, "報告/價格.csv")
	if err != nil || info.Size != int64(len(content)) || info.Dir {
		t.Fatalf("%+v %v", info, err)
	}
	rc, _, err := ws.Open(ctx, "報告/價格.csv")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, content) {
		t.Fatalf("%q", got)
	}
	// 擁有者是 Bot 使用者。
	owner := ""
	if out, err := fs.Output(ctx, "0", 1<<10, "stat", "-c", "%u:%g", "/workspace/報告/價格.csv", "/workspace/報告"); err != nil {
		t.Fatal(err)
	} else {
		owner = strings.Join(strings.Fields(string(out)), " ")
	}
	if owner != user+" "+user {
		t.Fatalf("擁有者：%q", owner)
	}
	// 換掉同一個檔案。
	if err := ws.Write(ctx, "報告/價格.csv", 3, strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}
	if info, _ := ws.Stat(ctx, "報告/價格.csv"); info.Size != 3 {
		t.Fatalf("換掉：%+v", info)
	}
	if err := ws.Mkdir(ctx, "a/b"); err != nil {
		t.Fatal(err)
	}
	ws.Write(ctx, ".hidden", 1, strings.NewReader("x"))
	ws.Write(ctx, "node_modules/p/i.js", 1, strings.NewReader("x"))

	// 列資料夾：連結不列；資料夾有標記。
	list, err := ws.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, i := range list {
		names[i.Rel] = i.Dir
	}
	if !names["報告"] || !names["a"] || names["etc-link"] || len(list) != 4 { // 報告、a、.hidden、node_modules
		t.Fatalf("%+v", list)
	}
	all, err := ws.Walk(ctx)
	if err != nil || len(all) != 1 || all[0].Rel != "報告/價格.csv" || all[0].Modified.IsZero() {
		t.Fatalf("Walk：%+v %v", all, err)
	}

	// 連結：讀、寫、列都擋掉。
	for _, p := range []string{"etc-link/passwd", "key-link", "etc-link"} {
		if _, _, err := ws.Open(ctx, p); !errors.Is(err, ErrLink) {
			t.Errorf("讀 %s：%v", p, err)
		}
	}
	if err := ws.Write(ctx, "etc-link/evil", 1, strings.NewReader("x")); !errors.Is(err, ErrLink) {
		t.Errorf("寫進連結：%v", err)
	}
	if _, err := ws.List(ctx, "etc-link"); !errors.Is(err, ErrLink) {
		t.Errorf("列連結資料夾：%v", err)
	}
	// 搬移、不蓋掉、刪除。
	if err := ws.Move(ctx, "報告/價格.csv", "a/b/改名.csv"); err != nil {
		t.Fatal(err)
	}
	if err := ws.Move(ctx, "a", "報告"); !errors.Is(err, ErrExists) {
		t.Fatalf("不蓋掉：%v", err)
	}
	if err := ws.Move(ctx, "a/b/改名.csv", "etc-link/x"); !errors.Is(err, ErrLink) {
		t.Fatalf("搬進連結：%v", err)
	}
	if err := ws.Move(ctx, "沒有", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := ws.Remove(ctx, "key-link"); err != nil {
		t.Fatal(err)
	}
	if out, _ := fs.Output(ctx, "0", 100, "cat", "/root/secret/key"); string(out) != "SECRET\n" {
		t.Fatal("刪連結不會刪到目標")
	}
	if err := ws.Remove(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Stat(ctx, "a/b/改名.csv"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}

	// 經過操作：上傳（multipart 走的那條）→ 列出 → 讀。
	dir := t.TempDir()
	db, err := store.Open(ctx, dir+"/"+store.FileName)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bot, _ := db.CreateBot(ctx, store.Bot{Name: "小秘", Engine: "hermes"})
	reg := ops.New(ops.Config{DB: db})
	running := true
	Register(reg, &Service{Locate: func(_ context.Context, _ string, wake bool) (Workspace, error) {
		d := &boxfs.Docker{API: cli, ID: res.ID, UID: box.BotUID, GID: box.BotGID, Running: running}
		if wake && !running {
			return nil, errors.New("測試不開機")
		}
		return &Docker{FS: d, User: user}, nil
	}})
	call := func(op string, in, out any) error {
		raw, _ := json.Marshal(in)
		r, err := reg.Invoke(ctx, ops.Owner("test"), op, raw, ops.Options{})
		if err == nil && out != nil {
			json.Unmarshal(r.Output, out)
		}
		return err
	}
	var up Entry
	raw := bytes.Repeat([]byte("x"), 3<<20) // 3 MB，串流寫入
	r, err := reg.Invoke(WithStream(ctx, Stream{R: bytes.NewReader(raw), Size: int64(len(raw))}), ops.Owner("test"), "xbot.files.upload",
		json.RawMessage(fmt.Sprintf(`{"bot_id":%q,"name":"大檔.bin","size":%d}`, bot.ID, len(raw))), ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(r.Output, &up)
	if up.Path != "/workspace/uploads/大檔.bin" || up.Size != int64(len(raw)) {
		t.Fatalf("%+v", up)
	}
	var lo ListOut
	if err := call("xbot.files.list", ListIn{BotID: bot.ID, Path: "uploads"}, &lo); err != nil || len(lo.Entries) != 1 || lo.Entries[0].Size != int64(len(raw)) {
		t.Fatalf("%+v %v", lo, err)
	}
	rc, _, err = ws.Open(ctx, "uploads/大檔.bin")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, raw) {
		t.Fatal("下載的內容要和上傳的一樣")
	}

	// 關機中：讀寫照常，列資料夾回 off。
	if _, err := cli.ContainerStop(ctx, res.ID, client.ContainerStopOptions{}); err != nil {
		t.Fatal(err)
	}
	running = false
	if err := call("xbot.files.write", WriteIn{BotID: bot.ID, Path: "關機/筆記.md", Content: "hi"}, nil); err != nil {
		t.Fatalf("關機中寫入：%v", err)
	}
	var ro ReadOut
	if err := call("xbot.files.read", ReadIn{BotID: bot.ID, Path: "關機/筆記.md"}, &ro); err != nil || ro.Text != "hi" {
		t.Fatalf("關機中讀：%+v %v", ro, err)
	}
	if err := call("xbot.files.list", ListIn{BotID: bot.ID}, &lo); err != nil || lo.State != "off" {
		t.Fatalf("關機中列資料夾：%+v %v", lo, err)
	}
}
