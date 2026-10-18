package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"xbot/internal/boxfs"
	"xbot/internal/limits"
	"xbot/internal/store"
)

// DockerBrowser 在 Bot 的電腦裡用 Playwright 經 CDP（127.0.0.1:9222）讀寫瀏覽器的 cookies（LazyBoy server/logins.py）。
// 電腦沒開機時先開機。要寫入的 cookies 先放進 Bot 家目錄（可寫的 volume）裡只有 Bot 能讀的暫存檔，讀完就刪掉。
type DockerBrowser struct {
	// Locate 找 Bot 的電腦（runtime.Computers.BotFS）。
	Locate boxfs.Locator
	// User 是在電腦裡執行的使用者（"1000:1000"）。
	User string
	// UID、GID 是暫存檔的擁有者。
	UID, GID int
}

// playwright 是映像裡的 Playwright（box/Dockerfile）。
const playwright = "/opt/xbot/playwright/node_modules/playwright/index.mjs"

// connect 等瀏覽器起來（剛開機時要一點時間）再接上。
const connectJS = `import { chromium } from '` + playwright + `';
setTimeout(() => process.exit(3), 30000);
let browser;
for (let i = 0; i < 30 && !browser; i++) {
  try { browser = await chromium.connectOverCDP('http://127.0.0.1:9222', { timeout: 3000 }); }
  catch { await new Promise(r => setTimeout(r, 1000)); }
}
if (!browser) process.exit(4);
const context = browser.contexts()[0];
if (!context) process.exit(2);
`

const exportJS = connectJS + `process.stdout.write(JSON.stringify(await context.cookies()));
process.exit(0);
`

func (d *DockerBrowser) computer(ctx context.Context, botID string) (*boxfs.Docker, error) {
	fs, err := d.Locate(ctx, botID, false)
	if err != nil {
		return nil, err
	}
	dk, ok := fs.(*boxfs.Docker)
	if !ok {
		return nil, errors.New("這台電腦不能執行指令")
	}
	if err := dk.EnsureRunning(ctx); err != nil {
		return nil, err
	}
	return dk, nil
}

func (d *DockerBrowser) run(ctx context.Context, dk *boxfs.Docker, script string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, limits.LoginScriptTimeout)
	defer cancel()
	out, err := dk.Output(ctx, d.User, limits.LoginCookiesBytes, "node", "--input-type=module", "-e", script)
	var exit *boxfs.ExitError
	if errors.As(err, &exit) && (exit.Code == 2 || exit.Code == 4) {
		return nil, errors.New("瀏覽器還沒開好，請稍後再試")
	}
	return out, err
}

// Cookies 實作 Browser。
func (d *DockerBrowser) Cookies(ctx context.Context, botID string) ([]Cookie, error) {
	dk, err := d.computer(ctx, botID)
	if err != nil {
		return nil, err
	}
	out, err := d.run(ctx, dk, exportJS)
	if err != nil {
		return nil, err
	}
	var rows []Cookie
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, errors.New("瀏覽器回傳的登入資料無法讀取")
	}
	return rows, nil
}

// AddCookies 實作 Browser。
func (d *DockerBrowser) AddCookies(ctx context.Context, botID string, cookies []Cookie) (int, error) {
	dk, err := d.computer(ctx, botID)
	if err != nil {
		return 0, err
	}
	data, err := json.Marshal(cookies)
	if err != nil {
		return 0, err
	}
	name := ".xbot-cookies-" + store.NewID() + ".json"
	if err := dk.WriteFiles(ctx, "/home/box", []boxfs.File{{Name: name, Data: data, Mode: 0o600}}); err != nil {
		return 0, err
	}
	p := "/home/box/" + name
	script := `import { readFile, unlink } from 'node:fs/promises';
const file = ` + strconv.Quote(p) + `;
const cookies = JSON.parse(await readFile(file, 'utf8'));
await unlink(file).catch(() => {});
` + connectJS + `await context.addCookies(cookies);
process.stdout.write(String(cookies.length));
process.exit(0);
`
	out, err := d.run(ctx, dk, script)
	if err != nil {
		_ = dk.Remove(context.WithoutCancel(ctx), p)
		return 0, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n, nil
}

// OpenURL 在 Bot 電腦的瀏覽器打開一個網頁（OAuth 登入頁，I03；同 LazyBoy connectors.open_page 的 box-chrome）。
// 只接受 http(s) 網址；網址當參數傳，不經過 shell 解讀。
func (d *DockerBrowser) OpenURL(ctx context.Context, botID, url string) error {
	if !webURL(url) {
		return errors.New("登入網址不安全")
	}
	dk, err := d.computer(ctx, botID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, limits.LoginScriptTimeout)
	defer cancel()
	_, err = dk.Output(ctx, d.User, 64<<10, "/bin/sh", "-c", `DISPLAY=:1 nohup /usr/local/bin/box-chrome "$0" >/dev/null 2>&1 &`, url)
	return err
}

var reEscape = regexp.MustCompile(`[.\-]`)

// ClearSites 實作 Browser：清掉這些網站（含子網域）的 cookies。
func (d *DockerBrowser) ClearSites(ctx context.Context, botID string, sites []string) error {
	dk, err := d.computer(ctx, botID)
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(connectJS)
	for _, s := range sites {
		if !siteRE.MatchString(s) {
			return fmt.Errorf("網站格式不對：%s", s)
		}
		fmt.Fprintf(&b, "await context.clearCookies({ domain: /(^|\\.)%s$/ });\n", reEscape.ReplaceAllString(s, `\$0`))
	}
	b.WriteString("process.exit(0);\n")
	_, err = d.run(ctx, dk, b.String())
	return err
}
