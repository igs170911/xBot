package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"xbot/internal/engine"
	"xbot/internal/limits"
	"xbot/internal/version"
)

// ProtocolVersion 是 XBot 講的 ACP 版本。
const ProtocolVersion = 1

// Workspace 是 session 的工作資料夾（Bot 電腦的 /workspace）。
const Workspace = "/workspace"

// Options 是 ACP 驅動的設定。
type Options struct {
	// Agent 是這個驅動負責的引擎（Agents 裡的一個）。
	Agent Agent
	// Launch 開 agent 行程。
	Launch Launcher
	// Authorize 在開 session 與送訊息前確認 Bot 可以用這個引擎；nil 表示不檢查。
	Authorize Authorizer
	// MCPServers 回傳要交給 agent 的 MCP 伺服器（desktop_mcp、XBot MCP）。
	MCPServers func(botID string) []MCPServer
	// OnModels 在開 session 時收到 agent 列出的模型（和目前用的）；nil 表示不用。
	OnModels func(botID string, current string, models []ModelChoice)
	// Instructions 回傳 Bot 的身分與守則（和 Hermes 的 SOUL.md 同一份：名稱、人設、基本守則、隊友與組織）；nil 表示不給。
	// agent 自己的系統提示只說它是 Claude Code、Grok…，不給的話它不知道自己是哪個 Bot（使用者回報 2026-10-19：
	// Grok 的 Bot 回「我是 Grok」、用簡體中文）。開 session 時拿一次：Agent.SystemPrompt 的放進系統提示，
	// 其他的加在新 session 的第一則訊息前面（接回的 session 歷史裡已經有了，不再重送）。
	Instructions func(ctx context.Context, botID string) (string, error)
	Logger       *slog.Logger
}

// ModelChoice 是 agent 列出的一個可選模型。
type ModelChoice struct {
	Value string `json:"value"`
	Name  string `json:"name,omitempty"`
}

// Driver 實作 engine.Driver。每個 Bot 開一個 agent 行程，所有對話串的 session 共用。
type Driver struct {
	o   Options
	log *slog.Logger

	mu    sync.Mutex
	bots  map[string]*agentConn
	dials map[string]*sync.Mutex

	// B2 (2026-10-22): 閒置 session 清理器
	stopIdleReaper chan struct{}
	reaperDone     chan struct{}
}

var _ engine.Driver = (*Driver)(nil)

// New 建立 ACP 驅動。
func New(o Options) *Driver {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	d := &Driver{
		o:              o,
		log:            o.Logger.With("engine", o.Agent.Engine),
		bots:           map[string]*agentConn{},
		dials:          map[string]*sync.Mutex{},
		stopIdleReaper: make(chan struct{}),
		reaperDone:     make(chan struct{}),
	}
	// B2 (2026-10-22): 啟動閒置 session 清理器
	go d.idleReaper()
	return d
}

// Name 實作 engine.Driver。
func (d *Driver) Name() string { return d.o.Agent.Engine }

// Shutdown 停止驅動並清理資源（B2, 2026-10-22）。
func (d *Driver) Shutdown() error {
	// B2 (2026-10-22): 停止閒置 session 清理器
	close(d.stopIdleReaper)
	<-d.reaperDone
	return nil
}

// idleReaper periodically checks for idle sessions and closes them (B2, 2026-10-22).
func (d *Driver) idleReaper() {
	defer close(d.reaperDone)
	ticker := time.NewTicker(30 * time.Second) // 每 30 秒檢查一次
	defer ticker.Stop()

	for {
		select {
		case <-d.stopIdleReaper:
			return
		case <-ticker.C:
			d.reapIdleSessions()
		}
	}
}

// reapIdleSessions closes sessions that have been idle for too long.
// Round 11 fix: Snapshot all sessions under d.mu, release lock, then check/close.
func (d *Driver) reapIdleSessions() {
	now := time.Now()

	// Snapshot all sessions under lock
	d.mu.Lock()
	var allSessions []*session
	for _, ac := range d.bots {
		ac.mu.Lock()
		for _, s := range ac.sessions {
			allSessions = append(allSessions, s)
		}
		ac.mu.Unlock()
	}
	d.mu.Unlock()

	// Check and close idle sessions WITHOUT holding any locks
	for _, s := range allSessions {
		s.mu.Lock()
		idle := !s.running && now.Sub(s.lastUsed) > limits.ACPSessionIdleTimeout
		botID := s.botID
		sessionID := s.id
		lastUsed := s.lastUsed
		s.mu.Unlock()

		if idle {
			d.log.Info("Closing idle session", "bot", botID, "session", sessionID, "idle_duration", now.Sub(lastUsed).Round(time.Second))
			_ = d.CloseSession(s)
		}
	}
}

// agentGen wraps one agent process for one session (D1 Round 11: 1:1 relationship).
type agentGen struct {
	c *conn
}

// agentConn tracks per-bot sessions for cap enforcement.
type agentConn struct {
	botID string
	mode  string

	mu          sync.Mutex
	sessions    map[string]*session
	threads     map[string]*session
	sem         chan struct{}       // D1: Per-box cap semaphore
	loadSession bool                // Agent capability
	children    map[string]*session // agent 開的子代理 session → 開它的 session（權限請求、背景工作對回去）
	requests    map[string]pending  // 等人回覆的權限請求
}

type permOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

type pending struct {
	id      json.RawMessage
	session string
	options []permOption
}

// session 實作 engine.Session。
// D1 (Round 11): Each session owns its own agent process (1:1 with gen).
type session struct {
	botID    string
	gen      *agentGen // D1: This session's own agent process
	id       string
	threadID string

	// D1: Release semaphore exactly once
	releaseSem sync.Once
	sem        chan struct{} // Reference to agentConn.sem

	// E1: Close session exactly once
	closeOnce sync.Once

	mu         sync.Mutex
	running    bool
	cancelling bool
	closed     bool
	mode       string
	effortOpt  string
	efforts    []string
	effort     string
	tools      map[string]*toolState
	bg         map[string]string
	shown      map[string]bool
	dropped    map[string]bool
	holding    bool
	held       json.RawMessage
	agentTurn  bool
	wake       bool
	denied     bool
	resumes    int
	bgNote     string
	settle     *time.Timer
	giveUp     *time.Timer
	q          *queue
	resetAt    int64
	intro      string
	all, tail  strings.Builder
	lastUsed   time.Time
}

func (s *session) ID() string { return s.id }

// ResumeID 實作 engine.Session：ACP 的 sessionId 可以用 session/load 接回。
func (s *session) ResumeID() string { return s.id }

// botConn looks up the agentConn for a bot.
func (d *Driver) botConn(botID string) *agentConn {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.bots[botID]
}

func (d *Driver) authorize(ctx context.Context, botID string) error {
	if d.o.Authorize == nil {
		return nil
	}
	return d.o.Authorize(ctx, botID, d.o.Agent.Engine)
}

// client 回傳 Bot 目前的 agent 連線追蹤結構；沒有就建立（同一個 Bot 同時只開一次）。
// D1: agentConn tracks sessions and enforces cap; each session spawns its own agent.
func (d *Driver) client(ctx context.Context, botID, mode string) (*agentConn, error) {
	d.mu.Lock()
	l, ok := d.dials[botID]
	if !ok {
		l = &sync.Mutex{}
		d.dials[botID] = l
	}
	ac := d.bots[botID]
	d.mu.Unlock()

	if ac != nil {
		return ac, nil
	}

	l.Lock()
	defer l.Unlock()

	// Check again under lock
	d.mu.Lock()
	ac = d.bots[botID]
	if ac != nil {
		d.mu.Unlock()
		return ac, nil
	}

	// D1: Initialize semaphore channel (starts FULL of tokens, one per slot)
	sem := make(chan struct{}, limits.ACPMaxSessionsPerBox)
	for i := 0; i < limits.ACPMaxSessionsPerBox; i++ {
		sem <- struct{}{} // Fill with available slots
	}

	ac = &agentConn{
		botID:    botID,
		mode:     mode,
		sessions: map[string]*session{},
		threads:  map[string]*session{},
		children: map[string]*session{},
		requests: map[string]pending{},
		sem:      sem,
	}

	d.bots[botID] = ac
	d.mu.Unlock()
	return ac, nil
}

type modeInfo struct {
	ID string `json:"id"`
}

// sessionResult 是 session/new 回覆中用得到的部分。
type sessionResult struct {
	SessionID string `json:"sessionId"`
	Modes     *struct {
		CurrentModeID  string     `json:"currentModeId"`
		AvailableModes []modeInfo `json:"availableModes"`
	} `json:"modes"`
	ConfigOptions []struct {
		ID           string `json:"id"`
		Category     string `json:"category"`
		CurrentValue any    `json:"currentValue"`
		// Options 可能是平的 [{value,name}]，也可能分組 [{group,name,options:[...]}]（ACP SessionConfigSelectOptions）。
		Options []struct {
			Value   string        `json:"value"`
			Name    string        `json:"name"`
			Options []ModelChoice `json:"options"`
		} `json:"options"`
	} `json:"configOptions"`
	// Models 是較舊的 agent 用的欄位（ACP unstable session models）。
	Models *struct {
		CurrentModelID  string `json:"currentModelId"`
		AvailableModels []struct {
			ModelID string `json:"modelId"`
			Name    string `json:"name"`
		} `json:"availableModels"`
	} `json:"models"`
}

// models 整理 agent 列出的模型與目前用的；沒有列時 list 是空的。
func (r sessionResult) models() (current string, list []ModelChoice) {
	for _, opt := range r.ConfigOptions {
		if opt.ID != "model" && opt.Category != "model" {
			continue
		}
		current, _ = opt.CurrentValue.(string)
		for _, v := range opt.Options {
			if v.Value != "" {
				list = append(list, ModelChoice{Value: v.Value, Name: v.Name})
			}
			list = append(list, v.Options...)
		}
		return current, list
	}
	if r.Models != nil {
		for _, m := range r.Models.AvailableModels {
			list = append(list, ModelChoice{Value: m.ModelID, Name: m.Name})
		}
		return r.Models.CurrentModelID, list
	}
	return "", nil
}

// Start 實作 engine.Driver：同一個對話串只開一個 session；有存下的 sessionId 且 agent 支援時先用 session/load 接回，
// 接不回才開新的（session/new）。之後照 Bot 的權限等級與模型設定 session。
func (d *Driver) Start(ctx context.Context, o engine.StartOptions) (engine.Session, error) {
	if err := d.authorize(ctx, o.BotID); err != nil {
		return nil, err
	}
	ac, err := d.client(ctx, o.BotID, o.Mode)
	if err != nil {
		return nil, err
	}

	ac.mu.Lock()
	// Check if thread already has a session
	if s := ac.threads[o.ThreadID]; s != nil {
		ac.mu.Unlock()
		s.mu.Lock()
		s.mode = o.Mode
		s.lastUsed = time.Now()
		s.mu.Unlock()
		cctx, cancel := context.WithTimeout(ctx, limits.ACPCallTimeout)
		d.applyEffort(cctx, s, o.BotID, o.Effort)
		cancel()
		return s, nil
	}
	// Get sem reference while holding lock, then release
	sem := ac.sem
	ac.mu.Unlock()

	// Round 10 fix + C4: Use semaphore channel for cap enforcement
	// D1: Acquire semaphore token (1 per session) - NO LOCK HELD
	select {
	case <-sem:
		// Got a token (slot freed or initially available)
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// D1: Always spawn a new agent process for this session (outside mutex)
	p, err := d.launch(ctx, o.BotID, o.Mode) // Use session's mode, not ac.mode
	if err != nil {
		sem <- struct{}{} // Release token on spawn failure
		return nil, err
	}

	log := d.log.With("bot", o.BotID)

	// Initialize the agent
	var c *conn
	c = newConn(p, log,
		func(m string, params json.RawMessage) { d.notification(ac, m, params) },
		func(r request) { d.request(ac, r) },
		func(cause error) {
			// D1: Disconnect callback - find session for this conn and mark it dead
			ac.mu.Lock()
			var sid string
			for id, s := range ac.sessions {
				if s.gen != nil && s.gen.c == c {
					sid = id
					break
				}
			}
			ac.mu.Unlock()
			if sid != "" {
				d.disconnected(ac, sid, p, cause)
			}
		})

	ictx, cancel := context.WithTimeout(ctx, limits.ACPInitTimeout)
	defer cancel()
	raw, err := c.call(ictx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientCapabilities": map[string]any{
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]string{"name": "xbot", "title": "XBot", "version": version.Version},
	})
	if err != nil {
		c.close()
		sem <- struct{}{} // Release token on init failure
		if t, ok := p.(StderrTail); ok && authFailRe.MatchString(t.StderrTail()) {
			return nil, fmt.Errorf("%s 沒有啟動：%s 還沒登入或登入已過期", d.o.Agent.Label, d.o.Agent.Label)
		}
		return nil, fmt.Errorf("%s 沒有啟動：%w", d.o.Agent.Label, err)
	}

	var init struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(raw, &init); err != nil || init.ProtocolVersion != ProtocolVersion {
		c.close()
		sem <- struct{}{} // Release token
		return nil, fmt.Errorf("%s protocol version mismatch", d.o.Agent.Label)
	}

	// D1: Store loadSession capability (no lock needed, benign race)
	ac.loadSession = init.AgentCapabilities.LoadSession

	g := &agentGen{c: c}
	d.log.Info("Spawned agent for new session", "bot", o.BotID, "thread", o.ThreadID, "live_sessions", len(ac.sessions)+1, "cap", limits.ACPMaxSessionsPerBox)

	var mcp []MCPServer
	if d.o.MCPServers != nil {
		mcp = d.o.MCPServers(o.BotID)
	}
	if mcp == nil {
		mcp = []MCPServer{}
	}
	inst := d.instructions(ctx, o.BotID)
	if inst != "" && d.o.Agent.Hint != "" {
		inst += "\n\n" + d.o.Agent.Hint
	}
	withPrompt := func(params map[string]any) map[string]any {
		meta := map[string]any{}
		for k, v := range d.o.Agent.ModeMeta[o.Mode] {
			meta[k] = v
		}
		switch {
		case inst == "":
		case d.o.Agent.SystemPrompt:
			meta["systemPrompt"] = map[string]any{"append": SystemPromptText(inst)}
		case d.o.Agent.Rules:
			meta["rules"] = SystemPromptText(inst)
		}
		if len(meta) > 0 {
			params["_meta"] = meta
		}
		return params
	}
	sctx, cancel := context.WithTimeout(ctx, limits.ACPSessionTimeout)
	defer cancel()
	var res sessionResult
	id := ""
	loaded := false
	if o.Resume != "" && ac.loadSession {
		// session/load 會把舊的對話以 session/update 重播；這時 session 還沒登記，重播的內容不會變成事件。
		raw, err := g.c.call(sctx, "session/load", withPrompt(map[string]any{"sessionId": o.Resume, "cwd": Workspace, "mcpServers": mcp}))
		switch {
		case err == nil:
			id, loaded = o.Resume, true
			_ = json.Unmarshal(raw, &res)
		case errors.Is(err, engine.ErrDisconnected):
			sem <- struct{}{} // Release semaphore
			return nil, err
		default:
			d.log.Info("接不回之前的 session，開新的", "bot", o.BotID, "session", o.Resume, "err", err)
		}
	}
	if id == "" {
		raw, err := g.c.call(sctx, "session/new", withPrompt(map[string]any{"cwd": Workspace, "title": o.Title, "mcpServers": mcp}))
		if err != nil {
			sem <- struct{}{} // Release semaphore on error
			return nil, d.explain("無法開始對話", err)
		}
		if err := json.Unmarshal(raw, &res); err != nil || res.SessionID == "" {
			sem <- struct{}{} // Release semaphore
			return nil, fmt.Errorf("開始對話的回覆不完整：%s", truncate(string(raw), 200))
		}
		id = res.SessionID
	}
	s := &session{
		botID: o.BotID, gen: g, id: id, threadID: o.ThreadID, mode: o.Mode,
		sem:   sem, // D1: Reference to semaphore for release on close
		tools: map[string]*toolState{}, bg: map[string]string{},
		shown: map[string]bool{}, dropped: map[string]bool{}, q: newQueue(), lastUsed: time.Now(),
	}
	if inst != "" && !d.o.Agent.SystemPrompt && !d.o.Agent.Rules && !loaded {
		s.intro = IntroText(inst)
	}

	// 2026-10-22n: Removed MCP PID tracking - see CloseSession for real fix.

	ac.mu.Lock()
	if old := ac.threads[o.ThreadID]; old != nil {
		ac.mu.Unlock()
		s.q.close()
		return old, nil
	}
	ac.sessions[id] = s
	ac.threads[o.ThreadID] = s
	ac.mu.Unlock()
	d.configure(ctx, s, res, o)
	// D1: Eviction is handled by semaphore blocking Start() when cap is reached
	return s, nil
}

// instructions 拿 Bot 的身分與守則；拿不到時記紀錄、照樣開 session（只是少了守則）。
func (d *Driver) instructions(ctx context.Context, botID string) string {
	if d.o.Instructions == nil {
		return ""
	}
	text, err := d.o.Instructions(ctx, botID)
	if err != nil {
		d.log.Warn("讀不到 Bot 的身分與守則", "bot", botID, "err", err)
		return ""
	}
	return strings.TrimSpace(text)
}

// launch creates a new agent process.
// Round 10: Helper for spawnNewGen.
func (d *Driver) launch(ctx context.Context, botID, mode string) (Process, error) {
	agent := d.o.Agent.ForMode(mode)
	return d.o.Launch(ctx, botID, agent)
}

// identityNote：agent 自己的系統提示說它是 Claude Code、Grok…；在 XBot 裡它就是這個 Bot。
const identityNote = "你在 XBot 裡就是下面這位 Bot：用它的名字、個性和語言（繁體中文）回應；被問到用的是哪個引擎或模型時可以照實說。" +
	"XBot 的工具在 MCP 伺服器 xbot 裡，名稱寫成 xbot_<領域>_<動作>；守則裡寫成 xbot.<領域>.<動作> 的就是同一個工具。"

// SystemPromptText 是放進 agent 系統提示的身分與守則（Agent.SystemPrompt，例：Claude Code）。
func SystemPromptText(inst string) string { return "# XBot\n" + identityNote + "\n\n" + inst }

// IntroText 是 agent 沒有系統提示可以放時，加在新 session 第一則訊息前面的身分與守則。
func IntroText(inst string) string {
	return "[XBot 系統設定] 以下是你的身分與守則，這整段對話都適用；這一段不用回覆，直接處理後面的訊息。" + identityNote +
		"\n<xbot-instructions>\n" + inst + "\n</xbot-instructions>"
}

// configure 照 Bot 的權限等級設定 session 模式、照設定的模型選模型。agent 沒有對應的選項就不設定；設定失敗只記紀錄。
func (d *Driver) configure(ctx context.Context, s *session, res sessionResult, o engine.StartOptions) {
	cctx, cancel := context.WithTimeout(ctx, limits.ACPCallTimeout)
	defer cancel()
	if mode := d.o.Agent.Modes[o.Mode]; mode != "" && res.Modes != nil && res.Modes.CurrentModeID != mode &&
		slices.ContainsFunc(res.Modes.AvailableModes, func(m modeInfo) bool { return m.ID == mode }) {
		if _, err := s.gen.c.call(cctx, "session/set_mode", map[string]any{"sessionId": s.id, "modeId": mode}); err != nil {
			d.log.Warn("設定 session 模式失敗", "bot", o.BotID, "mode", mode, "err", err)
		}
	}
	d.setEffort(cctx, s, res, o)
	current, models := res.models()
	if d.o.OnModels != nil && len(models) > 0 {
		d.o.OnModels(o.BotID, current, models)
	}
	if o.Model == "" || current == o.Model {
		return
	}
	for _, opt := range res.ConfigOptions {
		if opt.ID != "model" {
			continue
		}
		for _, v := range models {
			if v.Value == o.Model {
				if _, err := s.gen.c.call(cctx, "session/set_config_option", map[string]any{"sessionId": s.id, "configId": "model", "value": o.Model}); err != nil {
					d.log.Warn("設定模型失敗", "bot", o.BotID, "model", o.Model, "err", err)
				}
				return
			}
		}
		// 2026-10-22q (Item 1): StrictModel check moved to Start() for early failure.
		// This path is only reached when StrictModel=false.
		d.log.Info("agent 沒有這個模型，用它的預設", "bot", o.BotID, "model", o.Model)
	}
}

// effortLevels 是思考強度由低到高（各 agent 的選項名稱照這個順序比）。
var effortLevels = []string{"minimal", "low", "medium", "high", "xhigh"}

// setEffort 照 Bot 的思考強度（bot.effort）設 agent 的思考強度選項（ACP configOptions 的 thought_level，例：Grok 的
// reasoning_effort）。2026-10-21 實測：島灰設「低」，Grok 卻一直用它預設的 high，簡單的問題先想 10～19 秒才查。
// agent 沒有這個選項、或沒有一樣的值時，挑最接近的。
func (d *Driver) setEffort(ctx context.Context, s *session, res sessionResult, o engine.StartOptions) {
	for _, opt := range res.ConfigOptions {
		if opt.ID != "reasoning_effort" && opt.Category != "thought_level" {
			continue
		}
		s.mu.Lock()
		s.effortOpt, s.efforts = opt.ID, nil
		for _, v := range opt.Options {
			s.efforts = append(s.efforts, v.Value)
		}
		s.effort, _ = opt.CurrentValue.(string)
		s.mu.Unlock()
		d.applyEffort(ctx, s, o.BotID, o.Effort)
		return
	}
}

// applyEffort 把 session 的思考強度換成最接近 effort 的值；agent 沒有這個選項、或已經是這個值時不動。
func (d *Driver) applyEffort(ctx context.Context, s *session, botID, effort string) {
	want := slices.Index(effortLevels, effort)
	s.mu.Lock()
	opt, values, cur := s.effortOpt, s.efforts, s.effort
	s.mu.Unlock()
	if want < 0 || opt == "" {
		return
	}
	best, gap := "", len(effortLevels)
	for _, v := range values {
		if i := slices.Index(effortLevels, v); i >= 0 && abs(i-want) < gap {
			best, gap = v, abs(i-want)
		}
	}
	if best == "" || best == cur {
		return
	}
	if _, err := s.gen.c.call(ctx, "session/set_config_option", map[string]any{"sessionId": s.id, "configId": opt, "value": best}); err != nil {
		d.log.Warn("設定思考強度失敗", "bot", botID, "effort", best, "err", err)
		return
	}
	s.mu.Lock()
	s.effort = best
	s.mu.Unlock()
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// quota 看這一輪的錯誤是不是額度用完、限流、餘額不足或登入過期（EN-7）：agent 的訊息加上結構化的 data
// （claude-agent-acp 的 errorKind、codex-acp 的 codexErrorInfo）一起分類；沒寫恢復時間時用 agent 之前報的用量狀態。
func (d *Driver) quota(s *session, err error) *engine.QuotaError {
	text := err.Error()
	var re *RPCError
	if errors.As(err, &re) {
		if re.Code == codeAuthRequired && strings.Contains(strings.ToLower(re.Message), "auth") {
			return &engine.QuotaError{Engine: d.o.Agent.Engine, Kind: engine.QuotaAuth, Message: re.Message}
		}
		if len(re.Data) > 0 && string(re.Data) != "null" {
			text += " " + truncate(string(re.Data), 2000)
		}
	}
	q := engine.ClassifyQuota(d.o.Agent.Engine, text, time.Now())
	if q == nil {
		return nil
	}
	s.mu.Lock()
	reset := s.resetAt
	s.mu.Unlock()
	if q.ResetAt == 0 && q.Kind != engine.QuotaAuth && reset > time.Now().UnixMilli() {
		q.ResetAt = reset
	}
	return q
}

// explain 把 agent 的錯誤換成給人看的說明（尤其是還沒登入）。
func (d *Driver) explain(what string, err error) error {
	var re *RPCError
	if errors.As(err, &re) && (re.Code == codeAuthRequired || strings.Contains(strings.ToLower(re.Message), "auth")) {
		return fmt.Errorf("%s：%s 還沒登入，請到 Bot 設定 ›「AI 帳號與模型」登入", what, d.o.Agent.Label)
	}
	return fmt.Errorf("%s：%w", what, err)
}

func (d *Driver) own(s engine.Session) (*session, error) {
	as, ok := s.(*session)
	if !ok {
		return nil, errors.New("不是 ACP 的 session")
	}
	if !as.gen.c.alive() {
		return nil, engine.ErrDisconnected
	}
	return as, nil
}

// authFailRe 認出 agent 行程因為沒有登入而結束時寫在 stderr 的訊息。
var authFailRe = regexp.MustCompile(`(?i)failed to authenticate|authentication (failed|required)|please (log ?in|sign in)|not (logged|signed) in`)

// ErrBusy 表示這個 session 上一輪還沒結束。
var ErrBusy = errors.New("上一輪還沒結束")

// ErrBlocked 表示訊息是被擋下的斜線指令（Agent.BlockedPrompts）。
var ErrBlocked = errors.New("不能送出這個指令")

// Send 實作 engine.Driver（session/prompt）。prompt 的回覆就是這一輪的結束（stopReason），在背景等。
func (d *Driver) Send(ctx context.Context, s engine.Session, m engine.Message) error {
	as, err := d.own(s)
	if err != nil {
		return err
	}
	if err := d.authorize(ctx, as.botID); err != nil {
		return err
	}
	if cmd, blocked := d.o.Agent.Blocked(m.Text); blocked {
		return fmt.Errorf("%w：%s 的 %s 會關掉所有核准，XBot 不送這個指令（SEC-2）", ErrBlocked, d.o.Agent.Label, cmd)
	}
	as.mu.Lock()
	if as.running {
		as.mu.Unlock()
		return ErrBusy
	}
	as.running, as.cancelling, as.denied, as.resumes = true, false, false, 0
	as.lastUsed = time.Now() // 2026-10-22：記錄使用時間
	intro := as.intro
	as.intro = ""
	as.mu.Unlock()
	prompt := []map[string]string{{"type": "text", "text": m.Text}}
	if intro != "" {
		prompt = append([]map[string]string{{"type": "text", "text": intro}}, prompt...)
	}
	params := map[string]any{"sessionId": as.id, "prompt": prompt}
	_, err = as.gen.c.start("session/prompt", params, func(raw json.RawMessage, err error) { d.promptDone(as, raw, err) })
	if err != nil {
		as.mu.Lock()
		as.running = false
		as.intro = intro // 沒送出去：下一則再帶
		as.mu.Unlock()
		return err
	}
	return nil
}

// promptDone：session/prompt 回來了。使用者拒絕了某個動作、agent 因此整輪取消時，請它換方法接著做（resumeAfterDeny）；
// agent 還有背景工作時先留著（hold），等它做完、回報完才收尾（endTurn）。
func (d *Driver) promptDone(s *session, raw json.RawMessage, err error) {
	if err == nil && (d.resumeAfterDeny(s, raw) || d.hold(s, raw)) {
		return
	}
	d.endTurn(s, raw, err)
}

// DeniedNote 是使用者拒絕某個動作後，請 agent 換方法接著做的訊息（對話裡看不到）。
const DeniedNote = "（系統：使用者拒絕了剛才那個動作，其他的事沒有要你停。不要再用同樣的方法或指令，改用別的做得到的方式把原本的事做完；" +
	"真的沒有別的方法，就照你目前知道的回答，並說明缺了什麼。）"

// resumeAfterDeny：這一輪因為使用者拒絕而被 agent 取消（stopReason=cancelled，不是使用者按停止）時，在同一輪送 DeniedNote
// 請它接著做；回答有沒有送。
func (d *Driver) resumeAfterDeny(s *session, raw json.RawMessage) bool {
	var res struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(raw, &res)
	s.mu.Lock()
	if res.StopReason != "cancelled" || s.cancelling || !s.denied || s.resumes >= limits.ACPDenyResumeMax {
		s.mu.Unlock()
		return false
	}
	s.denied = false
	s.resumes++
	s.tail.Reset()
	s.mu.Unlock()
	params := map[string]any{"sessionId": s.id, "prompt": []map[string]string{{"type": "text", "text": DeniedNote}}}
	if _, err := s.gen.c.start("session/prompt", params, func(raw json.RawMessage, err error) { d.promptDone(s, raw, err) }); err != nil {
		d.log.Warn("拒絕後請 agent 接著做失敗", "bot", s.botID, "err", err)
		return false
	}
	return true
}

// hold 在 agent 還有背景工作、或有背景工作剛做完還沒回報時，把這一輪留著；回答有沒有留。
func (d *Driver) hold(s *session, raw json.RawMessage) bool {
	var res struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(raw, &res)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelling || (res.StopReason != "" && res.StopReason != "end_turn") || (len(s.bg) == 0 && !s.agentTurn && !s.wake) {
		return false
	}
	s.holding, s.held = true, raw
	s.giveUp = time.AfterFunc(limits.ACPBackgroundWait, func() {
		s.mu.Lock()
		s.abandon()
		s.mu.Unlock()
		d.release(s)
	})
	// 這時還沒做完的子代理就是背景子代理：在工作步驟裡顯示成進行中（看門狗也照有工具在跑的時限）。
	for _, id := range slices.Sorted(maps.Keys(s.bg)) {
		s.show(id)
	}
	d.rearm(s)
	return true
}

// show（呼叫端持有鎖）把一個背景工作顯示成進行中的步驟。
func (s *session) show(id string) {
	if s.shown[id] {
		return
	}
	s.shown[id] = true
	s.q.push(engine.Event{Type: engine.EventToolCall, Session: s.id, Tool: &engine.ToolEvent{ID: id, Name: s.bg[id]}})
}

// finishBg（呼叫端持有鎖）結束一個背景工作；顯示過的步驟照結果收掉。
func (s *session) finishBg(id string, ok bool) {
	name := s.bg[id]
	delete(s.bg, id)
	if s.shown[id] {
		delete(s.shown, id)
		s.q.push(engine.Event{Type: engine.EventToolResult, Session: s.id, Tool: &engine.ToolEvent{ID: id, Name: name, OK: ok}})
	}
}

// abandon（呼叫端持有鎖）放下所有背景工作（按了停止、或等太久）：步驟以「沒做完」收掉，之後它們的進度不再讓一輪留著
// （不然下一則不相干的訊息也會被留著等它）。
func (s *session) abandon() {
	for id := range s.bg {
		s.dropped[id] = true
		s.finishBg(id, false)
	}
	s.wake = false
}

// rearm（呼叫端持有鎖）照背景工作的狀態決定留著的一輪什麼時候收尾：背景工作還在跑、或 agent 正在回報就繼續等；
// 背景工作剛做完、agent 還沒開始回報，等 limits.ACPBackgroundSettle 看它會不會開始；都做完也回報完了就收尾。
func (d *Driver) rearm(s *session) {
	if !s.holding {
		return
	}
	if s.settle != nil {
		s.settle.Stop()
		s.settle = nil
	}
	switch {
	case len(s.bg) > 0 || s.agentTurn:
	case s.wake:
		s.settle = time.AfterFunc(limits.ACPBackgroundSettle, func() { d.release(s) })
	default:
		go d.release(s)
	}
}

// release 收尾留著的一輪（背景工作做完、回報完，或等太久）。
func (d *Driver) release(s *session) {
	s.mu.Lock()
	if !s.holding {
		s.mu.Unlock()
		return
	}
	raw := s.held
	s.unhold()
	s.mu.Unlock()
	d.endTurn(s, raw, nil)
}

// unhold 清掉等背景工作的狀態（呼叫端持有鎖）。
func (s *session) unhold() {
	for _, t := range []*time.Timer{s.settle, s.giveUp} {
		if t != nil {
			t.Stop()
		}
	}
	s.holding, s.held, s.settle, s.giveUp, s.agentTurn, s.wake = false, nil, nil, nil, false, false
}

// endTurn 結束這一輪：送出用量與 done。
func (d *Driver) endTurn(s *session, raw json.RawMessage, err error) {
	s.mu.Lock()
	s.running = false
	cancelling := s.cancelling
	s.cancelling = false
	s.tools = map[string]*toolState{}
	all, tail := s.all.String(), strings.TrimSpace(s.tail.String())
	s.all.Reset()
	s.tail.Reset()
	s.mu.Unlock()
	if errors.Is(err, engine.ErrDisconnected) {
		return // disconnected 會送出連線中斷
	}
	if err != nil {
		if cancelling {
			s.q.push(engine.Event{Type: engine.EventDone, Session: s.id, Status: engine.StatusInterrupted})
			return
		}
		msg := d.explain("這一輪失敗", err).Error()
		q := d.quota(s, err)
		s.q.push(engine.Event{Type: engine.EventError, Session: s.id, Text: msg, Quota: q})
		s.q.push(engine.Event{Type: engine.EventDone, Session: s.id, Status: engine.StatusError, Text: msg, Quota: q})
		return
	}
	var res struct {
		StopReason string       `json:"stopReason"`
		Usage      *promptUsage `json:"usage"`
	}
	_ = json.Unmarshal(raw, &res)
	if res.Usage != nil {
		s.q.push(engine.Event{Type: engine.EventUsage, Session: s.id, Usage: &engine.Usage{
			InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens, Reasoning: res.Usage.ThoughtTokens,
			CacheRead: res.Usage.CachedReadTokens, CacheWrite: res.Usage.CachedWriteTokens,
		}})
	}
	status, note := stopStatus(res.StopReason)
	if cancelling && status == engine.StatusComplete && res.StopReason == "" {
		status = engine.StatusInterrupted
	}
	if note != "" && status != engine.StatusError {
		s.q.push(engine.Event{Type: engine.EventStatus, Session: s.id, Text: note})
		note = ""
	}
	if note == "" && status == engine.StatusComplete && tail != "" && tail != strings.TrimSpace(all) &&
		utf8.RuneCountInString(strings.TrimSpace(all))-utf8.RuneCountInString(tail) <= limits.InterimNarrationMax {
		note = tail // 最後的回答是用完工具之後寫的那段；前面只是一兩句「我先看一下」
	}
	s.q.push(engine.Event{Type: engine.EventDone, Session: s.id, Status: status, Text: note})
}

// Interrupt 實作 engine.Driver（session/cancel）：還在等人回覆的權限請求一律以 cancelled 回覆（ACP 規定），
// agent 收尾後 session/prompt 會以 stopReason=cancelled 回來。沒有進行中的一輪時直接送出 interrupted。
func (d *Driver) Interrupt(_ context.Context, s engine.Session) error {
	as, err := d.own(s)
	if err != nil {
		return err
	}
	as.mu.Lock()
	running, holding := as.running, as.holding
	if running {
		as.cancelling = true
	}
	if holding {
		as.unhold()
	}
	as.abandon()
	as.mu.Unlock()
	if !running {
		as.q.push(engine.Event{Type: engine.EventDone, Session: as.id, Status: engine.StatusInterrupted})
		return nil
	}
	if holding {
		// prompt 已經回來了，不會再有回覆：這裡直接收尾（agent 若正在回報，下面的 session/cancel 會停下它）。
		defer d.endTurn(as, nil, nil)
	}
	ac := d.botConn(as.botID)
	if ac != nil {
		ac.mu.Lock()
		var open []pending
		for k, p := range ac.requests {
			if p.session == as.id {
				open = append(open, p)
				delete(ac.requests, k)
			}
		}
		ac.mu.Unlock()
		for _, p := range open {
			_ = as.gen.c.answer(p.id, cancelledOutcome)
		}
	}
	return as.gen.c.notify("session/cancel", map[string]string{"sessionId": as.id})
}

var cancelledOutcome = map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}

// Respond 實作 engine.Driver：once → allow_once、always → allow_always（沒有就 allow_once）、
// 其他 → reject_once（沒有就 reject_always）；都沒有對應的選項時回 cancelled。
func (d *Driver) Respond(_ context.Context, s engine.Session, requestID string, a engine.Answer) error {
	as, err := d.own(s)
	if err != nil {
		return err
	}
	ac := d.botConn(as.botID)
	if ac == nil {
		return errors.New("bot connection not found")
	}
	ac.mu.Lock()
	p, ok := ac.requests[requestID]
	if ok && p.session == as.id {
		delete(ac.requests, requestID)
	}
	ac.mu.Unlock()
	if !ok || p.session != as.id {
		return errors.New("這個問題已結束，請重新整理對話")
	}
	var want []string
	switch a.Choice {
	case "once":
		want = []string{"allow_once", "allow_always"}
	case "always":
		want = []string{"allow_always", "allow_once"}
	default:
		want = []string{"reject_once", "reject_always"}
		as.mu.Lock()
		as.denied = true
		as.mu.Unlock()
	}
	for _, k := range want {
		for _, o := range p.options {
			if o.Kind == k {
				return as.gen.c.answer(p.id, map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": o.OptionID}})
			}
		}
	}
	return as.gen.c.answer(p.id, cancelledOutcome)
}

// Events 實作 engine.Driver。
func (d *Driver) Events(s engine.Session) <-chan engine.Event {
	as, ok := s.(*session)
	if !ok {
		ch := make(chan engine.Event)
		close(ch)
		return ch
	}
	return as.q.out
}

// Close 結束一個 Bot 的 agent 行程（電腦停止、刪除、收回憑證時）；它的 session 都會收到連線中斷。
// D1: Close all sessions' agent processes.
func (d *Driver) Close(botID string) {
	d.mu.Lock()
	ac := d.bots[botID]
	delete(d.bots, botID)
	d.mu.Unlock()

	if ac != nil {
		ac.mu.Lock()
		sessions := make([]*session, 0, len(ac.sessions))
		for _, s := range ac.sessions {
			sessions = append(sessions, s)
		}
		ac.mu.Unlock()

		// Close all sessions (each kills its own agent process)
		for _, s := range sessions {
			_ = d.CloseSession(s)
		}
	}
}

// CloseSession 關閉一個 session（2026-10-22p：立即重啟 agent 以清理 MCP）。
// 2026-10-22p (LEAK FIX): 不等所有 sessions 關閉才重啟。一有 session 關閉就標記需要重啟。
// 當所有剩餘 sessions 都閒置時，立即重啟 agent 以清理被關閉 sessions 的 MCP 服務器。
// 這確保在穩定使用中（sessions 重疊，計數永不為 0）MCP 也能被及時清理。
func (d *Driver) CloseSession(s engine.Session) error {
	as, ok := s.(*session)
	if !ok {
		return errors.New("不是 ACP 的 session")
	}

	// E1+E2: Idempotent cleanup using closeOnce
	as.closeOnce.Do(func() {
		as.mu.Lock()
		if as.closed {
			as.mu.Unlock()
			return
		}
		as.closed = true
		running := as.running
		if running {
			as.cancelling = true
		}
		if as.holding {
			as.unhold()
		}
		as.abandon()
		g := as.gen
		as.mu.Unlock()

		// Release semaphore exactly once
		as.releaseSem.Do(func() {
			as.sem <- struct{}{}
		})

		// 如果正在跑，發 session/cancel 停下來
		if running && g != nil && g.c != nil {
			_ = g.c.notify("session/cancel", map[string]string{"sessionId": as.id})
		}

		// Kill this session's agent process group
		// Note: We do NOT close queue here - let disconnect callback do that after emitting error
		if g != nil && g.c != nil {
			go d.killProcessGroup(g.c, as.botID, as.id)
		}
	})

	return nil
}

// killProcessGroup terminates the agent process.
// D1 (Round 11): Each session owns its process; kill it on close.
func (d *Driver) killProcessGroup(c *conn, botID, sessionID string) {
	if c == nil {
		return
	}

	d.log.Info("Terminating agent process", "bot", botID, "session", sessionID)
	c.close()
}

// 2026-10-22n: Removed diffPids, snapshotMCPPids, killMCPPidsInContainer.
// These don't work because the agent respawns MCP servers when they're killed.
// D1 (Round 11): Real fix: 1 process per session, kill on CloseSession.
// D1: Removed evictOldSessions - cap is enforced by semaphore blocking Start().

// D1: disconnected marks only this session as dead (never bot-wide state).
func (d *Driver) disconnected(ac *agentConn, sessionID string, p Process, cause error) {
	attrs := []any{"bot", ac.botID, "session", sessionID, "cause", cause}
	if t, ok := p.(StderrTail); ok {
		if tail := t.StderrTail(); tail != "" {
			attrs = append(attrs, "stderr", tail)
		}
	}
	d.log.Info("agent process disconnected", attrs...)

	// E1+E2: Remove from map, emit error, and close queue
	ac.mu.Lock()
	s := ac.sessions[sessionID]
	if s != nil {
		delete(ac.sessions, s.id)
		delete(ac.threads, s.threadID)
		// 清掉這個 session 等待中的權限請求
		for k, p := range ac.requests {
			if p.session == s.id {
				delete(ac.requests, k)
			}
		}
		liveCount := len(ac.sessions)
		ac.mu.Unlock()
		d.log.Info("Removed disconnected session from map", "bot", ac.botID, "session", sessionID, "live_sessions", liveCount)
	} else {
		ac.mu.Unlock()
	}

	if s != nil {
		// Emit error event and close queue
		s.q.push(engine.Event{Type: engine.EventError, Session: s.id, Text: engine.ErrDisconnected.Error(), Fatal: true})
		s.q.close()
		// E1: Also call CloseSession to ensure semaphore is released (idempotent via closeOnce)
		_ = d.CloseSession(s)
	}
}

func (d *Driver) notification(ac *agentConn, method string, params json.RawMessage) {
	if strings.HasPrefix(method, "_x.ai/") || strings.HasPrefix(method, "x.ai/") {
		// Grok 的擴充通知：背景工作的進度在 _x.ai/session_notification（它自己的紀錄檔寫成 _x.ai/session/update）。
		d.background(ac, params)
		return
	}
	if method != "session/update" {
		return
	}
	var p updateParams
	if json.Unmarshal(params, &p) != nil || p.Meta.IsReplay {
		return
	}
	ac.mu.Lock()
	s := ac.sessions[p.SessionID]
	ac.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	if rl := p.Update.Meta.RateLimit; rl != nil && rl.ResetsAt > 0 {
		s.resetAt = rl.ResetsAt * 1000
	}
	if !s.running {
		// 沒有進行中的一輪（例：背景工作做完後 agent 自己開的一輪，而這一輪已經收尾）：不能混進下一輪的回覆。
		s.mu.Unlock()
		return
	}
	if s.holding && turnActivity[p.Update.SessionUpdate] {
		// prompt 回來之後 agent 又動了：它在回報背景工作，等這一輪（turn_completed）結束。
		s.agentTurn, s.wake = true, false
		d.rearm(s)
	}
	evs := normalize(p.Update, s.tools, time.Now())
	for _, e := range evs {
		switch e.Type {
		case engine.EventText:
			s.all.WriteString(e.Text)
			s.tail.WriteString(e.Text)
		case engine.EventToolCall:
			s.tail.Reset()
		}
	}
	s.mu.Unlock()
	for _, e := range evs {
		e.Session = s.id
		s.q.push(e)
	}
}

// turnActivity 是 agent 在跑一輪時才會送的 session/update（開始回報背景工作的證據）。標題、指令清單、模式、用量這類
// 隨時會來的更新不算（Grok 一輪結束後才送 session_info_update：算進去會一直等一個不會結束的回報）。
var turnActivity = map[string]bool{"user_message_chunk": true, "agent_message_chunk": true, "agent_thought_chunk": true,
	"tool_call": true, "tool_call_update": true, "plan": true}

// bgUpdate 是 Grok 的 _x.ai/session_notification（背景工作的進度；只列用得到的欄位）。
type bgUpdate struct {
	SessionID string `json:"sessionId"`
	Update    struct {
		SessionUpdate string `json:"sessionUpdate"`
		// workflow_updated
		RunID        string `json:"run_id"`
		Name         string `json:"name"`
		Status       string `json:"status"`
		Foreground   bool   `json:"foreground"`
		CurrentPhase string `json:"current_phase"`
		// subagent_spawned／subagent_finished
		SubagentID    string `json:"subagent_id"`
		Parent        string `json:"parent_session_id"`
		Child         string `json:"child_session_id"`
		Description   string `json:"description"`
		WorkflowRunID string `json:"workflow_run_id"`
		WillWake      bool   `json:"will_wake"`
		// turn_completed
		PromptID string `json:"prompt_id"`
		Usage    *struct {
			InputTokens      int64 `json:"inputTokens"`
			OutputTokens     int64 `json:"outputTokens"`
			ReasoningTokens  int64 `json:"reasoningTokens"`
			CachedReadTokens int64 `json:"cachedReadTokens"`
		} `json:"usage"`
	} `json:"update"`
}

// workflowEnded 是 workflow 結束的狀態（其他狀態都當作還在跑）。
var workflowEnded = map[string]bool{"complete": true, "completed": true, "failed": true, "error": true, "cancelled": true, "canceled": true, "stopped": true}

// background 記下 agent 的背景工作（Grok 的 workflow、子代理）：還在跑的背景工作讓這一輪留著（hold），
// 在工作步驟裡顯示成一個進行中的步驟；子代理的權限請求對到開它的 session。
func (d *Driver) background(ac *agentConn, params json.RawMessage) {
	var p bgUpdate
	if json.Unmarshal(params, &p) != nil {
		return
	}
	u := p.Update
	sid := p.SessionID
	if u.SessionUpdate == "subagent_spawned" && u.Parent != "" {
		sid = u.Parent
	}
	ac.mu.Lock()
	s := ac.sessions[sid]
	own := s != nil // 不是子代理自己的 session（子代理每一輪結束也會送 turn_completed）
	if s == nil {
		s = ac.children[sid]
	}
	if s != nil && u.SessionUpdate == "subagent_spawned" && u.Child != "" {
		ac.children[u.Child] = s
	}
	ac.mu.Unlock()
	if s == nil {
		return
	}
	var evs []engine.Event
	s.mu.Lock()
	switch u.SessionUpdate {
	case "workflow_updated":
		id := "bg:" + u.RunID
		_, known := s.bg[id]
		switch {
		case u.RunID == "" || u.Foreground || s.dropped[id]:
		case !workflowEnded[u.Status]:
			if !known && s.running { // 只算進行中的這一輪開的背景工作
				s.bg[id] = "背景工作：" + firstNonEmpty(u.Name, "workflow")
				s.show(id)
			}
			if note := "背景工作 " + firstNonEmpty(u.Name, "workflow") + "：" + u.CurrentPhase; u.CurrentPhase != "" && note != s.bgNote {
				// 進度（也讓看門狗知道還在動：背景工作跑的時候這一輪沒有別的事件）
				s.bgNote = note
				evs = append(evs, engine.Event{Type: engine.EventStatus, Text: note})
			}
		case known:
			s.finishBg(id, u.Status == "complete" || u.Status == "completed")
			s.wake = s.running // 做完的 workflow 一定會叫 agent 回報（沒有進行中的一輪時，回報沒人收，不用等）
		}
	case "subagent_spawned":
		// workflow 裡的子代理跟著 workflow 算；單獨開的子代理在 prompt 回來之後還沒做完，就是背景子代理。
		if u.SubagentID != "" && u.WorkflowRunID == "" && s.running {
			s.bg["sub:"+u.SubagentID] = "背景子代理：" + firstNonEmpty(u.Description, "subagent")
		}
	case "subagent_finished":
		if id := "sub:" + u.SubagentID; s.bg[id] != "" {
			s.finishBg(id, u.Status == "" || u.Status == "completed" || u.Status == "complete")
			if u.WillWake && s.running {
				s.wake = true
			}
		}
		ac.mu.Lock()
		delete(ac.children, u.Child)
		ac.mu.Unlock()
	case "turn_completed":
		if !own {
			break
		}
		if s.holding && s.agentTurn && u.Usage != nil {
			evs = append(evs, engine.Event{Type: engine.EventUsage, Usage: &engine.Usage{
				InputTokens: u.Usage.InputTokens, OutputTokens: u.Usage.OutputTokens, Reasoning: u.Usage.ReasoningTokens, CacheRead: u.Usage.CachedReadTokens,
			}})
		}
		s.agentTurn = false
	}
	running := s.running
	d.rearm(s)
	s.mu.Unlock()
	if !running {
		return
	}
	for _, e := range evs {
		e.Session = s.id
		s.q.push(e)
	}
}

// request 處理 agent 發來的請求：權限請求交給人（needs_approval）；XBot 沒宣告支援的檔案、終端機請求一律拒絕。
// D1: Each session has its own agent, so requests must have a session.
func (d *Driver) request(ac *agentConn, r request) {
	// For non-permission requests, refuse them
	if r.Method != "session/request_permission" {
		// D1: Can't respond without a session context
		d.log.Debug("Refusing non-permission request without session", "bot", ac.botID, "method", r.Method)
		return
	}

	var params struct {
		SessionID string       `json:"sessionId"`
		ToolCall  permAction   `json:"toolCall"`
		Options   []permOption `json:"options"`
	}
	_ = json.Unmarshal(r.Params, &params)
	ac.mu.Lock()
	s := ac.sessions[params.SessionID]
	if s == nil {
		s = ac.children[params.SessionID] // 子代理的權限請求：算在開它的那一輪
	}
	ac.mu.Unlock()
	if s == nil {
		// No session found
		d.log.Debug("Permission request for unknown session", "bot", ac.botID, "session", params.SessionID)
		return
	}
	s.mu.Lock()
	running, cancelling, mode := s.running, s.cancelling, s.mode
	s.mu.Unlock()
	if !running || cancelling {
		_ = s.gen.c.answer(r.ID, cancelledOutcome)
		return
	}
	if autoAllow(mode, params.ToolCall) {
		for _, k := range []string{"allow_once", "allow_always"} {
			for _, o := range params.Options {
				if o.Kind == k {
					if err := s.gen.c.answer(r.ID, map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": o.OptionID}}); err != nil {
						d.log.Debug("代答權限請求失敗", "bot", ac.botID, "err", err)
					}
					return
				}
			}
		}
	}
	id := idKey(r.ID)
	ac.mu.Lock()
	ac.requests[id] = pending{id: r.ID, session: s.id, options: params.Options}
	ac.mu.Unlock()
	choices := []string{}
	has := func(prefix string) bool {
		return slices.ContainsFunc(params.Options, func(o permOption) bool { return strings.HasPrefix(o.Kind, prefix) })
	}
	if has("allow") {
		choices = append(choices, "once")
	}
	if slices.ContainsFunc(params.Options, func(o permOption) bool { return o.Kind == "allow_always" }) {
		choices = append(choices, "always")
	}
	choices = append(choices, "deny")
	prompt := firstNonEmpty(params.ToolCall.Title, "Bot 想執行一個動作")
	s.q.push(engine.Event{Type: engine.EventNeedsApproval, Session: s.id, Request: &engine.Request{
		ID: id, Kind: "approval", Prompt: prompt, Choices: choices, Params: r.Params,
	}})
}

// permAction 是權限請求裡的 toolCall（ACP ToolCallUpdate；只列用得到的欄位）。
type permAction struct {
	ToolCallID string          `json:"toolCallId"`
	Title      string          `json:"title"`
	Kind       string          `json:"kind"`
	RawInput   json.RawMessage `json:"rawInput"`
	Locations  []struct {
		Path string `json:"path"`
	} `json:"locations"`
}

// readOnlyKinds 是只讀的動作（ACP ToolKind）：讀檔、搜尋、讀網頁、思考。和 Hermes 的 web_search、web_extract 一樣
// 任何權限等級都不用問人（2026-10-21：Grok 的 Bot 讀一個網頁、研究的子代理每讀一頁都開一張核准卡，事情做不完）。
var readOnlyKinds = map[string]bool{"read": true, "search": true, "fetch": true, "think": true}

// autoAllow 回答 XBot 能不能直接替使用者允許這個權限請求（agent 自己的模式之外，XBot 再代答一層）：
// 只讀的一律允許；「一般的事自己做」以上，改的、搬的是 /workspace 裡的一般檔案也允許。
// 指令、刪除、其他動作，以及改設定檔（隱藏的資料夾或檔案，例：.grok/config.toml 能關掉核准）照樣問人（SEC-2）。
func autoAllow(mode string, a permAction) bool {
	if readOnlyKinds[a.Kind] || xbotTool(a) {
		return true
	}
	if (a.Kind != "edit" && a.Kind != "move") || mode == "" || mode == "ask" {
		return false
	}
	paths := []string{}
	for _, l := range a.Locations {
		paths = append(paths, l.Path)
	}
	var in map[string]any
	_ = json.Unmarshal(a.RawInput, &in)
	for _, k := range []string{"file_path", "path", "target_file", "filePath", "source", "destination", "new_path"} {
		if v, ok := in[k].(string); ok {
			paths = append(paths, v)
		}
	}
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if !plainWorkspaceFile(p) {
			return false
		}
	}
	return true
}

// xbotTool 回答這是不是 XBot 自己的 MCP 工具（伺服器 xbot）。XBot 的操作自己決定要不要使用者核准（confirm 級的會開核准卡，
// SEC-2），agent 再問一次是多一道關卡（2026-10-21：Grok 查隊友名冊也要核准）；Hermes 呼叫 XBot 工具本來就不問。
func xbotTool(a permAction) bool {
	var in struct {
		ToolName string `json:"tool_name"`
	}
	_ = json.Unmarshal(a.RawInput, &in)
	for _, name := range []string{in.ToolName, a.Title} {
		if strings.HasPrefix(name, "xbot__") || strings.HasPrefix(name, "mcp__xbot__") {
			return true
		}
	}
	return false
}

// plainWorkspaceFile 回答這是不是 /workspace 裡的一般檔案（不是隱藏的資料夾或檔案，沒有「..」）。
func plainWorkspaceFile(p string) bool {
	rel, ok := strings.CutPrefix(p, Workspace+"/")
	if !ok || rel == "" {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}
