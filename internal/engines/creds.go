// Package engines 管訂閱 CLI 的登入憑證（PRD EN-2a、EN-2b、SEC-5 例外）與 xbot.engines.* 操作。
//
// 方案 A：Claude Code／Codex CLI 跑在 Bot 自己的電腦裡，要用你在這台電腦上的訂閱登入。
// 只有你本人逐 Bot 同意（xbot.engines.link_credentials）後，才把這台電腦上的 CLI 憑證檔複製進那個 Bot 的電腦：
//   - 放在容器自己的檔案層 /opt/xbot/cred/<引擎>/（不在 home／workspace volume 裡），權限 0400、擁有者是 Bot 使用者，
//     所在資料夾屬於 root，Bot 不能換掉或刪掉它；重建容器就消失。
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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/moby/moby/client"

	"xbot/internal/engine/acp"
	"xbot/internal/engine/hermes"
	"xbot/internal/store"
)

// 容器內的位置。
const (
	// CredDir 是憑證在容器檔案層裡的資料夾（box/Dockerfile 建好，屬於 root）。
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
}

// Specs 是支援連結訂閱的引擎。
//   - Claude Code（Linux）：~/.claude/.credentials.json（CLAUDE_CONFIG_DIR 可改）。macOS 的 Claude Code 把登入放在鑰匙圈，這裡讀不到。
//   - Codex CLI：~/.codex/auth.json（CODEX_HOME 可改）。
var Specs = map[string]Spec{
	acp.EngineClaude: {Engine: acp.EngineClaude, Label: "Claude Code", Plan: "Claude", EnvDir: "CLAUDE_CONFIG_DIR", Dir: ".claude", File: ".credentials.json", Login: "claude（再輸入 /login）"},
	acp.EngineCodex:  {Engine: acp.EngineCodex, Label: "Codex CLI", Plan: "ChatGPT", EnvDir: "CODEX_HOME", Dir: ".codex", File: "auth.json", Login: "codex login"},
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

// Files 是寫憑證用到的 Docker API；hosts.DockerAPI 符合。
type Files interface {
	CopyToContainer(ctx context.Context, id string, o client.CopyToContainerOptions) (client.CopyToContainerResult, error)
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
	dir := getenv(sp.EnvDir)
	if dir == "" {
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

// Authorize 實作 acp.Authorizer：沒有同意就不讓驅動開 agent。
func (s *Service) Authorize(ctx context.Context, botID, engine string) error {
	sp, ok := Specs[engine]
	if !ok {
		return nil
	}
	linked, err := s.Linked(ctx, botID, engine)
	if err != nil {
		return err
	}
	if !linked {
		return fmt.Errorf("%w：這個 Bot 還沒有使用你的 %s 訂閱。請到 Bot 設定同意連結（xbot.engines.link_credentials）", ErrNotLinked, sp.Plan)
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

// Unlink 收回：刪掉同意（驅動立刻拒絕）、結束 agent 行程、清掉容器裡的憑證。
// 清不掉時（主機離線）記成待清除，下次開機前清；回傳的 cleared 表示容器裡已確定沒有憑證。
func (s *Service) Unlink(ctx context.Context, botID string) (cleared bool, err error) {
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

// Prepare 在每次開機前（box Prepare）呼叫：已同意且引擎相同就放最新的憑證；已收回或引擎換了就清掉。
func (s *Service) Prepare(ctx context.Context, c Container, b store.Bot) error {
	l, ok, err := s.Get(ctx, b.ID)
	if err != nil || !ok {
		return err
	}
	sp, known := Specs[l.Engine]
	if !known {
		return nil
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
	cred, err := tarball(
		&tar.Header{Name: sp.Engine + "/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: now},
		&tar.Header{Name: path.Join(sp.Engine, sp.File), Typeflag: tar.TypeReg, Mode: 0o400, Size: int64(len(data)), Uid: s.UID, Gid: s.GID, ModTime: now},
		data,
	)
	if err != nil {
		return err
	}
	if _, err := c.API.CopyToContainer(ctx, c.ID, client.CopyToContainerOptions{DestinationPath: CredDir, Content: cred, CopyUIDGID: true}); err != nil {
		return fmt.Errorf("寫入 Bot 電腦的 %s 憑證失敗：%w", sp.Label, err)
	}
	ln, err := tarball(
		&tar.Header{Name: sp.Dir + "/", Typeflag: tar.TypeDir, Mode: 0o700, Uid: s.UID, Gid: s.GID, ModTime: now},
		&tar.Header{Name: path.Join(sp.Dir, sp.File), Typeflag: tar.TypeSymlink, Linkname: sp.target(), Mode: 0o777, Uid: s.UID, Gid: s.GID, ModTime: now},
		nil,
	)
	if err != nil {
		return err
	}
	if _, err := c.API.CopyToContainer(ctx, c.ID, client.CopyToContainerOptions{DestinationPath: BoxHome, Content: ln, CopyUIDGID: true}); err != nil {
		return fmt.Errorf("寫入 Bot 電腦的 %s 設定失敗：%w", sp.Label, err)
	}
	return nil
}

// tarball 打包一個資料夾和裡面的一個項目（data 給一般檔案用）。
func tarball(dir, item *tar.Header, data []byte) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(dir); err != nil {
		return nil, err
	}
	if err := tw.WriteHeader(item); err != nil {
		return nil, err
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
