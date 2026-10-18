// Package onboard 是首次設定（PRD ON-3、ON-4，畫板 A03～A10）：偵測這台電腦上已登入的 AI 工具與本機模型、
// Bot 電腦映像好了沒，並記住首次設定、導覽與首頁「之後再做」卡片的狀態。
package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"

	"xbot/internal/engines"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Setting 是存首次設定狀態的設定名稱（setting 表）。
const Setting = "onboard"

// Cards 是首頁「之後再做」卡片（A05）。
var Cards = []string{"import", "phone", "tour"}

// LocalServer 是一個要偵測的本機模型服務。
type LocalServer struct {
	ID      string // lmstudio、ollama
	Label   string
	BaseURL string // OpenAI 相容的網址（…/v1）
	Preset  string // 內建範本代號（xbot.models.presets）；沒有範本時空白
}

// DefaultLocal 是預設偵測的本機模型服務：LM Studio 與 Ollama 的預設埠。
var DefaultLocal = []LocalServer{
	{ID: "lmstudio", Label: "LM Studio", BaseURL: "http://127.0.0.1:1234/v1", Preset: "lmstudio"},
	{ID: "ollama", Label: "Ollama", BaseURL: "http://127.0.0.1:11434/v1"},
}

// Deps 是首次設定需要的元件；都可以是 nil（只登錄定義或測試）。
type Deps struct {
	// Engines 回答這台電腦上各訂閱 CLI（Claude Code、Codex、Gemini CLI、OpenCode、Grok）有沒有登入（engines.Service.HostLogins）。
	Engines func() []engines.HostLogin
	// Image 回傳這台的 Bot 電腦映像名稱與好了沒；err 表示 Docker 連不上（hosts.Service.BoxImage）。
	Image func(ctx context.Context) (name string, ready bool, err error)
	// Local 是要偵測的本機模型服務；nil 用 DefaultLocal。
	Local []LocalServer
	// Client 是偵測本機模型用的 HTTP client；nil 用 http.DefaultClient（每個網址各自限時）。
	Client *http.Client
	// Legacy 找這台電腦上 LazyBoy、OpenMausBot 的舊資料（importer.Service.Detect 的摘要）；nil 時不找。
	Legacy func(ctx context.Context) Legacy
}

// Legacy 是找到的舊資料（A05「帶入舊設定」卡片只在 Found 時出現）。
type Legacy struct {
	Found   bool   `json:"found" desc:"找到還沒帶進來的舊資料"`
	Summary string `json:"summary,omitempty" desc:"卡片上的一句話，例：在這台電腦找到 LazyBoy 的 3 個 Bot、2 把金鑰和 4 個定時任務。"`
	Sources int    `json:"sources" desc:"找到幾份舊資料"`
}

// State 是首次設定的狀態（存在 setting 表）。
type State struct {
	Completed   bool     `json:"completed" desc:"首次設定做完或跳過了"`
	CompletedAt int64    `json:"completed_at,omitempty"`
	TourDone    bool     `json:"tour_done" desc:"導覽看完或跳過了（A10）"`
	Dismissed   []string `json:"dismissed" desc:"首頁「之後再做」關掉的卡片：import、phone、tour"`
}

// Engine 是這台電腦上一個訂閱 CLI 的登入狀態。
type Engine struct {
	Engine   string `json:"engine" desc:"claude-code、codex、gemini、opencode 或 grok"`
	Label    string `json:"label"`
	Plan     string `json:"plan" desc:"訂閱方案名稱，例：Claude"`
	LoggedIn bool   `json:"logged_in"`
	Login    string `json:"login" desc:"沒有登入時，在這台電腦上執行的指令"`
}

// Local 是偵測到的本機模型服務。
type Local struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	BaseURL string   `json:"base_url"`
	Preset  string   `json:"preset,omitempty" desc:"內建範本代號；沒有時用自訂供應商（base_url、chat_completions）"`
	Models  []string `json:"models" desc:"服務列出的模型"`
}

// Computer 是 Bot 電腦準備好了沒（ON-3「建電腦」）。
type Computer struct {
	Docker bool   `json:"docker" desc:"這台的 Docker 連得上"`
	Image  string `json:"image" desc:"Bot 電腦映像名稱"`
	Ready  bool   `json:"ready" desc:"映像已經建好；Bot 第一次收到工作時就能開機"`
	Hint   string `json:"hint,omitempty" desc:"還沒好時該怎麼做"`
	Error  string `json:"error,omitempty"`
}

// StatusIn 是 xbot.onboard.status 的輸入。
type StatusIn struct {
	SkipDetect bool `json:"skip_detect,omitempty" desc:"不偵測本機模型與電腦映像（只看狀態，比較快）"`
}

// StatusOut 是 xbot.onboard.status 的輸出。
type StatusOut struct {
	FirstRun bool     `json:"first_run" desc:"還沒有任何 Bot、也還沒做完首次設定：顯示首次設定"`
	Bots     int      `json:"bots" desc:"Bot 數（含封存）"`
	State    State    `json:"state"`
	Engines  []Engine `json:"engines" desc:"這台電腦上已登入的訂閱 CLI"`
	Local    []Local  `json:"local" desc:"偵測到的本機模型服務（LM Studio、Ollama）"`
	Computer Computer `json:"computer"`
	Legacy   Legacy   `json:"legacy" desc:"這台電腦上 LazyBoy、OpenMausBot 的舊資料（首頁「帶入舊設定」）"`
}

// UpdateIn 是 xbot.onboard.update 的輸入：有給的欄位才改。
type UpdateIn struct {
	Completed *bool  `json:"completed,omitempty" desc:"首次設定做完（或跳過）"`
	TourDone  *bool  `json:"tour_done,omitempty" desc:"導覽看完（或跳過）；false 表示再看一次"`
	Dismiss   string `json:"dismiss,omitempty" desc:"關掉首頁的一張「之後再做」卡片"`
	Restore   bool   `json:"restore,omitempty" desc:"把關掉的卡片都放回來"`
}

// Register 登錄 xbot.onboard.status 與 xbot.onboard.update。
func Register(reg *ops.Registry, d Deps) {
	ops.Register(reg, ops.Op[StatusIn, StatusOut]{
		Name: "xbot.onboard.status", Summary: "首次設定的狀態：已登入的 AI 工具、本機模型、Bot 電腦映像",
		Description: "偵測這台電腦上已登入的 Claude Code、Codex（只看有沒有登入，不讀憑證），本機的 LM Studio、Ollama 有哪些模型，" +
			"以及 Bot 電腦映像建好了沒。還沒有任何 Bot、也還沒做完首次設定時 first_run 是 true。",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in StatusIn) (StatusOut, error) {
			return d.status(ctx, call, in)
		},
	})
	ops.Register(reg, ops.Op[UpdateIn, State]{
		Name: "xbot.onboard.update", Summary: "記下首次設定、導覽與首頁卡片的狀態",
		Scope: ops.ScopeManage, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in UpdateIn) (State, error) {
			if in.Dismiss != "" && !slices.Contains(Cards, in.Dismiss) {
				return State{}, ops.InvalidInput(ops.FieldError{Field: "dismiss", Message: "只能是 import、phone、tour"})
			}
			st, err := Load(ctx, call.DB)
			if err != nil {
				return st, err
			}
			if in.Completed != nil {
				st.Completed = *in.Completed
				st.CompletedAt = 0
				if st.Completed {
					st.CompletedAt = store.NowMS()
				}
			}
			if in.TourDone != nil {
				st.TourDone = *in.TourDone
			}
			if in.Restore {
				st.Dismissed = []string{}
			}
			if in.Dismiss != "" && !slices.Contains(st.Dismissed, in.Dismiss) {
				st.Dismissed = append(st.Dismissed, in.Dismiss)
			}
			if call.DryRun {
				return st, nil
			}
			return st, call.DB.SetSetting(ctx, Setting, st)
		},
	})
}

// Load 讀首次設定狀態；沒有存過時是空的。
func Load(ctx context.Context, db *store.DB) (State, error) {
	st := State{Dismissed: []string{}}
	raw, err := db.GetSetting(ctx, Setting)
	if errors.Is(err, store.ErrNotFound) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return State{Dismissed: []string{}}, nil // 壞掉的設定當成沒存過
	}
	if st.Dismissed == nil {
		st.Dismissed = []string{}
	}
	return st, nil
}

func (d Deps) status(ctx context.Context, call *ops.Call, in StatusIn) (StatusOut, error) {
	st, err := Load(ctx, call.DB)
	if err != nil {
		return StatusOut{}, err
	}
	n, err := call.DB.CountBots(ctx)
	if err != nil {
		return StatusOut{}, err
	}
	out := StatusOut{Bots: n, State: st, FirstRun: n == 0 && !st.Completed, Engines: []Engine{}, Local: []Local{}}
	if d.Engines != nil {
		for _, h := range d.Engines() {
			out.Engines = append(out.Engines, Engine{Engine: h.Engine, Label: h.Label, Plan: h.Plan, LoggedIn: h.LoggedIn, Login: h.Login})
		}
	}
	if d.Legacy != nil {
		// 只看檔案，很快；skip_detect 時也找（首頁卡片要用）。
		out.Legacy = d.Legacy(ctx)
	}
	if in.SkipDetect {
		return out, nil
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		out.Local = d.detectLocal(ctx)
	}()
	go func() {
		defer wg.Done()
		out.Computer = d.computer(ctx)
	}()
	wg.Wait()
	return out, nil
}

func (d Deps) computer(ctx context.Context) Computer {
	if d.Image == nil {
		return Computer{Hint: "這個服務沒有管理 Bot 電腦"}
	}
	ctx, cancel := context.WithTimeout(ctx, limits.OnboardImageCheckTimeout)
	defer cancel()
	name, ready, err := d.Image(ctx)
	c := Computer{Image: name, Ready: ready, Docker: err == nil}
	switch {
	case err != nil:
		c.Error = err.Error()
		c.Hint = "Docker 沒有開：先在這台電腦啟動 Docker，再執行 make box"
	case !ready:
		c.Hint = "還沒有 Bot 電腦映像：在這台電腦執行 make box（第一次約 10 分鐘），好了這裡會自動更新"
	}
	return c
}

// detectLocal 同時問每個本機模型服務的 /models；沒開的略過。
func (d Deps) detectLocal(ctx context.Context) []Local {
	servers := d.Local
	if servers == nil {
		servers = DefaultLocal
	}
	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
	found := make([]*Local, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			models, ok := listModels(ctx, client, s.BaseURL)
			if ok {
				found[i] = &Local{ID: s.ID, Label: s.Label, BaseURL: s.BaseURL, Preset: s.Preset, Models: models}
			}
		}()
	}
	wg.Wait()
	out := []Local{}
	for _, l := range found {
		if l != nil {
			out = append(out, *l)
		}
	}
	return out
}

// listModels 讀 OpenAI 相容的 GET <base>/models；連不上或格式不對回 false。
func listModels(ctx context.Context, client *http.Client, base string) ([]string, bool) {
	ctx, cancel := context.WithTimeout(ctx, limits.LocalModelProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/models", nil)
	if err != nil {
		return nil, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, limits.LocalModelListBytes)).Decode(&body); err != nil {
		return nil, false
	}
	models := []string{}
	for _, m := range body.Data {
		if m.ID != "" && len(models) < limits.LocalModelsMax {
			models = append(models, m.ID)
		}
	}
	return models, true
}
