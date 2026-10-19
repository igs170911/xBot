package screen

import (
	"context"
	"errors"

	"xbot/internal/ops"
	"xbot/internal/store"
)

// BotIn 指定一個 Bot 的電腦。
type BotIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
}

// HandbackIn 是 xbot.computers.handback 的輸入。
type HandbackIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Note  string `json:"note,omitempty" max:"2000" desc:"告訴 Bot 你做了什麼（選填）；不填送出「我剛剛在電腦上操作過，請從目前畫面繼續。」"`
}

// RequestIn 是 xbot.computers.request_takeover 的輸入。
type RequestIn struct {
	Reason string `json:"reason" required:"true" min:"1" max:"500" desc:"一兩句話說明要使用者在電腦上做什麼，例：Threads 要輸入簡訊驗證碼"`
}

// RequestOut 是 xbot.computers.request_takeover 的輸出。
type RequestOut struct {
	RequestResult
	Note string `json:"note"`
}

// Screen 是一台電腦的畫面狀態（E01、E03、E10）。
type Screen struct {
	BotID  string `json:"bot_id"`
	Name   string `json:"name" desc:"Bot 名稱"`
	HostID string `json:"host_id" desc:"電腦所在的主機；還沒有電腦時是 Bot 指定的主機（空白＝第一次開機時自動擺放）"`
	// Box 是電腦的狀態（box.state）。
	Box          string `json:"box" desc:"none 還沒建立、creating 建立中、running 開機中、sleeping 休眠中、updating 更新中、error 出錯"`
	Error        string `json:"error,omitempty" desc:"box=error 時的原因"`
	HostName     string `json:"host_name" desc:"主機名稱；還沒擺放時空白"`
	HostStatus   string `json:"host_status" desc:"主機狀態：online、offline、unknown、updating、failed"`
	Working      bool   `json:"working" desc:"Bot 正在做事"`
	Paused       bool   `json:"paused" desc:"Bot 因為接手而暫停"`
	LastActiveAt int64  `json:"last_active_at" desc:"電腦最後被使用的時間（毫秒）"`
	// Takeover 是正在進行的接手；Request 是 Bot 請你接手（還沒處理的卡片）。
	Takeover *Takeover `json:"takeover,omitempty"`
	Request  *Request  `json:"request,omitempty"`
	Viewers  int       `json:"viewers" desc:"正在看畫面的連線數"`
	You      bool      `json:"you" desc:"正在操作的是你（呼叫者）"`
	// VNC 是畫面 WebSocket 的路徑（noVNC 用）；接手的人加 &mode=control。
	VNC string `json:"vnc"`
}

// ScreensOut 是 xbot.computers.screens 的輸出。
type ScreensOut struct {
	Screens []Screen `json:"screens"`
}

// HandbackOut 是 xbot.computers.handback 的輸出。
type HandbackOut struct {
	HandbackResult
	Screen Screen `json:"screen"`
}

// CloseTabsOut 是 xbot.computers.close_tabs 的輸出。
type CloseTabsOut struct {
	BotID  string `json:"bot_id"`
	Closed int    `json:"closed" desc:"關了幾個分頁；電腦沒開機時是 0"`
}

// SnapshotOut 是 xbot.computers.snapshot 的輸出。
type SnapshotOut struct {
	BotID string `json:"bot_id"`
	// Available 是 false 時電腦沒有開機（休眠中、還沒建立），沒有畫面。
	Available bool   `json:"available"`
	Image     []byte `json:"image,omitempty" desc:"JPEG（base64）"`
	MediaType string `json:"media_type,omitempty"`
	TakenAt   int64  `json:"taken_at,omitempty"`
}

// Register 登錄 xbot.computers 的看畫面與接手操作。svc 是 nil 時只登錄定義（指令列、gen ts）。
func Register(reg *ops.Registry, svc *Service) {
	need := func() error {
		if svc == nil {
			return ops.Internal(errors.New("畫面服務沒有啟動"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[BotIn, Screen]{
		Name: "xbot.computers.screen", Summary: "看一台電腦的畫面狀態：開機或休眠、有沒有人在接手、Bot 有沒有請你接手、幾個人在看",
		Description: "畫面本身用 WebSocket 連 vnc 欄位的路徑（noVNC，預設只能看）。接手前先呼叫 xbot.computers.takeover。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (Screen, error) {
			if err := need(); err != nil {
				return Screen{}, err
			}
			sc, err := svc.Screen(ctx, in.BotID, call.Identity.Actor(), true)
			return sc, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[struct{}, ScreensOut]{
		Name: "xbot.computers.screens", Summary: "列出所有 Bot 的電腦（辦公室）：在哪台主機、開機或休眠、誰在工作、誰在等你接手",
		Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, _ struct{}) (ScreensOut, error) {
			if err := need(); err != nil {
				return ScreensOut{}, err
			}
			all, err := svc.Screens(ctx, call.Identity.Actor())
			out := ScreensOut{Screens: []Screen{}}
			for _, sc := range all {
				if call.Identity.CanAccess(ops.ResourceBot, sc.BotID) {
					out.Screens = append(out.Screens, sc)
				}
			}
			return out, err
		},
	})
	ops.Register(reg, ops.Op[BotIn, Screen]{
		Name: "xbot.computers.takeover", Summary: "接手一台電腦：Bot 先暫停（正在做的那一輪中斷、排隊的先等），你可以直接操作畫面",
		Description: "接手後用畫面 WebSocket 加 mode=control 操作。做完用 xbot.computers.handback 交還，Bot 會在同一條對話接著做。" +
			"別人正在操作時回 conflict。接手中沒有人連著畫面超過 30 分鐘會自動交還。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (Screen, error) {
			if err := need(); err != nil {
				return Screen{}, err
			}
			if !CanView(call.Identity, in.BotID) {
				return Screen{}, ops.Forbidden(call.Identity.DisplayName() + " 不能操作這台電腦")
			}
			if call.DryRun {
				return svc.Screen(ctx, in.BotID, call.Identity.Actor(), false)
			}
			if _, err := svc.Takeover(ctx, in.BotID, call.Identity.Actor(), call.Identity.DisplayName()); err != nil {
				return Screen{}, opErr(err)
			}
			sc, err := svc.Screen(ctx, in.BotID, call.Identity.Actor(), false)
			return sc, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[HandbackIn, HandbackOut]{
		Name: "xbot.computers.handback", Summary: "把電腦交還給 Bot，可以附一句說明；Bot 會從目前的畫面接著做原本的工作",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in HandbackIn) (HandbackOut, error) {
			if err := need(); err != nil {
				return HandbackOut{}, err
			}
			if call.DryRun {
				sc, err := svc.Screen(ctx, in.BotID, call.Identity.Actor(), false)
				return HandbackOut{Screen: sc}, err
			}
			res, err := svc.Handback(ctx, in.BotID, call.Identity.Actor(), in.Note)
			if err != nil {
				return HandbackOut{}, opErr(err)
			}
			sc, err := svc.Screen(ctx, in.BotID, call.Identity.Actor(), false)
			return HandbackOut{HandbackResult: res, Screen: sc}, opErr(err)
		},
	})
	ops.Register(reg, ops.Op[RequestIn, RequestOut]{
		Name: "xbot.computers.request_takeover", Summary: "請使用者接手你的電腦（登入、驗證碼、付款這類一定要真人做的步驟）",
		Description: "使用者的「待我處理」會出現一張接手卡。送出後在這一輪的回覆裡用一兩句話說明要使用者做什麼，然後結束這一輪，" +
			"不要再操作電腦。使用者交還時你會收到一則訊息（含使用者寫的說明），再從當時的畫面接著做。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe, ForBot: true, NoDelegate: true,
		Handle: func(ctx context.Context, call *ops.Call, in RequestIn) (RequestOut, error) {
			if err := need(); err != nil {
				return RequestOut{}, err
			}
			if call.Identity.Kind != ops.KindBot {
				return RequestOut{}, ops.Forbidden("這是給 Bot 自己用的操作")
			}
			if call.DryRun {
				return RequestOut{}, nil
			}
			res, err := svc.RequestTakeover(ctx, call.Identity.ID, in.Reason)
			if err != nil {
				return RequestOut{}, opErr(err)
			}
			note := "已請使用者接手你的電腦。請用一兩句話說明要使用者做什麼，然後結束這一輪；使用者交還時你會收到訊息，再從當時的畫面接著做。"
			if res.AlreadyPending {
				note = "你已經請過使用者接手了，還在等使用者。請結束這一輪，等使用者交還。"
			}
			return RequestOut{RequestResult: res, Note: note}, nil
		},
	})
	ops.Register(reg, ops.Op[BotIn, CloseTabsOut]{
		Name: "xbot.computers.close_tabs", Summary: "關掉 Bot 電腦瀏覽器裡的分頁，只留一個空白分頁（查完網頁沒關的）",
		Description: "電腦沒開機時 closed=0，不會叫醒它。瀏覽器登入不受影響。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (CloseTabsOut, error) {
			if err := need(); err != nil {
				return CloseTabsOut{}, err
			}
			if !CanView(call.Identity, in.BotID) {
				return CloseTabsOut{}, ops.Forbidden(call.Identity.DisplayName() + " 不能碰這個 Bot 的電腦")
			}
			if call.DryRun {
				return CloseTabsOut{BotID: in.BotID}, nil
			}
			n, err := svc.CloseTabs(ctx, in.BotID)
			if errors.Is(err, ErrNotRunning) {
				return CloseTabsOut{BotID: in.BotID}, nil
			}
			if err != nil {
				return CloseTabsOut{}, opErr(err)
			}
			return CloseTabsOut{BotID: in.BotID, Closed: n}, nil
		},
	})
	ops.Register(reg, ops.Op[BotIn, SnapshotOut]{
		Name: "xbot.computers.snapshot", Summary: "擷取一台電腦目前畫面的小縮圖（JPEG）；電腦沒開機時 available=false，不會叫醒它",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in BotIn) (SnapshotOut, error) {
			if err := need(); err != nil {
				return SnapshotOut{}, err
			}
			if !CanView(call.Identity, in.BotID) {
				return SnapshotOut{}, ops.Forbidden(call.Identity.DisplayName() + " 不能看這個 Bot 的畫面")
			}
			img, at, err := svc.Snapshot(ctx, in.BotID)
			if errors.Is(err, ErrNotRunning) {
				return SnapshotOut{BotID: in.BotID}, nil
			}
			if err != nil {
				return SnapshotOut{}, opErr(err)
			}
			return SnapshotOut{BotID: in.BotID, Available: true, Image: img, MediaType: "image/jpeg", TakenAt: at.UnixMilli()}, nil
		},
	})
}

func opErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrNotFound):
		return ops.NotFound("找不到這個 Bot")
	case errors.Is(err, ErrTakenByOther), errors.Is(err, ErrNotTakenOver):
		return ops.Conflict(err.Error())
	case errors.Is(err, ErrNotHolder):
		return ops.Forbidden(err.Error())
	}
	return err
}

// Screen 讀一台電腦的畫面狀態。live 時向 Docker 查實際狀態（較慢）；否則用 box 表。
func (s *Service) Screen(ctx context.Context, botID, actor string, live bool) (Screen, error) {
	bot, err := s.o.DB.GetBot(ctx, botID)
	if err != nil {
		return Screen{}, err
	}
	bx, err := s.o.DB.GetBox(ctx, botID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		bx = store.Box{BotID: botID, HostID: bot.HostID, State: "none"}
	case err != nil:
		return Screen{}, err
	}
	if live && bx.ContainerID != "" {
		if st, err := s.o.Boxes.Status(ctx, botID); err == nil {
			bx.State, bx.Error, bx.HostID = st.State, st.Error, st.HostID
		} else {
			bx.Error = err.Error()
		}
	}
	reqs, err := s.requests(ctx, botID)
	if err != nil {
		return Screen{}, err
	}
	return s.view(ctx, bot, bx, actor, reqs, nil), nil
}

// Screens 列出所有沒封存的 Bot 的電腦（用 box 表，不碰 Docker）。
func (s *Service) Screens(ctx context.Context, actor string) ([]Screen, error) {
	bots, err := s.o.DB.ListBots(ctx, false)
	if err != nil {
		return nil, err
	}
	boxes, err := s.o.DB.ListBoxes(ctx, "")
	if err != nil {
		return nil, err
	}
	byBot := map[string]store.Box{}
	for _, b := range boxes {
		byBot[b.BotID] = b
	}
	open, err := s.o.DB.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, Kinds: []string{InboxKind}})
	if err != nil {
		return nil, err
	}
	reqs := map[string][]Request{}
	for _, it := range open {
		reqs[it.BotID] = append(reqs[it.BotID], Request{InboxID: it.ID, Reason: it.Body, ThreadID: it.ThreadID, At: it.CreatedAt})
	}
	hostCache := map[string]store.Host{}
	out := make([]Screen, 0, len(bots))
	for _, b := range bots {
		bx, ok := byBot[b.ID]
		if !ok {
			bx = store.Box{BotID: b.ID, HostID: b.HostID, State: "none"}
		}
		out = append(out, s.view(ctx, b, bx, actor, reqs[b.ID], hostCache))
	}
	return out, nil
}

func (s *Service) view(ctx context.Context, bot store.Bot, bx store.Box, actor string, reqs []Request, hostCache map[string]store.Host) Screen {
	sc := Screen{BotID: bot.ID, Name: bot.Name, HostID: bx.HostID, Box: bx.State, Error: bx.Error, LastActiveAt: bx.LastActiveAt,
		VNC: VNCPath + "?bot=" + bot.ID}
	if sc.Box == "" {
		sc.Box = "none"
	}
	if th, _ := s.o.Runtime.Active(bot.ID); th != "" {
		sc.Working = true
	}
	if len(reqs) > 0 {
		r := reqs[0]
		sc.Request = &r
	}
	sc.Takeover = s.TakeoverOf(bot.ID)
	sc.Paused = sc.Takeover != nil
	sc.You = sc.Takeover != nil && sc.Takeover.By == actor
	sc.Viewers = s.Viewers(bot.ID)
	h := s.host(ctx, bx.HostID, hostCache)
	sc.HostName, sc.HostStatus = h.Name, h.Status
	return sc
}

func (s *Service) host(ctx context.Context, hostID string, cache map[string]store.Host) store.Host {
	if h, ok := cache[hostID]; ok {
		return h
	}
	// 還沒擺放（第一次開機時才決定）：介面只用狀態判斷離線，當成在線。
	h := store.Host{ID: hostID, Status: store.HostOnline}
	if hostID != "" {
		got, err := s.o.DB.GetHost(ctx, hostID)
		if err == nil {
			h = got
		} else {
			h.Status = store.HostUnknown
		}
	}
	if cache != nil {
		cache[hostID] = h
	}
	return h
}
