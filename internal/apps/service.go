package apps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"xbot/internal/engine/hermes"
	"xbot/internal/ipc"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// 連接的狀態（I01、I04）。
const (
	StatusOK          = "ok"           // 可以使用
	StatusNeedsSignin = "needs_signin" // 需要登入（還沒登入，或你按了重新登入）
	StatusExpired     = "expired"      // 授權已過期（換新權杖被拒）
	StatusError       = "error"        // 連不上或回應不對
	StatusDisabled    = "disabled"     // 你關掉了，或還缺環境變數
	StatusChecking    = "checking"     // 還沒測試（本機程式要等 Bot 第一次用）
	StatusSigningIn   = "signing_in"   // 登入中（只在畫面上，不存進資料庫）
)

// Options 是 Service 的設定。
type Options struct {
	DB   *store.DB
	Keys Sealer // 加密祕密（*proxy.Keyring）
	// HTTP 呼叫遠端 MCP 與授權伺服器；nil 用預設（逾時 limits.AppHTTPTimeout）。
	HTTP *http.Client
	// BaseURL 是 XBot 給瀏覽器用的網址（OAuth 回呼用）；呼叫端沒給 redirect_base 時用它。
	BaseURL string
	// OnChange 在 Bot 能用的連接改變時呼叫（botIDs 是 nil 表示所有 Bot）：重寫 Hermes 設定、請 Hermes 重新載入 MCP。
	// ACP 引擎在下一個 session 自動拿到新的清單。可為 nil。
	OnChange func(ctx context.Context, botIDs []string)
	// OpenOnBot 在 Bot 的電腦打開登入頁並請你接手（I03 主要流程，B14）；nil 時只給登入網址，在你自己的裝置登入。
	OpenOnBot func(ctx context.Context, botID, url string) error
	// Browser 讀寫 Bot 瀏覽器的登入（cookies，I05、E09）；nil 時 logins 操作回 internal。
	Browser Browser
	Logger  *slog.Logger
	// Now 是時間來源（測試用）；nil 用 time.Now。
	Now func() time.Time
}

// Service 是連接應用程式與登入。
type Service struct {
	o            Options
	mu           sync.Mutex
	flows        map[string]*flow     // flow id → 進行中的登入
	sessions     sync.Map             // botID+"\x00"+connectionID → *upSession（轉送遠端 MCP 的 session）
	notify       func(...store.Event) // 寫進 event 表後推給畫面（Register 時接上 reg.Notify）
	refreshLocks sync.Map             // connectionID → *sync.Mutex（同一個連接一次只換一次權杖）
}

// New 建立 Service。
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{o: o, flows: map[string]*flow{}}
}

func (s *Service) http() *http.Client {
	if s.o.HTTP != nil {
		return s.o.HTTP
	}
	return &http.Client{Timeout: limits.AppHTTPTimeout}
}

// ── 給畫面的樣子 ─────────────────────────────────────

// Connection 是一個連接給畫面（與 MCP）看的樣子：沒有任何祕密，只有末四碼與名稱。
type Connection struct {
	ID            string          `json:"id"`
	AppID         string          `json:"app_id" desc:"目錄 id；自訂的是 custom"`
	Name          string          `json:"name" desc:"在 Bot 設定裡的名字（工具叫 mcp__<name>__<工具>）"`
	Title         string          `json:"title"`
	Description   string          `json:"description"`
	Icon          string          `json:"icon"`
	Custom        bool            `json:"custom"`
	Transport     Transport       `json:"transport"`
	URL           string          `json:"url"`
	Command       string          `json:"command"`
	Args          []string        `json:"args"`
	Auth          AuthKind        `json:"auth"`
	Account       string          `json:"account"`
	Status        string          `json:"status" desc:"ok、needs_signin、expired、error、disabled、checking、signing_in"`
	Error         string          `json:"error"`
	SecretHint    string          `json:"secret_hint" desc:"已存的權杖或金鑰末四碼；空白表示沒存"`
	HeaderNames   []string        `json:"header_names"`
	EnvNames      []string        `json:"env_names"`
	MissingEnv    []string        `json:"missing_env"`
	Tools         []store.AppTool `json:"tools"`
	DisabledTools []string        `json:"disabled_tools"`
	AllBots       bool            `json:"all_bots"`
	BotIDs        []string        `json:"bot_ids" desc:"只給指定的 Bot 時，是哪些 Bot"`
	Enabled       bool            `json:"enabled"`
	CheckedAt     int64           `json:"checked_at"`
	LastUsedAt    int64           `json:"last_used_at"`
	CreatedAt     int64           `json:"created_at"`
	Signin        *Signin         `json:"signin,omitempty"`
	Uses          []store.AppUse  `json:"uses,omitempty" desc:"最近使用（xbot.apps.get 才有）"`
}

// Signin 是一次進行中的登入。
type Signin struct {
	FlowID    string `json:"flow_id"`
	AuthURL   string `json:"auth_url" desc:"要你打開的登入頁"`
	Redirect  string `json:"redirect_uri" desc:"登入完成後回到的網址；打不開時把網址列整串貼回來"`
	ExpiresAt int64  `json:"expires_at"`
	BotID     string `json:"bot_id,omitempty" desc:"登入頁開在這個 Bot 的電腦上"`
}

func (s *Service) view(ctx context.Context, q *store.Queries, a store.AppConnection) (Connection, error) {
	c := Connection{ID: a.ID, AppID: a.AppID, Name: a.Name, Title: a.Title, Custom: a.AppID == CustomApp, Transport: Transport(a.Transport),
		URL: a.URL, Command: a.Command, Args: a.Args, Auth: AuthKind(a.Auth), Account: a.Account, Status: a.Status, Error: a.Error,
		SecretHint: a.SecretHint, HeaderNames: a.HeaderNames, EnvNames: a.EnvNames, MissingEnv: []string{}, Tools: a.Tools,
		DisabledTools: a.DisabledTools, AllBots: a.AllBots, BotIDs: []string{}, Enabled: a.Enabled, CheckedAt: a.CheckedAt,
		LastUsedAt: a.LastUsedAt, CreatedAt: a.CreatedAt}
	if app, ok := Lookup(a.AppID); ok {
		c.Description, c.Icon = app.Description, app.Icon
	} else {
		c.Icon = "plug"
		if a.Transport == string(TransportStdio) {
			c.Icon = "terminal"
		}
	}
	if !a.AllBots {
		ids, err := q.AppGrants(ctx, a.ID)
		if err != nil {
			return c, err
		}
		c.BotIDs = ids
	}
	if a.Transport == string(TransportStdio) && len(a.EnvNames) > 0 {
		sec, err := s.open(a.ID, a.Secret)
		if err == nil {
			for _, k := range a.EnvNames {
				if strings.TrimSpace(sec.Env[k]) == "" {
					c.MissingEnv = append(c.MissingEnv, k)
				}
			}
		}
	}
	if f := s.flowFor(a.ID); f != nil {
		c.Status = StatusSigningIn
		c.Signin = f.view()
	}
	return c, nil
}

// ── 進行中的登入 ─────────────────────────────────────

type flow struct {
	id, state, verifier string
	connectionID        string
	botID               string
	authURL             string
	started             time.Time
	expires             time.Time
	// 結果：done 之後 err 是 nil 表示成功。
	done bool
	err  error
}

func (f *flow) view() *Signin {
	return &Signin{FlowID: f.id, AuthURL: f.authURL, Redirect: "", ExpiresAt: f.expires.UnixMilli(), BotID: f.botID}
}

func (s *Service) flowFor(connectionID string) *flow {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	for _, f := range s.flows {
		if f.connectionID == connectionID && !f.done {
			return f
		}
	}
	return nil
}

func (s *Service) gcLocked() {
	now := s.o.Now()
	for id, f := range s.flows {
		// 完成的留一段時間讓畫面查結果；過期的直接丟。
		if now.After(f.expires.Add(limits.AppOAuthTimeout)) || (!f.done && now.After(f.expires)) {
			if !f.done {
				f.done, f.err = true, errors.New("登入等太久已取消，請重新連接")
			}
			if now.After(f.expires.Add(limits.AppOAuthTimeout)) {
				delete(s.flows, id)
			}
		}
	}
}

// ── 連接、登入 ───────────────────────────────────────

// CustomApp 是自訂連接的 app_id。
const CustomApp = "custom"

// redirect 決定 OAuth 回呼網址：呼叫端給的網站開頭（網頁傳 window.location.origin），沒有時用服務的網址。
func (s *Service) redirect(base string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = strings.TrimRight(s.o.BaseURL, "/")
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return "", ops.InvalidInput(ops.FieldError{Field: "redirect_base", Message: "要是 XBot 的網址開頭，例：https://xbot.example.ts.net"})
	}
	return base + CallbackPath, nil
}

// startSignin 為一個 OAuth 連接開始登入：找授權伺服器、準備用戶端、產生 PKCE 與登入網址。
func (s *Service) startSignin(ctx context.Context, a store.AppConnection, redirectBase, clientID, clientSecret, botID string) (*Signin, error) {
	redirectURI, err := s.redirect(redirectBase)
	if err != nil {
		return nil, err
	}
	sec, err := s.open(a.ID, a.Secret)
	if err != nil {
		return nil, ops.Internal(err)
	}
	st := sec.OAuth
	if st == nil || st.AuthURL == "" || (st.Registered && st.RedirectURI != redirectURI) || clientID != "" {
		// 第一次、回呼網址換了（動態註冊的用戶端只認得註冊時的網址），或你給了新的用戶端 ID：重新找一次。
		fresh, err := s.discover(ctx, a.URL)
		if err != nil {
			return nil, ops.Errorf(ops.CodeInvalidInput, "找不到 %s 的登入方式：%v", a.Title, err)
		}
		if clientID == "" && st != nil && !st.Registered && st.ClientID != "" {
			clientID, clientSecret = st.ClientID, st.ClientSecret // 沿用你之前給的用戶端
		}
		if err := s.register(ctx, fresh, redirectURI, clientID, clientSecret); err != nil {
			if errors.Is(err, errNeedsClient) {
				return nil, ops.InvalidInput(ops.FieldError{Field: "client_id", Message: err.Error()})
			}
			return nil, ops.Errorf(ops.CodeInvalidInput, "%v", err)
		}
		if st != nil { // 換用戶端時舊的權杖還能用就先留著，登入完成才換掉
			fresh.AccessToken, fresh.RefreshToken, fresh.Expiry, fresh.TokenType = st.AccessToken, st.RefreshToken, st.Expiry, st.TokenType
		}
		st = fresh
		sec.OAuth = st
		sealed, err := s.seal(a.ID, sec)
		if err != nil {
			return nil, ops.Internal(err)
		}
		if err := s.o.DB.SetAppSecret(ctx, a.ID, sealed); err != nil {
			return nil, ops.Internal(err)
		}
	}
	verifier, challenge := pkce()
	now := s.o.Now()
	f := &flow{id: store.NewID(), state: randomToken(24), verifier: verifier, connectionID: a.ID, started: now, expires: now.Add(limits.AppOAuthTimeout)}
	f.authURL = authorizeURL(st, f.state, challenge)
	s.mu.Lock()
	s.gcLocked()
	for id, old := range s.flows { // 同一個連接只留最新的登入
		if old.connectionID == a.ID && !old.done {
			delete(s.flows, id)
		}
	}
	if len(s.flows) >= limits.AppOAuthPending {
		s.mu.Unlock()
		return nil, ops.Limit("進行中的登入太多了，請先完成或取消其他登入")
	}
	s.flows[f.id] = f
	s.mu.Unlock()
	if botID != "" && s.o.OpenOnBot != nil {
		if err := s.o.OpenOnBot(ctx, botID, f.authURL); err != nil {
			s.o.Logger.Warn("在 Bot 的電腦打開登入頁失敗，改在你的裝置登入", "bot", botID, "err", err)
		} else {
			f.botID = botID
		}
	}
	v := f.view()
	v.Redirect = redirectURI
	return v, nil
}

// SigninResult 是查登入結果的輸出。
type SigninResult struct {
	Status     string      `json:"status" desc:"pending：還在等你登入；done：完成；error：失敗；gone：找不到（過期或已取消）"`
	Error      string      `json:"error,omitempty"`
	Connection *Connection `json:"connection,omitempty"`
}

func (s *Service) signinStatus(ctx context.Context, flowID string) (SigninResult, error) {
	s.mu.Lock()
	s.gcLocked()
	f := s.flows[flowID]
	var done bool
	var ferr error
	var connID string
	if f != nil {
		done, ferr, connID = f.done, f.err, f.connectionID
	}
	s.mu.Unlock()
	if f == nil {
		return SigninResult{Status: "gone", Error: "找不到這次登入，可能已經過期，請重新連接"}, nil
	}
	out := SigninResult{Status: "pending"}
	if done {
		out.Status = "done"
		if ferr != nil {
			out.Status, out.Error = "error", ferr.Error()
		}
	}
	a, err := s.o.DB.GetAppConnection(ctx, connID)
	if err == nil {
		c, err := s.view(ctx, s.o.DB.Queries, a)
		if err != nil {
			return out, ops.Internal(err)
		}
		out.Connection = &c
	}
	return out, nil
}

// finish 用回呼帶回來的 code（或錯誤）完成登入。state 找不到時回 nil flow。
func (s *Service) finish(ctx context.Context, state, code, errCode, errDesc string) (*flow, error) {
	s.mu.Lock()
	s.gcLocked()
	var f *flow
	for _, x := range s.flows {
		if x.state != "" && x.state == state {
			f = x
			break
		}
	}
	if f == nil || f.done {
		s.mu.Unlock()
		if f != nil && f.err == nil {
			return f, nil // 重新整理回呼頁：已經完成了
		}
		return f, errors.New("這次登入已失效，請重新連接後再試")
	}
	f.state = "" // 一次性
	s.mu.Unlock()
	err := s.complete(ctx, f, code, errCode, errDesc)
	s.mu.Lock()
	f.done, f.err = true, err
	s.mu.Unlock()
	return f, err
}

func (s *Service) complete(ctx context.Context, f *flow, code, errCode, errDesc string) error {
	if errCode != "" {
		if errCode == "access_denied" {
			return errors.New("你沒有允許存取，登入已取消")
		}
		msg := errDesc
		if msg == "" {
			msg = errCode
		}
		return fmt.Errorf("登入沒有完成：%s", truncate(msg, 300))
	}
	if code == "" {
		return errors.New("登入頁沒有帶回授權碼，請重新連接")
	}
	a, err := s.o.DB.GetAppConnection(ctx, f.connectionID)
	if err != nil {
		return errors.New("這個連接已經移除了")
	}
	sec, err := s.open(a.ID, a.Secret)
	if err != nil || sec.OAuth == nil {
		return errors.New("這個連接的登入資料壞了，請中斷後重新連接")
	}
	tr, err := s.exchange(ctx, sec.OAuth, code, f.verifier)
	if err != nil {
		return err
	}
	sec.OAuth.apply(tr, s.o.Now())
	sealed, err := s.seal(a.ID, sec)
	if err != nil {
		return err
	}
	a.Secret = sealed
	if acc := tr.account(); acc != "" {
		a.Account = acc
	}
	a.Status, a.Error = StatusOK, ""
	if _, err := s.o.DB.UpdateAppConnection(ctx, a); err != nil {
		return err
	}
	// 登入好了就測一次，記下有哪些工具（失敗不擋：狀態會寫明原因）。
	if _, err := s.test(ctx, a.ID); err != nil {
		s.o.Logger.Warn("登入後測試連線失敗", "app", a.Name, "err", err)
	}
	s.changed(ctx, a, nil)
	if cur, err := s.o.DB.GetAppConnection(ctx, a.ID); err == nil {
		s.closeSigninCards(ctx, nil, cur)
	}
	return nil
}

// cancel 取消進行中的登入。
func (s *Service) cancel(flowID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.flows[flowID]
	if f == nil || f.done {
		return false
	}
	delete(s.flows, flowID)
	return true
}

// ── 通知 Bot ─────────────────────────────────────────

// changed 在連接 a 對 Bot 的可用範圍或內容改變後呼叫：before 是改之前授權的 Bot（nil 表示所有 Bot 或不需要）。
func (s *Service) changed(ctx context.Context, a store.AppConnection, before []string) {
	if s.o.OnChange == nil {
		return
	}
	if a.AllBots || before == nil {
		s.o.OnChange(context.WithoutCancel(ctx), nil)
		return
	}
	ids, err := s.o.DB.AppGrants(ctx, a.ID)
	if err != nil {
		s.o.OnChange(context.WithoutCancel(ctx), nil)
		return
	}
	set := map[string]bool{}
	for _, id := range append(ids, before...) {
		set[id] = true
	}
	s.o.OnChange(context.WithoutCancel(ctx), slices.Sorted(maps.Keys(set)))
}

// ── Bot 電腦裡的 MCP 伺服器 ──────────────────────────

// Servers 回傳一個 Bot 能用的連接在它電腦裡怎麼啟動（Hermes 的 mcp_servers、ACP 的 mcpServers）。
// 遠端網址的連接都是 XBot MCP 橋接＋XBOT_MCP_PATH（權杖留在服務端）；本機程式照你設定的指令，祕密放 env 檔。
// 需要登入、授權過期、停用或缺環境變數的不放（工具會在登入後出現）。
func (s *Service) Servers(ctx context.Context, botID string) ([]hermes.AppServer, error) {
	conns, err := s.o.DB.AppConnectionsForBot(ctx, botID)
	if err != nil {
		return nil, err
	}
	out := []hermes.AppServer{}
	for _, a := range conns {
		switch a.Status {
		case StatusNeedsSignin, StatusExpired, StatusDisabled:
			continue
		}
		if !hermes.AppNameRE.MatchString(a.Name) || hermes.ReservedApp(a.Name) {
			continue
		}
		switch Transport(a.Transport) {
		case TransportHTTP:
			out = append(out, hermes.AppServer{Name: a.Name, Command: hermes.Python, Args: []string{hermes.XBotMCP},
				Env: map[string]string{"XBOT_MCP_PATH": ipc.AppsPrefix + a.Name}})
		case TransportStdio:
			sec, err := s.open(a.ID, a.Secret)
			if err != nil {
				s.o.Logger.Warn("解不開連接的祕密，先不給 Bot", "app", a.Name, "err", err)
				continue
			}
			missing := false
			for _, k := range a.EnvNames {
				if strings.TrimSpace(sec.Env[k]) == "" {
					missing = true
				}
			}
			if missing || a.Command == "" {
				continue
			}
			env := map[string]string{}
			maps.Copy(env, sec.Env)
			out = append(out, hermes.AppServer{Name: a.Name, Command: a.Command, Args: a.Args, Secret: env})
		}
	}
	return out, nil
}

// DB 回傳資料庫（ops 用）。
func (s *Service) DB() *store.DB { return s.o.DB }
