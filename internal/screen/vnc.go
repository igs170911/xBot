package screen

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// 畫面連線的模式：view 只能看（預設），control 是接手的人操作。
const (
	ModeView    = "view"
	ModeControl = "control"
)

// VNCPath 是畫面 WebSocket 的路徑（api 套件掛在 guard 後面）。
const VNCPath = "/api/v1/computers/vnc"

// vncConn 是一條畫面連線。
type vncConn struct {
	cancel context.CancelCauseFunc
	once   sync.Once
}

func (c *vncConn) close(reason string) {
	c.once.Do(func() { c.cancel(errors.New(reason)) })
}

// CanView 回答這個身分能不能看這個 Bot 的畫面：你本人可以；代理要有 operate 而且能碰這個 Bot；Bot 自己不行（它有自己的桌面工具）。
func CanView(id ops.Identity, botID string) bool {
	switch id.Kind {
	case ops.KindOwner:
		return true
	case ops.KindAgent:
		return id.HasScope(ops.ScopeOperate) && id.CanAccess(ops.ResourceBot, botID)
	}
	return false
}

// ServeVNC 是 GET /api/v1/computers/vnc?bot=<id>[&mode=control]：把瀏覽器的 noVNC 接到 Bot 電腦的 websockify。
// 身分已經由 api 的 guard 驗過（同源、登入或權杖）。只能看的連線經過 viewOnly 過濾；control 只給正在接手的那個人。
func (s *Service) ServeVNC(w http.ResponseWriter, r *http.Request, id ops.Identity) {
	q := r.URL.Query()
	botID, mode := q.Get("bot"), q.Get("mode")
	if mode == "" {
		mode = ModeView
	}
	fail := func(code int, msg string) { http.Error(w, msg, code) }
	if botID == "" || (mode != ModeView && mode != ModeControl) {
		fail(http.StatusBadRequest, "要帶 bot=<id>，mode 只能是 view 或 control")
		return
	}
	if !CanView(id, botID) {
		fail(http.StatusForbidden, id.DisplayName()+" 不能看這個 Bot 的畫面")
		return
	}
	if _, err := s.o.DB.GetBot(r.Context(), botID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(http.StatusNotFound, "找不到這個 Bot")
			return
		}
		fail(http.StatusInternalServerError, err.Error())
		return
	}
	if mode == ModeControl {
		tk := s.TakeoverOf(botID)
		if tk == nil {
			fail(http.StatusConflict, "要先按「接手操作」才能操作畫面")
			return
		}
		if tk.By != id.Actor() {
			fail(http.StatusForbidden, tk.ByName+" 正在操作這台電腦")
			return
		}
	}
	st, err := s.o.Boxes.Status(r.Context(), botID)
	if err != nil {
		fail(http.StatusBadGateway, "讀不到電腦狀態："+err.Error())
		return
	}
	if !st.Running || st.NoVNCAddr == "" {
		fail(http.StatusConflict, "電腦沒有開機")
		return
	}

	dialCtx, cancelDial := context.WithTimeout(r.Context(), limits.VNCDialTimeout)
	up, _, err := websocket.Dial(dialCtx, "ws://"+st.NoVNCAddr+"/websockify", &websocket.DialOptions{Subprotocols: []string{"binary"}})
	cancelDial()
	if err != nil {
		fail(http.StatusBadGateway, "連不到電腦畫面："+err.Error())
		return
	}
	defer up.CloseNow()
	up.SetReadLimit(limits.VNCMessageMax)

	down, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return // Accept 已經回了錯誤
	}
	defer down.CloseNow()
	down.SetReadLimit(limits.VNCMessageMax)

	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	c := &vncConn{cancel: cancel}
	s.join(botID, c, mode)
	defer s.leave(botID, c, mode)
	defer s.o.Boxes.Hold(botID)() // 看的期間電腦不休眠（B13 閒置休眠）
	go s.touch(ctx, botID)

	var filter *viewOnly
	if mode == ModeView {
		filter = &viewOnly{}
	}
	// 讀寫不跟著 ctx：coder/websocket 的 Read 在 ctx 取消時會直接切斷連線，送不出關閉的原因。
	// 結束時（交還、刪除 Bot、服務關機）由下面的 Close 收尾，處理器回傳時 CloseNow 讓兩個 pump 結束。
	rw := context.WithoutCancel(ctx)
	errc := make(chan error, 2)
	go func() { errc <- pump(rw, down, up, filter) }()
	go func() { errc <- pump(rw, up, down, nil) }()
	select {
	case <-ctx.Done():
		reason := "連線已結束"
		if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
			reason = cause.Error()
		}
		_ = down.Close(websocket.StatusNormalClosure, reason)
	case err := <-errc:
		if errors.Is(err, errRFB) {
			_ = down.Close(websocket.StatusPolicyViolation, "只能看畫面")
		} else {
			_ = down.Close(websocket.StatusNormalClosure, "")
		}
	}
}

// pump 把 from 的訊息轉給 to；filter 不是 nil 時先過濾（空的就不送）。
func pump(ctx context.Context, from, to *websocket.Conn, filter *viewOnly) error {
	for {
		typ, data, err := from.Read(ctx)
		if err != nil {
			return err
		}
		if filter != nil {
			if data, err = filter.Write(data); err != nil {
				return err
			}
			if len(data) == 0 {
				continue
			}
		}
		if err := to.Write(ctx, typ, data); err != nil {
			return err
		}
	}
}

func (s *Service) join(botID string, c *vncConn, mode string) {
	s.mu.Lock()
	st := s.state(botID)
	st.viewers++
	if mode == ModeControl {
		st.controls[c] = struct{}{}
	}
	s.mu.Unlock()
	s.publish(botID, false)
}

func (s *Service) leave(botID string, c *vncConn, mode string) {
	s.mu.Lock()
	st := s.state(botID)
	st.viewers--
	if mode == ModeControl {
		delete(st.controls, c)
		if len(st.controls) == 0 {
			st.idleFrom = s.o.Now()
		}
	}
	s.mu.Unlock()
	s.publish(botID, false)
}

// touch 在連線期間定時記錄電腦正在使用（Hold 之外再保險：服務重開後 Hold 計數不在了，最後使用時間還在）。
func (s *Service) touch(ctx context.Context, botID string) {
	tick := time.NewTicker(s.o.TouchInterval)
	defer tick.Stop()
	for {
		if err := s.o.Boxes.Touch(context.WithoutCancel(ctx), botID); err != nil && !errors.Is(err, store.ErrNotFound) {
			s.log.Warn("記錄電腦使用時間失敗", "bot", botID, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
