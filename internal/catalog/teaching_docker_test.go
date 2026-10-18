//go:build docker

package catalog_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/api"
	"xbot/internal/auth"
	"xbot/internal/box"
	"xbot/internal/box/guardlock"
	"xbot/internal/boxfs"
	"xbot/internal/catalog"
	"xbot/internal/engine/hermes"
	"xbot/internal/engine/hermes/hermestest"
	"xbot/internal/events"
	"xbot/internal/hosts"
	"xbot/internal/ipc"
	"xbot/internal/ops"
	"xbot/internal/opsclient"
	"xbot/internal/proxy"
	"xbot/internal/runtime"
	"xbot/internal/screen"
	"xbot/internal/skills"
	"xbot/internal/store"
	"xbot/internal/teaching"
)

// teachPage 是測試用的網頁：一個搜尋框、一個按鈕。
const teachPage = `<!doctype html><html><head><meta charset="utf-8"><title>測試商店</title></head><body>
<form onsubmit="event.preventDefault(); document.getElementById('out').textContent = '找到 3 筆'">
<label for="q">關鍵字</label><input id="q" name="q">
<button id="go" type="submit">搜尋</button></form><p id="out"></p></body></html>`

// driveJS 經 CDP（Bot 電腦裡的 Chromium）打開測試頁、輸入關鍵字、按搜尋，就像你在畫面上操作（錄製器記的是頁面事件）。
const driveJS = `import { chromium } from '/opt/xbot/playwright/node_modules/playwright/index.mjs';
setTimeout(() => process.exit(3), 60000);
let browser;
for (let i = 0; i < 30 && !browser; i++) {
  try { browser = await chromium.connectOverCDP('http://127.0.0.1:9222', { timeout: 3000 }); }
  catch { await new Promise(r => setTimeout(r, 1000)); }
}
if (!browser) process.exit(4);
const context = browser.contexts()[0];
const page = context.pages()[0] || await context.newPage();
await page.bringToFront();
await page.goto('file:///workspace/teach-test.html');
await page.waitForTimeout(1500);
await page.click('#q');
await page.keyboard.type('耳機', { delay: 50 });
await page.waitForTimeout(300);
await page.click('#go');
await page.waitForTimeout(2500);
process.stdout.write(await page.textContent('#out'));
process.exit(0);
`

// TestDockerTeaching（B32）：真的 Bot 電腦（唯讀根目錄、Bot 使用者 1000）。開始示範 → 錄製器以 uid 1000 在 /workspace/.teaching 跑起來；
// 測試經 CDP 在測試頁上輸入、點擊 → 即時步驟出現；補充說明；完成 → 錄製器結束、畫面交還、草稿含步驟；存成技能（含截圖）→ 錄製檔清掉。
func TestDockerTeaching(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
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
	modelSrv := httptest.NewServer(&hermestest.FakeModel{})
	defer modelSrv.Close()

	dir, err := os.MkdirTemp("", "xbb32")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	local, err := hosts.EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()

	// 和 cmd/xbot/stack.go、registry.go 相同的組裝。
	kr, err := proxy.LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	pstore := proxy.NewDBStore(db)
	providers := proxy.NewProviders(pstore, kr)
	keys := proxy.NewWorkerKeys(pstore, kr)
	mp := proxy.NewHandler(proxy.Options{Keys: keys, Providers: providers, Usage: pstore})
	sockets := ipc.New(ipc.Options{Dir: func(id string) string { return filepath.Join(hosts.BotDir(dir, id), "ipc") }, Model: mp, Keys: keys})
	defer sockets.Shutdown()
	comp := &runtime.Computers{DB: db, Keys: keys, Sockets: sockets, Providers: pstore}
	registry := hosts.NewRegistry(local)
	comp.Hosts = registry
	guardlock.Shared(t)
	boxes := box.New(db, registry, box.Options{Image: image, Prepare: comp.Prepare})
	comp.Boxes = boxes
	drv := hermes.New(comp.Connect, nil)
	bus := events.New()
	rt := runtime.New(runtime.Options{DB: db, Driver: drv, Bus: bus, Models: comp})
	scr := screen.New(screen.Options{DB: db, Boxes: boxes, Hosts: registry, Runtime: rt, Bus: bus})
	sk := &skills.Service{Locate: comp.BotFS, Library: &skills.Library{Dir: filepath.Join(dir, "skills")}}
	tc := teaching.New(teaching.Options{DB: db, Screen: scr, Runtime: rt, Bus: bus,
		Open: teaching.DockerOpener(comp.BotFS, func(ctx context.Context, botID string) error {
			if _, err := boxes.Start(ctx, botID); err != nil {
				return err
			}
			_, err := boxes.WaitReady(ctx, botID)
			return err
		}),
		Create: func(ctx context.Context, botID string, e skills.Edit) (skills.Skill, error) {
			f, err := comp.BotFS(ctx, botID, true)
			if err != nil {
				return skills.Skill{}, err
			}
			return skills.Bot{FS: f, BotID: botID}.Create(ctx, e)
		}})

	reg := ops.New(ops.Config{DB: db, Notify: func(e store.Event) { bus.Publish(events.FromStore(e)) }})
	ops.RegisterBuiltin(reg)
	catalog.Register(reg, catalog.Deps{Runtime: rt, Providers: providers, Lister: pstore, Screen: scr, Skills: sk, Teaching: tc})
	const accessToken = "owner-access-token-b32"
	tokens := &auth.Authenticator{DB: db, AccessToken: accessToken}
	srv := httptest.NewServer(api.New(api.Options{ModelProxy: mp, Registry: reg, Bus: bus, DB: db, VNC: scr.ServeVNC,
		Auth: auth.NewHTTP(auth.HTTPConfig{Tokens: tokens, AccessToken: accessToken})}))
	defer srv.Close()
	c := &opsclient.Client{BaseURL: srv.URL, Token: accessToken}
	run := func(op string, in any, out any) {
		t.Helper()
		raw, _ := json.Marshal(in)
		res, err := c.Call(ctx, op, raw, ops.Options{})
		if err != nil {
			t.Fatalf("%s：%v", op, err)
		}
		if out != nil {
			if err := json.Unmarshal(res.Output, out); err != nil {
				t.Fatal(err)
			}
		}
	}

	run("xbot.models.set_provider", map[string]any{"id": "openai", "base_url": modelSrv.URL + "/v1", "api_key": "sk-fake-b32", "default_model": "fake-model"}, nil)
	var bot struct {
		ID string `json:"id"`
	}
	run("xbot.bots.create", map[string]any{"name": "抓蟲"}, &bot)
	name := box.ContainerName(bot.ID)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		tc.Forget(bot.ID)
		drv.Close(bot.ID)
		if _, err := boxes.Remove(ctx, bot.ID, true); err != nil {
			t.Errorf("清理失敗：%v", err)
		}
	}()
	dexec := func(args ...string) (string, error) {
		out, err := exec.Command("docker", append([]string{"exec", "-u", "1000:1000", name}, args...)...).CombinedOutput()
		return string(out), err
	}

	// 開始示範：電腦還沒開機，先開機再開始（E05「電腦開機中」）。
	var l teaching.Lesson
	run("xbot.teaching.start", map[string]any{"bot_id": bot.ID, "goal": "搜尋商品"}, &l)
	if l.State != store.LessonRecording || scr.TakeoverOf(bot.ID) == nil {
		t.Fatalf("開始：%+v", l)
	}
	teachDir := teaching.Dir + "/" + l.ID
	// 錄製器以 Bot 使用者（1000）執行；根目錄唯讀，錄製檔寫在 /workspace/.teaching。
	ps, err := dexec("sh", "-c", "ps -eo uid=,args= | grep '[b]ox-teach-'")
	if err != nil || strings.Count(ps, "box-teach-") != 2 {
		t.Fatalf("錄製器：%q %v", ps, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(ps), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "1000 ") {
			t.Fatalf("錄製器要以 uid 1000 執行：%q", line)
		}
	}
	if out, err := dexec("touch", "/usr/local/bin/x"); err == nil {
		t.Fatalf("根目錄要是唯讀的：%s", out)
	}

	// 測試經 CDP 在測試頁上操作（就像你在畫面上做）。
	f, err := comp.BotFS(ctx, bot.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFiles(ctx, box.WorkspaceDir, []boxfs.File{{Name: "teach-test.html", Data: []byte(teachPage)}}); err != nil {
		t.Fatal(err)
	}
	if out, err := dexec("node", "--input-type=module", "-e", driveJS); err != nil || !strings.Contains(out, "找到 3 筆") {
		t.Fatalf("操作測試頁：%q %v", out, err)
	}
	// 即時步驟：每秒讀一次錄製檔。
	var cur teaching.CurrentOut
	deadline := time.Now().Add(30 * time.Second)
	for {
		run("xbot.teaching.get", map[string]any{"bot_id": bot.ID}, &cur)
		if cur.Lesson != nil && len(cur.Lesson.Steps) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			log, _ := dexec("sh", "-c", "cat "+teachDir+"/browser.log; cat "+teachDir+"/browser.jsonl")
			t.Fatalf("沒有記到步驟：%+v\n%s", cur.Lesson, log)
		}
		time.Sleep(time.Second)
	}
	run("xbot.teaching.note", map[string]any{"bot_id": bot.ID, "lesson_id": l.ID, "text": "關鍵字每次不一樣"}, &l)

	var done teaching.Lesson
	run("xbot.teaching.stop", map[string]any{"bot_id": bot.ID, "lesson_id": l.ID}, &done)
	joined := strings.Join(func() []string {
		var out []string
		for _, s := range done.Steps {
			out = append(out, s.Text)
		}
		return out
	}(), "|")
	for _, want := range []string{"前往「測試商店」", "在文字框「關鍵字」輸入「耳機」", "點擊按鈕「搜尋」", "使用者補充：關鍵字每次不一樣"} {
		if !strings.Contains(joined, want) {
			t.Errorf("步驟少了 %q：%s", want, joined)
		}
	}
	if done.State != store.LessonStopped || done.Draft == nil || !strings.Contains(done.Draft.Instructions, "點擊按鈕「搜尋」") ||
		!strings.Contains(done.Draft.Instructions, "定位：`#go`") || len(done.Draft.Shots) == 0 {
		t.Fatalf("草稿：%+v", done.Draft)
	}
	if scr.TakeoverOf(bot.ID) != nil {
		t.Fatal("完成後要交還畫面")
	}
	if out, err := dexec("sh", "-c", "ps -eo args= | grep '[b]ox-teach-'"); err == nil {
		t.Fatalf("錄製器要結束：%s", out)
	}
	var shot teaching.ShotOut
	run("xbot.teaching.shot", map[string]any{"bot_id": bot.ID, "lesson_id": l.ID, "name": done.Draft.Shots[0]}, &shot)
	if len(shot.Image) < 1000 || shot.Image[0] != 0xFF || shot.Image[1] != 0xD8 {
		t.Fatalf("截圖：%d bytes", len(shot.Image))
	}

	var saved teaching.SaveResult
	run("xbot.teaching.save", map[string]any{"bot_id": bot.ID, "lesson_id": l.ID, "name": done.Draft.Name, "description": done.Draft.Description,
		"instructions": done.Draft.Instructions, "skip_trial": true}, &saved)
	if saved.Skill.Source != teaching.SourceTaught || saved.Lesson.State != store.LessonSaved || len(saved.Skill.Files) == 0 {
		t.Fatalf("存檔：%+v", saved)
	}
	md, err := dexec("cat", skills.SkillsDir+"/"+saved.Skill.ID+"/SKILL.md")
	if err != nil || !strings.Contains(md, "name: 搜尋商品") || !strings.Contains(md, "點擊按鈕「搜尋」") {
		t.Fatalf("SKILL.md：%s %v", md, err)
	}
	if out, err := dexec("test", "-e", teachDir); err == nil {
		t.Fatalf("錄製檔要清掉：%s", out)
	}
}
