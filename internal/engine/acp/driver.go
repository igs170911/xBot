package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

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
	Logger   *slog.Logger
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
}

var _ engine.Driver = (*Driver)(nil)

// New 建立 ACP 驅動。
func New(o Options) *Driver {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	return &Driver{o: o, log: o.Logger.With("engine", o.Agent.Engine), bots: map[string]*agentConn{}, dials: map[string]*sync.Mutex{}}
}

// Name 實作 engine.Driver。
func (d *Driver) Name() string { return d.o.Agent.Engine }

// agentConn 是一個 Bot 的 agent 行程與上面的 session。
type agentConn struct {
	botID       string
	mode        string // 開行程時的權限等級（Agent.ModeEnv 用）
	c           *conn
	loadSession bool

	mu       sync.Mutex
	sessions map[string]*session // ACP sessionId → session
	threads  map[string]*session // XBot thread id → session
	requests map[string]pending  // 等人回覆的權限請求
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
type session struct {
	ac       *agentConn
	id       string
	threadID string

	mu         sync.Mutex
	running    bool
	cancelling bool
	tools      map[string]*toolState
	q          *queue
	// resetAt 是 agent 最近報的訂閱用量恢復時間（毫秒，Claude Code 的 _claude/rateLimit）；額度錯誤沒寫時間時用它。
	resetAt int64
}

func (s *session) ID() string { return s.id }

// ResumeID 實作 engine.Session：ACP 的 sessionId 可以用 session/load 接回。
func (s *session) ResumeID() string { return s.id }

func (d *Driver) authorize(ctx context.Context, botID string) error {
	if d.o.Authorize == nil {
		return nil
	}
	return d.o.Authorize(ctx, botID, d.o.Agent.Engine)
}

// client 回傳 Bot 目前的 agent 連線；沒有或已斷就重新開（同一個 Bot 同時只開一次）。
// 權限用環境變數設定的 agent（Agent.ModeEnv），權限等級改了而且沒有進行中的一輪時重開。
func (d *Driver) client(ctx context.Context, botID, mode string) (*agentConn, error) {
	d.mu.Lock()
	l, ok := d.dials[botID]
	if !ok {
		l = &sync.Mutex{}
		d.dials[botID] = l
	}
	d.mu.Unlock()
	l.Lock()
	defer l.Unlock()

	d.mu.Lock()
	ac := d.bots[botID]
	d.mu.Unlock()
	if ac != nil && ac.c.alive() {
		if len(d.o.Agent.ModeEnv) == 0 || ac.mode == mode || ac.busy() {
			return ac, nil
		}
		d.log.Info("權限等級改了，重開 agent", "bot", botID, "from", ac.mode, "to", mode)
		ac.c.close()
	}
	p, err := d.o.Launch(ctx, botID, d.o.Agent.ForMode(mode))
	if err != nil {
		return nil, err
	}
	ac = &agentConn{botID: botID, mode: mode, sessions: map[string]*session{}, threads: map[string]*session{}, requests: map[string]pending{}}
	log := d.log.With("bot", botID)
	ac.c = newConn(p, log, func(m string, params json.RawMessage) { d.notification(ac, m, params) },
		func(r request) { d.request(ac, r) },
		func(cause error) { d.disconnected(ac, p, cause) })
	ictx, cancel := context.WithTimeout(ctx, limits.ACPInitTimeout)
	defer cancel()
	raw, err := ac.c.call(ictx, "initialize", map[string]any{
		"protocolVersion": ProtocolVersion,
		"clientCapabilities": map[string]any{
			// 檔案與終端機都在 Bot 自己的電腦裡，agent 自己處理；XBot 不代讀寫。
			"fs":       map[string]bool{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]string{"name": "xbot", "title": "XBot", "version": version.Version},
	})
	if err != nil {
		ac.c.close()
		// 有些 CLI 找不到（或登入過期）時，開機就停下來要人輸入授權碼（例：Gemini CLI），stderr 會寫認證失敗。
		if t, ok := p.(StderrTail); ok && authFailRe.MatchString(t.StderrTail()) {
			return nil, fmt.Errorf("%s 沒有啟動：%s 還沒登入或登入已過期。請在這台電腦重新登入，再到 Bot 設定確認已同意使用你的訂閱", d.o.Agent.Label, d.o.Agent.Label)
		}
		return nil, fmt.Errorf("%s 沒有啟動：%w", d.o.Agent.Label, err)
	}
	var init struct {
		ProtocolVersion   int `json:"protocolVersion"`
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(raw, &init); err != nil {
		ac.c.close()
		return nil, fmt.Errorf("%s 的 initialize 回覆看不懂：%w", d.o.Agent.Label, err)
	}
	if init.ProtocolVersion != ProtocolVersion {
		ac.c.close()
		return nil, fmt.Errorf("%s 講的 ACP 版本是 %d，XBot 支援 %d", d.o.Agent.Label, init.ProtocolVersion, ProtocolVersion)
	}
	ac.loadSession = init.AgentCapabilities.LoadSession
	d.mu.Lock()
	d.bots[botID] = ac
	d.mu.Unlock()
	return ac, nil
}

// busy 回答這個 agent 上有沒有進行中的一輪。
func (ac *agentConn) busy() bool {
	ac.mu.Lock()
	sessions := make([]*session, 0, len(ac.sessions))
	for _, s := range ac.sessions {
		sessions = append(sessions, s)
	}
	ac.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		running := s.running
		s.mu.Unlock()
		if running {
			return true
		}
	}
	return false
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
	if s := ac.threads[o.ThreadID]; s != nil {
		ac.mu.Unlock()
		return s, nil
	}
	ac.mu.Unlock()

	var mcp []MCPServer
	if d.o.MCPServers != nil {
		mcp = d.o.MCPServers(o.BotID)
	}
	if mcp == nil {
		mcp = []MCPServer{}
	}
	sctx, cancel := context.WithTimeout(ctx, limits.ACPSessionTimeout)
	defer cancel()
	var res sessionResult
	id := ""
	if o.Resume != "" && ac.loadSession {
		// session/load 會把舊的對話以 session/update 重播；這時 session 還沒登記，重播的內容不會變成事件。
		raw, err := ac.c.call(sctx, "session/load", map[string]any{"sessionId": o.Resume, "cwd": Workspace, "mcpServers": mcp})
		switch {
		case err == nil:
			id = o.Resume
			_ = json.Unmarshal(raw, &res)
		case errors.Is(err, engine.ErrDisconnected):
			return nil, err
		default:
			d.log.Info("接不回之前的 session，開新的", "bot", o.BotID, "session", o.Resume, "err", err)
		}
	}
	if id == "" {
		raw, err := ac.c.call(sctx, "session/new", map[string]any{"cwd": Workspace, "mcpServers": mcp})
		if err != nil {
			return nil, d.explain("無法開始對話", err)
		}
		if err := json.Unmarshal(raw, &res); err != nil || res.SessionID == "" {
			return nil, fmt.Errorf("開始對話的回覆不完整：%s", truncate(string(raw), 200))
		}
		id = res.SessionID
	}
	s := &session{ac: ac, id: id, threadID: o.ThreadID, tools: map[string]*toolState{}, q: newQueue()}
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
	return s, nil
}

// configure 照 Bot 的權限等級設定 session 模式、照設定的模型選模型。agent 沒有對應的選項就不設定；設定失敗只記紀錄。
func (d *Driver) configure(ctx context.Context, s *session, res sessionResult, o engine.StartOptions) {
	cctx, cancel := context.WithTimeout(ctx, limits.ACPCallTimeout)
	defer cancel()
	if mode := d.o.Agent.Modes[o.Mode]; mode != "" && res.Modes != nil && res.Modes.CurrentModeID != mode &&
		slices.ContainsFunc(res.Modes.AvailableModes, func(m modeInfo) bool { return m.ID == mode }) {
		if _, err := s.ac.c.call(cctx, "session/set_mode", map[string]any{"sessionId": s.id, "modeId": mode}); err != nil {
			d.log.Warn("設定 session 模式失敗", "bot", o.BotID, "mode", mode, "err", err)
		}
	}
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
				if _, err := s.ac.c.call(cctx, "session/set_config_option", map[string]any{"sessionId": s.id, "configId": "model", "value": o.Model}); err != nil {
					d.log.Warn("設定模型失敗", "bot", o.BotID, "model", o.Model, "err", err)
				}
				return
			}
		}
		d.log.Info("agent 沒有這個模型，用它的預設", "bot", o.BotID, "model", o.Model)
	}
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
		return fmt.Errorf("%s：%s 還沒登入。請到 Bot 設定連結你的訂閱（xbot.engines.link_credentials），或確認這台電腦上的登入還有效", what, d.o.Agent.Label)
	}
	return fmt.Errorf("%s：%w", what, err)
}

func (d *Driver) own(s engine.Session) (*session, error) {
	as, ok := s.(*session)
	if !ok {
		return nil, errors.New("不是 ACP 的 session")
	}
	if !as.ac.c.alive() {
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
	if err := d.authorize(ctx, as.ac.botID); err != nil {
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
	as.running, as.cancelling = true, false
	as.mu.Unlock()
	params := map[string]any{"sessionId": as.id, "prompt": []map[string]string{{"type": "text", "text": m.Text}}}
	_, err = as.ac.c.start("session/prompt", params, func(raw json.RawMessage, err error) { d.promptDone(as, raw, err) })
	if err != nil {
		as.mu.Lock()
		as.running = false
		as.mu.Unlock()
		return err
	}
	return nil
}

func (d *Driver) promptDone(s *session, raw json.RawMessage, err error) {
	s.mu.Lock()
	s.running = false
	cancelling := s.cancelling
	s.cancelling = false
	s.tools = map[string]*toolState{}
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
	running := as.running
	if running {
		as.cancelling = true
	}
	as.mu.Unlock()
	if !running {
		as.q.push(engine.Event{Type: engine.EventDone, Session: as.id, Status: engine.StatusInterrupted})
		return nil
	}
	ac := as.ac
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
		_ = ac.c.answer(p.id, cancelledOutcome)
	}
	return ac.c.notify("session/cancel", map[string]string{"sessionId": as.id})
}

var cancelledOutcome = map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}

// Respond 實作 engine.Driver：once → allow_once、always → allow_always（沒有就 allow_once）、
// 其他 → reject_once（沒有就 reject_always）；都沒有對應的選項時回 cancelled。
func (d *Driver) Respond(_ context.Context, s engine.Session, requestID string, a engine.Answer) error {
	as, err := d.own(s)
	if err != nil {
		return err
	}
	ac := as.ac
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
	}
	for _, k := range want {
		for _, o := range p.options {
			if o.Kind == k {
				return ac.c.answer(p.id, map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": o.OptionID}})
			}
		}
	}
	return ac.c.answer(p.id, cancelledOutcome)
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
func (d *Driver) Close(botID string) {
	d.mu.Lock()
	ac := d.bots[botID]
	d.mu.Unlock()
	if ac != nil && ac.c != nil {
		ac.c.close()
	}
}

func (d *Driver) disconnected(ac *agentConn, p Process, cause error) {
	d.mu.Lock()
	if d.bots[ac.botID] == ac {
		delete(d.bots, ac.botID)
	}
	d.mu.Unlock()
	attrs := []any{"bot", ac.botID, "cause", cause}
	if t, ok := p.(StderrTail); ok {
		if tail := t.StderrTail(); tail != "" {
			attrs = append(attrs, "stderr", tail)
		}
	}
	d.log.Info("agent 行程結束", attrs...)
	ac.mu.Lock()
	sessions := ac.sessions
	ac.sessions, ac.threads, ac.requests = map[string]*session{}, map[string]*session{}, map[string]pending{}
	ac.mu.Unlock()
	for _, s := range sessions {
		s.q.push(engine.Event{Type: engine.EventError, Session: s.id, Text: engine.ErrDisconnected.Error(), Fatal: true})
		s.q.close()
	}
}

func (d *Driver) notification(ac *agentConn, method string, params json.RawMessage) {
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
	evs := normalize(p.Update, s.tools, time.Now())
	s.mu.Unlock()
	for _, e := range evs {
		e.Session = s.id
		s.q.push(e)
	}
}

// request 處理 agent 發來的請求：權限請求交給人（needs_approval）；XBot 沒宣告支援的檔案、終端機請求一律拒絕。
func (d *Driver) request(ac *agentConn, r request) {
	if r.Method != "session/request_permission" {
		if err := ac.c.refuse(r.ID, codeMethodNotFound, "XBot 不提供 "+r.Method); err != nil {
			d.log.Debug("回覆 agent 請求失敗", "bot", ac.botID, "method", r.Method, "err", err)
		}
		return
	}
	var params struct {
		SessionID string `json:"sessionId"`
		ToolCall  struct {
			ToolCallID string          `json:"toolCallId"`
			Title      string          `json:"title"`
			Kind       string          `json:"kind"`
			RawInput   json.RawMessage `json:"rawInput"`
		} `json:"toolCall"`
		Options []permOption `json:"options"`
	}
	_ = json.Unmarshal(r.Params, &params)
	ac.mu.Lock()
	s := ac.sessions[params.SessionID]
	ac.mu.Unlock()
	if s == nil {
		_ = ac.c.answer(r.ID, cancelledOutcome)
		return
	}
	s.mu.Lock()
	running, cancelling := s.running, s.cancelling
	s.mu.Unlock()
	if !running || cancelling {
		_ = ac.c.answer(r.ID, cancelledOutcome)
		return
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
