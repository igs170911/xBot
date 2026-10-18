// Package hermes 是 Hermes Agent 的驅動：經容器內 hermes_host.py 的 /rpc（Hermes 原生 TUI gateway JSON-RPC）
// 開 session、送訊息、串流事件、中斷，把事件正規化成 engine.Event（TECH-DESIGN §4、PRD EN-2）。
// 行為照 LazyBoy server/rpc.py 與 server/runtime.py。
package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"xbot/internal/engine"
	"xbot/internal/limits"
)

// Name 是這個引擎在 bot.engine 的名稱。
const Name = "hermes"

// Provider 是 Hermes 設定裡代表 XBot Model Proxy 的供應商（config.yaml 的 providers.product）。
const Provider = "custom:product"

// Endpoint 是連到一個 Bot 的 Hermes 需要的東西。
type Endpoint struct {
	// Addr 是 hermes_host.py 發布在主機上的位址（host:port）。
	Addr string
	// Key 是這個 Bot 的 worker-key（hermes_host.py 用它驗證 /rpc）。
	Key string
}

// Connector 讓 Bot 的電腦就緒並回傳連線位址；由 runtime 提供（開機、等就緒、寫好 Hermes 設定）。
type Connector func(ctx context.Context, botID string) (Endpoint, error)

// Driver 實作 engine.Driver。
type Driver struct {
	connect Connector
	log     *slog.Logger

	// IdleWait、StartTimeout 為 0 時用 limits.HermesIdleWait、limits.HermesStartTimeout（測試可以調短）。
	IdleWait     time.Duration
	StartTimeout time.Duration

	mu    sync.Mutex
	bots  map[string]*botConn
	dials map[string]*sync.Mutex
}

var _ engine.Driver = (*Driver)(nil)

// New 建立 Hermes 驅動。
func New(connect Connector, log *slog.Logger) *Driver {
	if log == nil {
		log = slog.Default()
	}
	return &Driver{connect: connect, log: log, bots: map[string]*botConn{}, dials: map[string]*sync.Mutex{}}
}

// botConn 是一個 Bot 的連線與上面所有 session。
type botConn struct {
	botID string
	c     *conn

	mu       sync.Mutex
	sessions map[string]*session // Hermes session_id → session
	threads  map[string]*session // XBot thread id → session
	requests map[string]pending  // 等人回覆的伺服器請求
}

type pending struct {
	id      json.RawMessage
	method  string
	params  json.RawMessage
	session string
}

// session 實作 engine.Session。
type session struct {
	bc       *botConn
	id       string
	threadID string

	mu     sync.Mutex
	resume string
	// running：收到 message.start 到 message.complete 之間。
	running bool
	// busy：Hermes 那邊這個 session 還在忙。message.complete 之後 Hermes 還要收尾，收完才放開，
	// 並送 session.info（running=false）；這之前送出的 prompt 會被當成插話，不會開新的一輪。
	busy bool
	idle chan struct{} // busy 時不是 nil；閒下來時關閉
	// 等 message.start 的看門狗：seq 每送一次加一，舊的計時器就不算數。
	seq      int
	awaiting bool
	timer    *time.Timer
	q        *queue
}

func (s *session) setBusyLocked(busy bool) {
	switch {
	case busy && !s.busy:
		s.busy, s.idle = true, make(chan struct{})
	case !busy && s.busy:
		s.busy = false
		close(s.idle)
		s.idle = nil
	}
}

// waitIdle 等這個 session 閒下來，最多 d。逾時回傳 false；ctx 結束回傳 ctx 的錯誤。
func (s *session) waitIdle(ctx context.Context, d time.Duration) (bool, error) {
	s.mu.Lock()
	idle := s.idle
	s.mu.Unlock()
	if idle == nil {
		return true, nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-idle:
		return true, nil
	case <-t.C:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// stopAwaitingLocked 收起等 message.start 的看門狗。
func (s *session) stopAwaitingLocked() {
	s.awaiting = false
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

func (s *session) ID() string { return s.id }

func (s *session) ResumeID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resume
}

func (s *session) setResume(id string) {
	if id == "" {
		return
	}
	s.mu.Lock()
	s.resume = id
	s.mu.Unlock()
}

// Name 實作 engine.Driver。
func (d *Driver) Name() string { return Name }

// client 回傳 Bot 目前的連線；沒有或已斷就重新連（同一個 Bot 同時只連一次）。
func (d *Driver) client(ctx context.Context, botID string) (*botConn, error) {
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
	bc := d.bots[botID]
	d.mu.Unlock()
	if bc != nil && bc.c.alive() {
		return bc, nil
	}
	ep, err := d.connect(ctx, botID)
	if err != nil {
		return nil, err
	}
	bc = &botConn{botID: botID, sessions: map[string]*session{}, threads: map[string]*session{}, requests: map[string]pending{}}
	c, err := dial(ctx, ep.Addr, ep.Key, d.log.With("bot", botID), bc.event, func(r serverRequest) { d.request(bc, r) }, func(error) { d.disconnected(bc) })
	if err != nil {
		return nil, err
	}
	bc.c = c
	d.mu.Lock()
	d.bots[botID] = bc
	d.mu.Unlock()
	return bc, nil
}

// Start 實作 engine.Driver：同一條連線上，同一個對話串只開一個 session；有存下的 session 就先接回（session.resume），
// 接不回才開新的（session.create）。同 LazyBoy runtime.session()。
func (d *Driver) Start(ctx context.Context, o engine.StartOptions) (engine.Session, error) {
	bc, err := d.client(ctx, o.BotID)
	if err != nil {
		return nil, err
	}
	bc.mu.Lock()
	if s := bc.threads[o.ThreadID]; s != nil {
		bc.mu.Unlock()
		return s, nil
	}
	bc.mu.Unlock()

	var res struct {
		SessionID       string `json:"session_id"`
		StoredSessionID string `json:"stored_session_id"`
	}
	resumed := false
	if o.Resume != "" {
		raw, err := bc.c.call(ctx, "session.resume", map[string]any{"session_id": o.Resume, "source": "cli"}, 0)
		if err == nil && json.Unmarshal(raw, &res) == nil && res.SessionID != "" {
			resumed = true
		} else if errors.Is(err, engine.ErrDisconnected) {
			return nil, err
		}
	}
	if !resumed {
		params := map[string]any{"cwd": "/workspace", "source": "cli", "provider": Provider}
		if o.Model != "" {
			params["model"] = o.Model
		}
		if o.Title != "" {
			params["title"] = o.Title
		}
		raw, err := bc.c.call(ctx, "session.create", params, 0)
		if err != nil {
			return nil, fmt.Errorf("無法開始對話：%w", err)
		}
		if err := json.Unmarshal(raw, &res); err != nil || res.SessionID == "" {
			return nil, fmt.Errorf("開始對話的回覆不完整：%s", truncate(string(raw), 200))
		}
	}
	s := &session{bc: bc, id: res.SessionID, threadID: o.ThreadID, q: newQueue()}
	switch {
	case res.StoredSessionID != "":
		s.resume = res.StoredSessionID
	case resumed:
		s.resume = o.Resume
	default:
		s.resume = res.SessionID
	}
	bc.mu.Lock()
	if old := bc.threads[o.ThreadID]; old != nil {
		// 另一個 Start 同時完成了，用先到的那個。
		bc.mu.Unlock()
		return old, nil
	}
	bc.sessions[s.id] = s
	bc.threads[o.ThreadID] = s
	bc.mu.Unlock()
	return s, nil
}

func (d *Driver) own(s engine.Session) (*session, error) {
	hs, ok := s.(*session)
	if !ok {
		return nil, errors.New("不是 Hermes 的 session")
	}
	if !hs.bc.c.alive() {
		return nil, engine.ErrDisconnected
	}
	return hs, nil
}

// Send 實作 engine.Driver（prompt.submit）。
//
// 先等 Hermes 把上一輪收完尾（見 session.busy），再送出。送出時一律帶 queued=true：萬一 Hermes 還在忙
// （等太久、或它自己在跑別的），這則訊息會排在後面、等它忙完自己開一輪，而不是被當成插話併進已經結束的那一輪。
// 回覆 streaming 之後開始等 message.start，超過 StartTimeout 就以錯誤結束這一輪。
func (d *Driver) Send(ctx context.Context, s engine.Session, m engine.Message) error {
	hs, err := d.own(s)
	if err != nil {
		return err
	}
	idle, err := hs.waitIdle(ctx, durationOr(d.IdleWait, limits.HermesIdleWait))
	if err != nil {
		return err
	}
	if !idle {
		d.log.Warn("Hermes 的上一輪還沒收完尾，改成排隊送出", "bot", hs.bc.botID, "session", hs.id)
	}
	// 先標成忙、開始等 message.start：Hermes 的事件可能比 prompt.submit 的回覆先到。
	hs.mu.Lock()
	hs.setBusyLocked(true)
	hs.stopAwaitingLocked()
	hs.seq++
	seq := hs.seq
	hs.awaiting = true
	hs.mu.Unlock()

	raw, err := hs.bc.c.call(ctx, "prompt.submit", map[string]any{"session_id": hs.id, "text": m.Text, "queued": true}, 0)
	if err != nil {
		hs.mu.Lock()
		if hs.seq == seq {
			hs.stopAwaitingLocked()
			if !hs.running {
				hs.setBusyLocked(false)
			}
		}
		hs.mu.Unlock()
		return err
	}
	var res struct {
		Status          string `json:"status"`
		StoredSessionID string `json:"stored_session_id"`
	}
	_ = json.Unmarshal(raw, &res)
	hs.setResume(res.StoredSessionID)
	switch res.Status {
	case "streaming", "":
		hs.mu.Lock()
		if hs.seq == seq && hs.awaiting {
			hs.timer = time.AfterFunc(durationOr(d.StartTimeout, limits.HermesStartTimeout), func() { d.startTimedOut(hs, seq) })
		}
		hs.mu.Unlock()
		return nil
	case "queued":
		// Hermes 還在跑別的；忙完會自己開這一輪。可能等很久，交給 runtime 的看門狗。
		hs.mu.Lock()
		if hs.seq == seq {
			hs.stopAwaitingLocked()
		}
		hs.mu.Unlock()
		return nil
	default:
		// steered／redirected：訊息被併進正在收尾的那一輪，不會有自己的一輪。帶了 queued=true 不應該發生。
		hs.mu.Lock()
		if hs.seq == seq {
			hs.stopAwaitingLocked()
		}
		hs.mu.Unlock()
		d.log.Warn("Hermes 沒有為這則訊息開新的一輪", "bot", hs.bc.botID, "session", hs.id, "status", res.Status)
		return fmt.Errorf("Bot 還在收尾上一輪，這則訊息沒有開始（%s），請再送一次", res.Status)
	}
}

// startTimedOut：送出後一直等不到 message.start，以錯誤結束這一輪，不會永遠等下去。
func (d *Driver) startTimedOut(hs *session, seq int) {
	hs.mu.Lock()
	if hs.seq != seq || !hs.awaiting {
		hs.mu.Unlock()
		return
	}
	hs.stopAwaitingLocked()
	hs.mu.Unlock()
	d.log.Warn("送出後等不到 Hermes 開始這一輪", "bot", hs.bc.botID, "session", hs.id)
	hs.q.push(engine.Event{Type: engine.EventError, Session: hs.id, Text: "Bot 收下訊息後一直沒有開始，已結束這次工作；可以再送一次"})
	hs.q.push(engine.Event{Type: engine.EventDone, Session: hs.id, Status: engine.StatusError})
}

func durationOr(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Interrupt 實作 engine.Driver（session.interrupt）。
func (d *Driver) Interrupt(ctx context.Context, s engine.Session) error {
	hs, err := d.own(s)
	if err != nil {
		return err
	}
	_, err = hs.bc.c.call(ctx, "session.interrupt", map[string]any{"session_id": hs.id}, 0)
	return err
}

// Respond 實作 engine.Driver：回覆格式照 LazyBoy runtime.answer()。
func (d *Driver) Respond(ctx context.Context, s engine.Session, requestID string, a engine.Answer) error {
	hs, err := d.own(s)
	if err != nil {
		return err
	}
	bc := hs.bc
	bc.mu.Lock()
	p, ok := bc.requests[requestID]
	if ok && p.session == hs.id {
		// 先拿走，避免同時兩個回覆都送出去。
		delete(bc.requests, requestID)
	}
	bc.mu.Unlock()
	if !ok || p.session != hs.id {
		return errors.New("這個問題已結束，請重新整理對話")
	}
	var result any
	switch p.method {
	case "clarify":
		var params struct {
			Questions []struct {
				QID string `json:"qid"`
			} `json:"questions"`
		}
		_ = json.Unmarshal(p.params, &params)
		answers := map[string]string{}
		for i, q := range params.Questions {
			answers[q.QID] = a.Text
			if i < len(a.Answers) && a.Answers[i] != "" {
				answers[q.QID] = a.Answers[i]
			}
		}
		result = map[string]any{"answers": answers, "outcome": "answered"}
	case "approval":
		choice := a.Choice
		if choice != "once" && choice != "always" {
			choice = "deny"
		}
		result = map[string]any{"choice": choice}
	case "secret", "sudo", "vault.code", "vault.unlock_prompt", "vault.save_login":
		result = map[string]any{"value": a.Value}
	default:
		result = map[string]any{"settled_by": "user", "targets": []any{}}
	}
	return bc.c.answer(ctx, p.id, result)
}

// Events 實作 engine.Driver。
func (d *Driver) Events(s engine.Session) <-chan engine.Event {
	hs, ok := s.(*session)
	if !ok {
		ch := make(chan engine.Event)
		close(ch)
		return ch
	}
	return hs.q.out
}

// Close 關掉一個 Bot 的連線（電腦停止或刪除時）；它的 session 都會收到連線中斷。
func (d *Driver) Close(botID string) {
	d.mu.Lock()
	bc := d.bots[botID]
	d.mu.Unlock()
	if bc != nil && bc.c != nil {
		bc.c.close()
	}
}

func (d *Driver) disconnected(bc *botConn) {
	d.mu.Lock()
	if d.bots[bc.botID] == bc {
		delete(d.bots, bc.botID)
	}
	d.mu.Unlock()
	bc.mu.Lock()
	sessions := bc.sessions
	bc.sessions, bc.threads, bc.requests = map[string]*session{}, map[string]*session{}, map[string]pending{}
	bc.mu.Unlock()
	for _, s := range sessions {
		s.q.push(engine.Event{Type: engine.EventError, Session: s.id, Text: engine.ErrDisconnected.Error(), Fatal: true})
		s.q.close()
	}
}

// 人要回覆的請求種類（LazyBoy runtime.human_request）。
var humanRequests = map[string]string{
	"clarify": "clarify", "approval": "approval",
	"secret": "secret", "sudo": "secret", "vault.code": "secret", "vault.unlock_prompt": "secret", "vault.save_login": "secret",
}

func (d *Driver) request(bc *botConn, r serverRequest) {
	var params struct {
		SessionID   string   `json:"session_id"`
		Prompt      string   `json:"prompt"`
		Description string   `json:"description"`
		Command     string   `json:"command"`
		Choices     []string `json:"choices"`
		Questions   []struct {
			Question string   `json:"question"`
			Choices  []string `json:"choices"`
		} `json:"questions"`
	}
	_ = json.Unmarshal(r.Params, &params)
	kind, human := humanRequests[r.Method]
	bc.mu.Lock()
	s := bc.sessions[params.SessionID]
	bc.mu.Unlock()
	if !human || s == nil {
		ctx, cancel := context.WithTimeout(context.Background(), limits.HermesReadyTimeout)
		defer cancel()
		if err := bc.c.refuse(ctx, r.ID, "This web host does not expose this display surface; use browser_exec or the computer MCP tools."); err != nil {
			d.log.Warn("回覆 Hermes 請求失敗", "bot", bc.botID, "method", r.Method, "err", err)
		}
		return
	}
	id := string(r.ID)
	var sid string
	if json.Unmarshal(r.ID, &sid) == nil {
		id = sid
	}
	prompt := firstNonEmpty(params.Prompt, params.Description, params.Command)
	choices := params.Choices
	if r.Method == "clarify" {
		var qs []string
		for _, q := range params.Questions {
			qs = append(qs, q.Question)
		}
		prompt = strings.Join(qs, "\n\n")
		if len(params.Questions) == 1 {
			choices = params.Questions[0].Choices
		}
	}
	if prompt == "" {
		prompt = "需要你的回覆"
	}
	bc.mu.Lock()
	bc.requests[id] = pending{id: r.ID, method: r.Method, params: r.Params, session: s.id}
	bc.mu.Unlock()
	s.q.push(engine.Event{Type: engine.EventNeedsApproval, Session: s.id, Request: &engine.Request{
		ID: id, Kind: kind, Method: r.Method, Prompt: prompt, Choices: choices, Params: r.Params,
	}})
}

func (bc *botConn) event(p eventParams) {
	bc.mu.Lock()
	s := bc.sessions[p.SessionID]
	bc.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	switch p.Type {
	case "message.start":
		s.running = true
		s.setBusyLocked(true)
		s.stopAwaitingLocked()
	case "message.complete":
		// busy 留著：Hermes 還在收尾，等 session.info。
		s.running = false
		s.stopAwaitingLocked()
	case "session.info":
		var info struct {
			Running *bool `json:"running"`
		}
		if json.Unmarshal(p.Payload, &info) == nil && info.Running != nil {
			s.setBusyLocked(*info.Running)
		}
	case "error":
		if !s.running {
			// 輪外的錯誤：這一輪沒開始就結束了，Hermes 那邊也不在跑。
			s.stopAwaitingLocked()
			s.setBusyLocked(false)
		}
	}
	running := s.running
	s.mu.Unlock()
	evs := normalize(p)
	if p.Type == "error" && !running {
		// 輪外的錯誤（agent 建不起來、輪被拒絕）：Hermes 不會再送 message.complete，這一輪就此結束。
		evs = append(evs, engine.Event{Type: engine.EventDone, Status: engine.StatusError})
	}
	for _, e := range evs {
		e.Session = s.id
		s.q.push(e)
	}
	if p.Type == "request.cancel" {
		var c struct {
			ID any `json:"id"`
		}
		if json.Unmarshal(p.Payload, &c) == nil {
			id := fmt.Sprint(c.ID)
			bc.mu.Lock()
			_, had := bc.requests[id]
			delete(bc.requests, id)
			bc.mu.Unlock()
			if had {
				s.q.push(engine.Event{Type: engine.EventRequestClosed, Session: s.id, Request: &engine.Request{ID: id}})
			}
		}
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
