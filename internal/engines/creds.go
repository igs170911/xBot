// Package engines 管訂閱 CLI 的登入憑證（PRD EN-2a、EN-2b、SEC-5 例外）與 xbot.engines.* 操作。
//
// 方案 A：Claude Code／Codex CLI（E31 起加上 Gemini CLI、OpenCode、Grok）跑在 Bot 自己的電腦裡，要用你在這台電腦上的訂閱登入。
// 只有你本人逐 Bot 同意（xbot.engines.link_credentials）後，才把這台電腦上的 CLI 憑證檔複製進那個 Bot 的電腦：
//   - 放在 /opt/xbot/cred/<引擎>/（這個 Bot 專用的 xbot-box-<id>-cred volume；B13 起根檔案系統唯讀，不能再放在容器的檔案層），
//     權限 0400、擁有者是 Bot 使用者，所在資料夾屬於 root，Bot 不能換掉或刪掉它；不在 home／workspace 裡，刪除電腦資料時一起刪。
//   - Bot 家目錄裡的 ~/.claude/.credentials.json、~/.codex/auth.json 是指向它的連結，CLI 照常找得到。
//   - 每次開機前（box Prepare）從這台電腦重新複製一次，換到最新的權杖。
//
// 收回（xbot.engines.unlink_credentials）：立刻刪掉同意紀錄（驅動之後拒絕這個 Bot）、結束正在跑的 agent 行程，
// 再把容器裡的憑證換成空的內容；容器暫時連不上時記成「已收回、待清除」，下次開機前清掉。
//
// Claude 訂閱只經 Claude Code 本身使用（EN-2a）：只有 bot.engine 是 claude-code 的 Bot 能連結 Claude 憑證，
// Hermes 的 Bot 永遠拿不到；Model Proxy 也不收 Claude 的 OAuth 權杖（internal/models）。
package engines

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/engine/acp"
	"xbot/internal/engine/hermes"
	"xbot/internal/store"
)

// 容器內的位置。
const (
	// CredDir 是憑證所在的資料夾（xbot-box-<id>-cred volume 的掛載點，一開始是映像裡空的、屬於 root 的資料夾）。
	CredDir = "/opt/xbot/cred"
	// BoxHome 是 Bot 的家目錄。
	BoxHome = "/home/box"
	// maxCredBytes 是一個憑證檔的上限（正常只有幾 KB）。
	maxCredBytes = 1 << 20
)

// Spec 是一種訂閱 CLI 的憑證檔。
type Spec struct {
	Engine string
	// Label 是 CLI 名稱；Plan 是訂閱名稱（介面標示「這個 Bot 用你的 <Plan> 訂閱」）。
	Label, Plan string
	// EnvDir 是 CLI 改設定資料夾用的環境變數；沒設時用家目錄下的 Dir。
	EnvDir, Dir string
	// File 是憑證檔名（在設定資料夾裡）。
	File string
	// Login 是在這台電腦上登入的指令（找不到憑證時告訴使用者）。
	Login string
	// EnvSub 是 EnvDir 指到的資料夾底下、憑證所在的子資料夾（例：XDG_DATA_HOME/opencode）。
	EnvSub string
	// Filter 在放進 Bot 電腦之前整理憑證檔（例：OpenCode 的 auth.json 拿掉 Claude 的登入，EN-2a）。可為 nil。
	Filter func([]byte) ([]byte, error)
	// HostLoginRequired：CLI 找不到登入時會停下來等人輸入（例：Gemini CLI 印出網址、等授權碼），
	// 所以這台電腦上登出以後，就算同意還在也不開 agent。
	HostLoginRequired bool
}

// Specs 是支援連結訂閱的引擎。
//   - Claude Code（Linux）：~/.claude/.credentials.json（CLAUDE_CONFIG_DIR 可改）。macOS 的 Claude Code 把登入放在鑰匙圈，這裡讀不到。
//   - Codex CLI：~/.codex/auth.json（CODEX_HOME 可改）。
//   - Gemini CLI（E31）：~/.gemini/oauth_creds.json（GEMINI_CLI_HOME 可改家目錄）。用 Google 帳號登入（Gemini Code Assist 的額度）。
//   - OpenCode（E31）：~/.local/share/opencode/auth.json（XDG_DATA_HOME 可改）。OpenCode Go／Zen 的金鑰；
//     裡面如果有 Claude（anthropic）的登入，放進 Bot 電腦前拿掉（EN-2a：Claude 訂閱只經 Claude Code 本身）。
//   - Grok（E31）：~/.grok/auth.json（GROK_HOME 可改）。用 grok.com 帳號登入（SuperGrok 等方案）。
var Specs = map[string]Spec{
	acp.EngineClaude: {Engine: acp.EngineClaude, Label: "Claude Code", Plan: "Claude", EnvDir: "CLAUDE_CONFIG_DIR", Dir: ".claude", File: ".credentials.json", Login: "claude（再輸入 /login）"},
	acp.EngineCodex:  {Engine: acp.EngineCodex, Label: "Codex CLI", Plan: "ChatGPT", EnvDir: "CODEX_HOME", Dir: ".codex", File: "auth.json", Login: "codex login"},
	acp.EngineGemini: {Engine: acp.EngineGemini, Label: "Gemini CLI", Plan: "Google", EnvDir: "GEMINI_CLI_HOME", EnvSub: ".gemini", Dir: ".gemini",
		File: "oauth_creds.json", Login: "gemini（選 Login with Google）", HostLoginRequired: true},
	acp.EngineOpenCode: {Engine: acp.EngineOpenCode, Label: "OpenCode", Plan: "OpenCode Go", EnvDir: "XDG_DATA_HOME", EnvSub: "opencode",
		Dir: ".local/share/opencode", File: "auth.json", Login: "opencode auth login", Filter: withoutClaude},
	acp.EngineGrok: {Engine: acp.EngineGrok, Label: "Grok", Plan: "Grok", EnvDir: "GROK_HOME", Dir: ".grok", File: "auth.json", Login: "grok login"},
}

// claudeProviders 是 OpenCode auth.json 裡屬於 Claude 訂閱的項目。
var claudeProviders = []string{"anthropic", "claude", "claude-code"}

// withoutClaude 拿掉 OpenCode auth.json 裡 Claude 的登入（EN-2a）；拿掉後沒有別的登入時當成沒有登入。
func withoutClaude(data []byte) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%w：OpenCode 的 auth.json 看不懂", ErrNoHostLogin)
	}
	for _, k := range claudeProviders {
		delete(m, k)
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("%w：OpenCode 裡只有 Claude 的登入（Claude 訂閱只能經 Claude Code 使用）。請先執行 opencode auth login 登入 OpenCode Go 或 Zen", ErrNoHostLogin)
	}
	out, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Marker 是介面上的標示文字。
func (s Spec) Marker() string { return "這個 Bot 用你的 " + s.Plan + " 訂閱" }

// target 是容器裡真正放憑證的位置；CLI 從家目錄 BoxHome/<Dir>/<File> 的連結找到它。
func (s Spec) target() string { return path.Join(CredDir, s.Engine, s.File) }

// Link 是使用者對一個 Bot 的同意紀錄（setting：engine.link.<botID>）。
type Link struct {
	Engine   string `json:"engine"`
	LinkedAt int64  `json:"linked_at"`
	// Revoked 表示已收回，但容器裡的憑證還沒確定清掉（下次開機前清）。
	Revoked bool `json:"revoked,omitempty"`
}

func settingKey(botID string) string { return "engine.link." + botID }

// Container 是 Bot 的電腦：Docker API 與容器 id；容器不存在時 ID 是空的。
type Container struct {
	API     Files
	ID      string
	Running bool
}

// Files 是讀寫憑證用到的 Docker API；hosts.DockerAPI 符合。
type Files interface {
	CopyToContainer(ctx context.Context, id string, o client.CopyToContainerOptions) (client.CopyToContainerResult, error)
	CopyFromContainer(ctx context.Context, id string, o client.CopyFromContainerOptions) (client.CopyFromContainerResult, error)
}

// Service 處理連結、收回與開機前的同步。
type Service struct {
	DB *store.DB
	// HostHome 是這台電腦（XBot 服務）的使用者家目錄；Getenv 讀 CLAUDE_CONFIG_DIR 等。
	HostHome string
	Getenv   func(string) string
	// Locate 找 Bot 的電腦（不開機）。可為 nil（只記同意，開機時再放憑證）。
	Locate func(ctx context.Context, botID string) (Container, error)
	// Close 結束 Bot 正在跑的 agent 行程（收回時）。
	Close func(botID string)
	// UID、GID 是容器裡的 Bot 使用者。
	UID, GID int
	Logger   *slog.Logger

	// 換引擎（EN-7，xbot.engines.switch）用到的；都可為 nil。
	// Models 列出 Hermes 可以換用的模型；Busy 回答 Bot 是不是正在做事；Forget 清掉 Bot 記下的額度錯誤；
	// Resend 在對話串裡以你的名義再送一次訊息，回傳新工作的 id。
	Models func(ctx context.Context) ([]ModelChoice, error)
	Busy   func(botID string) bool
	Forget func(botID string)
	Resend func(ctx context.Context, botID, threadID, text string) (string, error)

	// Bot 自己登入（own.go）用到的；都可為 nil（不能在網頁上登入）。
	// Exec 在 Bot 的電腦裡（必要時先開機）以 root 開行程，HOME 是 home：跑 CLI 的登入指令、用 own 資料夾裡的憑證確認登入。
	// Launch 照平常的方式（Bot 使用者）開 agent：Bot 正在用那個 CLI 時確認登入。
	Exec   func(ctx context.Context, botID, home string, a acp.Agent) (acp.Process, error)
	Launch acp.Launcher

	loginMu sync.Mutex
	logins  map[string]*login
}

func (s *Service) log() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}

// HostPath 回傳這台電腦上某個 CLI 的憑證檔位置。
func (s *Service) HostPath(sp Spec) string {
	getenv := s.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	dir := ""
	if sp.EnvDir != "" {
		dir = getenv(sp.EnvDir)
	}
	if dir != "" {
		dir = filepath.Join(dir, sp.EnvSub)
	} else {
		dir = filepath.Join(s.HostHome, sp.Dir)
	}
	return filepath.Join(dir, sp.File)
}

// ErrNoHostLogin 表示這台電腦上沒有那個 CLI 的登入。
var ErrNoHostLogin = errors.New("這台電腦上找不到登入")

// ReadHost 讀這台電腦上的憑證檔。
func (s *Service) ReadHost(sp Spec) ([]byte, error) {
	p := s.HostPath(sp)
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w：%s 的憑證不在 %s。請先在這台電腦執行 %s 登入", ErrNoHostLogin, sp.Label, p, sp.Login)
	}
	if err != nil {
		return nil, fmt.Errorf("讀不到 %s 的憑證（%s）：%w", sp.Label, p, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCredBytes+1))
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 || len(data) > maxCredBytes {
		return nil, fmt.Errorf("%w：%s 的憑證檔（%s）是空的或不對", ErrNoHostLogin, sp.Label, p)
	}
	if sp.Filter != nil {
		return sp.Filter(data)
	}
	return data, nil
}

// Get 讀 Bot 的同意紀錄。
func (s *Service) Get(ctx context.Context, botID string) (Link, bool, error) {
	var l Link
	ok, err := s.DB.GetSettingInto(ctx, settingKey(botID), &l)
	return l, ok, err
}

// Linked 回答 Bot 現在能不能用這個引擎的訂閱（已同意、沒收回、引擎相同）。
func (s *Service) Linked(ctx context.Context, botID, engine string) (bool, error) {
	l, ok, err := s.Get(ctx, botID)
	if err != nil {
		return false, err
	}
	return ok && !l.Revoked && l.Engine == engine, nil
}

// ErrNotLinked 表示 Bot 沒有（或已收回）連結訂閱。
var ErrNotLinked = errors.New("沒有連結訂閱")

// Ready 回答 Bot 現在能不能用這個 CLI：自己登入過（沒登出），或同意用這台電腦的登入（引擎相同）。
func (s *Service) Ready(ctx context.Context, botID, engine string) (bool, error) {
	if own, err := s.HasOwn(ctx, botID, engine); err != nil || own {
		return own, err
	}
	return s.Linked(ctx, botID, engine)
}

// Authorize 實作 acp.Authorizer：Bot 沒有自己登入、也沒同意用這台電腦的登入，就不讓驅動開 agent。
func (s *Service) Authorize(ctx context.Context, botID, engine string) error {
	sp, ok := Specs[engine]
	if !ok {
		return nil
	}
	if own, err := s.HasOwn(ctx, botID, engine); err != nil || own {
		return err
	}
	linked, err := s.Linked(ctx, botID, engine)
	if err != nil {
		return err
	}
	if !linked {
		return fmt.Errorf("%w：這個 Bot 還沒登入 %s。請到 Bot 設定的「AI 帳號與模型」登入（xbot.engines.login_start）", ErrNotLinked, sp.Label)
	}
	if sp.HostLoginRequired {
		if _, err := os.Stat(s.HostPath(sp)); err != nil {
			return fmt.Errorf("%w：這台電腦上的 %s 登入不見了，請先在這台電腦執行 %s 再登入一次", ErrNoHostLogin, sp.Label, sp.Login)
		}
	}
	return nil
}

// Link 記下同意，並把憑證放進 Bot 的電腦（電腦已存在時；還沒建立的話開機前會放）。
func (s *Service) Link(ctx context.Context, botID, engine string) (Link, error) {
	sp, ok := Specs[engine]
	if !ok {
		return Link{}, fmt.Errorf("不支援連結 %s 的訂閱", engine)
	}
	data, err := s.ReadHost(sp)
	if err != nil {
		return Link{}, err
	}
	// 改用這台電腦的登入：Bot 自己登入的那份不留（同一個 CLI 只用一份登入）。
	if own, err := s.HasOwn(ctx, botID, engine); err != nil {
		return Link{}, err
	} else if own {
		if _, err := s.Logout(ctx, botID, engine); err != nil {
			return Link{}, err
		}
	}
	l := Link{Engine: engine, LinkedAt: time.Now().UnixMilli()}
	if err := s.DB.SetSetting(ctx, settingKey(botID), l); err != nil {
		return Link{}, err
	}
	if s.Locate == nil {
		return l, nil
	}
	c, err := s.Locate(ctx, botID)
	if err != nil {
		// 同意已記下；開機前會再放一次。
		s.log().Warn("暫時無法把憑證放進 Bot 的電腦，開機時再放", "bot", botID, "err", err)
		return l, nil
	}
	if c.ID != "" {
		if err := s.install(ctx, c, sp, data); err != nil {
			s.log().Warn("暫時無法把憑證放進 Bot 的電腦，開機時再放", "bot", botID, "err", err)
		}
	}
	return l, nil
}

// Unlink 收回：刪掉同意（驅動立刻拒絕）、結束 agent 行程、清掉容器裡的憑證；Bot 自己登入的也全部登出。
// 清不掉時（主機離線）記成待清除，下次開機前清；回傳的 cleared 表示容器裡已確定沒有憑證。
func (s *Service) Unlink(ctx context.Context, botID string) (cleared bool, err error) {
	own, err := s.owns(ctx, botID)
	if err != nil {
		return false, err
	}
	cleared = true
	for _, e := range slices.Sorted(maps.Keys(own)) {
		ok, err := s.Logout(ctx, botID, e)
		if err != nil {
			return false, err
		}
		cleared = cleared && ok
	}
	hostCleared, err := s.unlinkHost(ctx, botID)
	return cleared && hostCleared, err
}

// unlinkHost 收回用這台電腦的登入。
func (s *Service) unlinkHost(ctx context.Context, botID string) (cleared bool, err error) {
	l, ok, err := s.Get(ctx, botID)
	if err != nil || !ok {
		return true, err
	}
	l.Revoked = true
	if err := s.DB.SetSetting(ctx, settingKey(botID), l); err != nil {
		return false, err
	}
	if s.Close != nil {
		s.Close(botID)
	}
	if s.Locate == nil {
		return false, nil
	}
	c, err := s.Locate(ctx, botID)
	if err != nil {
		s.log().Warn("暫時無法清掉 Bot 電腦裡的憑證，開機前再清", "bot", botID, "err", err)
		return false, nil
	}
	if c.ID != "" {
		if err := s.clear(ctx, c, Specs[l.Engine]); err != nil {
			s.log().Warn("暫時無法清掉 Bot 電腦裡的憑證，開機前再清", "bot", botID, "err", err)
			return false, nil
		}
	}
	return true, s.DB.DeleteSetting(ctx, settingKey(botID))
}

// Prepare 在每次開機前（box Prepare）呼叫：Bot 自己登入的照現在的引擎搬好（Sync）；
// 用這台電腦的登入：已同意且引擎相同就放最新的憑證，已收回或引擎換了就清掉。
func (s *Service) Prepare(ctx context.Context, c Container, b store.Bot) error {
	if err := s.syncOwn(ctx, c, b); err != nil {
		return err
	}
	l, ok, err := s.Get(ctx, b.ID)
	if err != nil || !ok {
		return err
	}
	sp, known := Specs[l.Engine]
	if !known {
		return nil
	}
	if own, err := s.HasOwn(ctx, b.ID, l.Engine); err != nil || own {
		// 同一個 CLI 已經自己登入（adopt 會刪掉這筆同意；這裡只是保險）：不要拿這台電腦的那份蓋掉。
		return err
	}
	engine := b.Engine
	if engine == "" {
		engine = hermes.Name
	}
	switch {
	case l.Revoked:
		if err := s.clear(ctx, c, sp); err != nil {
			return err
		}
		return s.DB.DeleteSetting(ctx, settingKey(b.ID))
	case l.Engine != engine:
		// Bot 換了引擎（例：改回 Hermes）：憑證不留在它的電腦裡（EN-2a）。
		return s.clear(ctx, c, sp)
	}
	data, err := s.ReadHost(sp)
	if err != nil {
		// 這台電腦上登出了：清掉舊的，Bot 用的時候會說要登入。不擋開機。
		s.log().Warn("這台電腦上找不到訂閱登入，清掉 Bot 電腦裡的舊憑證", "bot", b.ID, "engine", l.Engine, "err", err)
		return s.clear(ctx, c, sp)
	}
	return s.install(ctx, c, sp, data)
}

func (s *Service) install(ctx context.Context, c Container, sp Spec, data []byte) error {
	return s.write(ctx, c, sp, data)
}

// clear 把容器裡的憑證換成空的 JSON（連結也重寫：CLI 若把更新後的權杖寫成家目錄裡的一般檔案，一起蓋掉）。
func (s *Service) clear(ctx context.Context, c Container, sp Spec) error {
	return s.write(ctx, c, sp, []byte("{}\n"))
}

// write 寫兩份 tar：/opt/xbot/cred/<引擎>/<檔名>（0400，Bot 擁有；資料夾屬於 root），以及家目錄裡指向它的連結。
func (s *Service) write(ctx context.Context, c Container, sp Spec, data []byte) error {
	now := time.Now()
	cred, err := tarball([]*tar.Header{
		{Name: sp.Engine + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: now},
		{Name: path.Join(sp.Engine, sp.File), Typeflag: tar.TypeReg, Mode: 0o400, Size: int64(len(data)), Uid: s.UID, Gid: s.GID, ModTime: now},
	}, data)
	if err != nil {
		return err
	}
	if _, err := c.API.CopyToContainer(ctx, c.ID, client.CopyToContainerOptions{DestinationPath: CredDir, Content: cred}); err != nil {
		return fmt.Errorf("寫入 Bot 電腦的 %s 憑證失敗：%w", sp.Label, err)
	}
	// 設定資料夾可能有好幾層（例：.local/share/opencode）：每一層都由 Bot 擁有，不讓 Docker 用 root 補建上層。
	var dirs []*tar.Header
	parts := strings.Split(sp.Dir, "/")
	for i := range parts {
		mode := int64(0o755)
		if i == len(parts)-1 {
			mode = 0o700
		}
		dirs = append(dirs, &tar.Header{Name: strings.Join(parts[:i+1], "/") + "/", Typeflag: tar.TypeDir, Mode: mode, Uid: s.UID, Gid: s.GID, ModTime: now})
	}
	ln, err := tarball(
		append(dirs, &tar.Header{Name: path.Join(sp.Dir, sp.File), Typeflag: tar.TypeSymlink, Linkname: sp.target(), Mode: 0o777, Uid: s.UID, Gid: s.GID, ModTime: now}),
		nil,
	)
	if err != nil {
		return err
	}
	if _, err := c.API.CopyToContainer(ctx, c.ID, client.CopyToContainerOptions{DestinationPath: BoxHome, Content: ln}); err != nil {
		return fmt.Errorf("寫入 Bot 電腦的 %s 設定失敗：%w", sp.Label, err)
	}
	return nil
}

// tarball 打包資料夾和最後的一個項目（data 給最後那個一般檔案用）。
func tarball(headers []*tar.Header, data []byte) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range headers {
		if err := tw.WriteHeader(h); err != nil {
			return nil, err
		}
	}
	if len(data) > 0 {
		if _, err := tw.Write(data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}
