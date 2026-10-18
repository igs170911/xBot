package apps

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"xbot/internal/engine/hermes"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// ── 目錄 ─────────────────────────────────────────────

// CatalogItem 是目錄裡的一個應用程式加上目前的狀態（I01）。
type CatalogItem struct {
	App
	State       string   `json:"state" desc:"available：還沒連接；其他是第一個帳號的狀態（ok、needs_signin、expired、error、disabled、checking、signing_in）"`
	Connections []string `json:"connections" desc:"這個應用程式的連接 id（一個帳號一個）"`
}

// CatalogIn 是 xbot.apps.catalog 的輸入。
type CatalogIn struct {
	Query     string `json:"query,omitempty" max:"100" desc:"關鍵字（名稱、說明、網站）"`
	Category  string `json:"category,omitempty" max:"20"`
	Connected bool   `json:"connected,omitempty" desc:"只列已連接的"`
}

// CatalogOut 是 xbot.apps.catalog 的輸出。
type CatalogOut struct {
	Apps       []CatalogItem `json:"apps"`
	Categories []string      `json:"categories"`
	Connected  int           `json:"connected" desc:"已連接的應用程式數（含自訂連接）"`
	Total      int           `json:"total"`
}

func (s *Service) catalog(ctx context.Context, in CatalogIn) (CatalogOut, error) {
	conns, err := s.o.DB.ListAppConnections(ctx)
	if err != nil {
		return CatalogOut{}, ops.Internal(err)
	}
	byApp := map[string][]store.AppConnection{}
	connected := map[string]bool{}
	for _, c := range conns {
		byApp[c.AppID] = append(byApp[c.AppID], c)
		key := c.AppID
		if c.AppID == CustomApp {
			key = "custom:" + c.ID
		}
		connected[key] = true
	}
	all, cats := Catalog()
	q := strings.ToLower(strings.TrimSpace(in.Query))
	out := CatalogOut{Apps: []CatalogItem{}, Categories: cats, Connected: len(connected), Total: len(all)}
	for _, app := range all {
		if in.Category != "" && app.Category != in.Category {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(app.Title+" "+app.Description+" "+app.Site+" "+app.ID), q) {
			continue
		}
		item := CatalogItem{App: app, State: "available", Connections: []string{}}
		for _, c := range byApp[app.ID] {
			item.Connections = append(item.Connections, c.ID)
		}
		if cs := byApp[app.ID]; len(cs) > 0 {
			item.State = cs[0].Status
			if s.flowFor(cs[0].ID) != nil {
				item.State = StatusSigningIn
			}
		}
		if in.Connected && item.State == "available" {
			continue
		}
		out.Apps = append(out.Apps, item)
	}
	return out, nil
}

// ── 列出 ─────────────────────────────────────────────

func (s *Service) list(ctx context.Context, botID string) ([]Connection, error) {
	var conns []store.AppConnection
	var err error
	if botID != "" {
		conns, err = s.o.DB.AppConnectionsForBot(ctx, botID)
	} else {
		conns, err = s.o.DB.ListAppConnections(ctx)
	}
	if err != nil {
		return nil, ops.Internal(err)
	}
	out := []Connection{}
	for _, a := range conns {
		c, err := s.view(ctx, s.o.DB.Queries, a)
		if err != nil {
			return nil, ops.Internal(err)
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *Service) get(ctx context.Context, id string) (Connection, error) {
	a, err := s.load(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	c, err := s.view(ctx, s.o.DB.Queries, a)
	if err != nil {
		return c, ops.Internal(err)
	}
	if c.Uses, err = s.o.DB.ListAppUses(ctx, a.ID, 20); err != nil {
		return c, ops.Internal(err)
	}
	return c, nil
}

func (s *Service) load(ctx context.Context, id string) (store.AppConnection, error) {
	a, err := s.o.DB.GetAppConnection(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return a, ops.NotFound("找不到這個連接")
	}
	if err != nil {
		return a, ops.Internal(err)
	}
	return a, nil
}

// ── 連接目錄裡的應用程式 ─────────────────────────────

// ConnectIn 是 xbot.apps.connect 的輸入。
type ConnectIn struct {
	AppID        string   `json:"app_id" required:"true" max:"64" desc:"目錄裡的應用程式 id（xbot.apps.catalog）"`
	ConnectionID string   `json:"connection_id,omitempty" max:"64" desc:"重新連接（重新登入、換金鑰）已有的這個帳號；不填是新增一個帳號"`
	APIKey       string   `json:"api_key,omitempty" max:"4000" secret:"true" desc:"auth 是 api_key 的應用程式要的金鑰或權杖（例：GitHub Personal Access Token）"`
	ClientID     string   `json:"client_id,omitempty" max:"500" desc:"授權伺服器不開放自動註冊時，你自己的 OAuth 用戶端 ID"`
	ClientSecret string   `json:"client_secret,omitempty" max:"2000" secret:"true" desc:"你自己的 OAuth 用戶端密鑰（有的話）"`
	RedirectBase string   `json:"redirect_base,omitempty" max:"300" desc:"XBot 的網址開頭（登入完成後回到這裡）；網頁會帶目前的網址，不填用服務設定的網址"`
	BotIDs       []string `json:"bot_ids,omitempty" resource:"bot" max:"50" desc:"只給這些 Bot 用；不填是所有 Bot（XBot 預設）"`
	SigninOnBot  string   `json:"signin_on_bot,omitempty" resource:"bot" max:"64" desc:"把登入頁開在這個 Bot 的電腦上（你在即時畫面登入）；不填是在你自己的裝置登入"`
}

// ConnectOut 是 xbot.apps.connect、xbot.apps.add_mcp_server 的輸出。
type ConnectOut struct {
	Connection Connection `json:"connection"`
	Signin     *Signin    `json:"signin,omitempty" desc:"要登入時：打開 auth_url 登入，完成後用 xbot.apps.signin_status 查結果"`
}

func (s *Service) connect(ctx context.Context, call *ops.Call, in ConnectIn) (ConnectOut, error) {
	app, ok := Lookup(in.AppID)
	if !ok {
		return ConnectOut{}, ops.NotFound("目錄裡沒有這個應用程式：" + in.AppID)
	}
	key := strings.TrimSpace(in.APIKey)
	if app.Auth == AuthAPIKey && key == "" && in.ConnectionID == "" {
		return ConnectOut{}, ops.InvalidInput(ops.FieldError{Field: "api_key", Message: "請貼上 " + cmpOr(app.TokenHint, "金鑰")})
	}
	if app.NeedsClient && in.ClientID == "" && in.ConnectionID == "" {
		return ConnectOut{}, ops.InvalidInput(ops.FieldError{Field: "client_id", Message: "請填入 " + cmpOr(app.ClientHint, "OAuth 用戶端 ID")})
	}
	if call.DryRun {
		return ConnectOut{Connection: Connection{AppID: app.ID, Title: app.Title, Auth: app.Auth, Status: StatusChecking}}, nil
	}
	var a store.AppConnection
	var before []string
	if in.ConnectionID != "" {
		var err error
		if a, err = s.load(ctx, in.ConnectionID); err != nil {
			return ConnectOut{}, err
		}
		if a.AppID != app.ID {
			return ConnectOut{}, ops.InvalidInput(ops.FieldError{Field: "connection_id", Message: "這個連接不是 " + app.Title})
		}
	} else {
		conns, err := s.o.DB.ListAppConnections(ctx)
		if err != nil {
			return ConnectOut{}, ops.Internal(err)
		}
		if len(conns) >= limits.AppConnectionsMax {
			return ConnectOut{}, ops.Limit(fmt.Sprintf("最多 %d 個連接", limits.AppConnectionsMax))
		}
		name, err := s.freeName(ctx, app.ID)
		if err != nil {
			return ConnectOut{}, err
		}
		a = store.AppConnection{AppID: app.ID, Name: name, Title: app.Title, Transport: string(TransportHTTP), URL: app.URL,
			Auth: string(app.Auth), AllBots: len(in.BotIDs) == 0, Enabled: true, Status: StatusChecking, CreatedBy: call.Identity.Actor()}
		if a, err = s.o.DB.CreateAppConnection(ctx, a); err != nil {
			return ConnectOut{}, ops.Internal(err)
		}
		if len(in.BotIDs) > 0 {
			if err := s.setGrants(ctx, a.ID, in.BotIDs); err != nil {
				return ConnectOut{}, err
			}
		}
		before = []string{}
	}
	if key != "" {
		sec, err := s.open(a.ID, a.Secret)
		if err != nil {
			return ConnectOut{}, ops.Internal(err)
		}
		sec.APIKey, sec.TokenHeader = key, app.TokenHeader
		if a.Secret, err = s.seal(a.ID, sec); err != nil {
			return ConnectOut{}, ops.Internal(err)
		}
		a.SecretHint = hint(key)
		if a, err = s.o.DB.UpdateAppConnection(ctx, a); err != nil {
			return ConnectOut{}, ops.Internal(err)
		}
	}
	return s.afterSave(ctx, a, before, in.RedirectBase, in.ClientID, in.ClientSecret, in.SigninOnBot)
}

// afterSave：OAuth 的開始登入；其他的測試一次連線。
func (s *Service) afterSave(ctx context.Context, a store.AppConnection, before []string, redirectBase, clientID, clientSecret, onBot string) (ConnectOut, error) {
	if AuthKind(a.Auth) == AuthOAuth2 && Transport(a.Transport) == TransportHTTP {
		a.Status = StatusNeedsSignin
		if err := s.o.DB.SetAppStatus(ctx, a.ID, StatusNeedsSignin, ""); err != nil {
			return ConnectOut{}, ops.Internal(err)
		}
		signin, err := s.startSignin(ctx, a, redirectBase, clientID, clientSecret, onBot)
		if err != nil {
			_ = s.o.DB.SetAppStatus(ctx, a.ID, StatusNeedsSignin, ops.AsError(err).Message)
			return ConnectOut{}, err
		}
		c, err := s.get(ctx, a.ID)
		if err != nil {
			return ConnectOut{}, err
		}
		return ConnectOut{Connection: c, Signin: signin}, nil
	}
	if Transport(a.Transport) == TransportHTTP {
		if _, err := s.test(ctx, a.ID); err != nil {
			return ConnectOut{}, err
		}
	} else if err := s.o.DB.SetAppStatus(ctx, a.ID, s.stdioStatus(a), s.stdioNote(a)); err != nil {
		return ConnectOut{}, ops.Internal(err)
	}
	cur, err := s.load(ctx, a.ID)
	if err != nil {
		return ConnectOut{}, err
	}
	s.changed(ctx, cur, before)
	s.closeSigninCards(ctx, nil, cur)
	c, err := s.get(ctx, a.ID)
	return ConnectOut{Connection: c}, err
}

func (s *Service) stdioStatus(a store.AppConnection) string {
	if !a.Enabled {
		return StatusDisabled
	}
	sec, _ := s.open(a.ID, a.Secret)
	for _, k := range a.EnvNames {
		if strings.TrimSpace(sec.Env[k]) == "" {
			return StatusDisabled
		}
	}
	return StatusChecking
}

func (s *Service) stdioNote(a store.AppConnection) string {
	sec, _ := s.open(a.ID, a.Secret)
	var miss []string
	for _, k := range a.EnvNames {
		if strings.TrimSpace(sec.Env[k]) == "" {
			miss = append(miss, k)
		}
	}
	if len(miss) > 0 {
		return "還缺 " + strings.Join(miss, "、") + "，補上就能啟用"
	}
	return ""
}

// test 由 XBot 連一次遠端，記下工具與狀態（本機程式不能在服務端測，回原狀）。
func (s *Service) test(ctx context.Context, id string) (Connection, error) {
	a, err := s.load(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	if Transport(a.Transport) != TransportHTTP {
		return s.get(ctx, id)
	}
	tools, terr := s.listTools(ctx, a)
	cur, err := s.load(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	cur.CheckedAt = s.o.Now().UnixMilli()
	switch {
	case terr == nil:
		cur.Tools, cur.Status, cur.Error = tools, StatusOK, ""
		if !cur.Enabled {
			cur.Status = StatusDisabled
		}
	case errors.Is(terr, errUnauthorized) || errors.Is(terr, errRelogin):
		if AuthKind(cur.Auth) == AuthOAuth2 {
			cur.Status, cur.Error = StatusNeedsSignin, "需要登入"
			if errors.Is(terr, errRelogin) {
				cur.Status, cur.Error = StatusExpired, errRelogin.Error()
			}
		} else {
			cur.Status, cur.Error = StatusError, "金鑰不對或已失效"
		}
	default:
		cur.Status, cur.Error = StatusError, truncate(terr.Error(), 300)
	}
	if _, err := s.o.DB.UpdateAppConnection(ctx, cur); err != nil {
		return Connection{}, ops.Internal(err)
	}
	return s.get(ctx, id)
}

var nameClean = regexp.MustCompile(`[^a-z0-9_-]+`)

// freeName 找一個還沒用過的連接名字：notion、notion-2…
func (s *Service) freeName(ctx context.Context, base string) (string, error) {
	base = strings.Trim(nameClean.ReplaceAllString(strings.ToLower(base), "-"), "-_")
	if base == "" {
		base = "app"
	}
	if len(base) > 40 {
		base = base[:40]
	}
	for i := 1; i < 1000; i++ {
		name := base
		if i > 1 {
			name = fmt.Sprintf("%s-%d", base, i)
		}
		if hermes.ReservedApp(name) {
			continue
		}
		if _, err := s.o.DB.GetAppConnectionByName(ctx, name); errors.Is(err, store.ErrNotFound) {
			return name, nil
		} else if err != nil {
			return "", ops.Internal(err)
		}
	}
	return "", ops.Conflict("找不到可用的名字")
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ── 自訂 MCP ─────────────────────────────────────────

// ServerIn 是 xbot.apps.add_mcp_server 的輸入（I04）。
type ServerIn struct {
	Name         string            `json:"name" required:"true" max:"48" desc:"名字（英文小寫、數字、- _），Bot 的工具會叫 mcp__<name>__<工具>"`
	Title        string            `json:"title,omitempty" max:"80" desc:"顯示的名稱；不填用 name"`
	Transport    Transport         `json:"transport" required:"true" desc:"http：遠端網址（XBot 代為轉送，權杖不進 Bot 的電腦）；stdio：在 Bot 電腦裡執行的本機程式"`
	URL          string            `json:"url,omitempty" max:"2000" desc:"遠端網址（Streamable HTTP）"`
	Command      string            `json:"command,omitempty" max:"500" desc:"本機程式的指令，例：npx"`
	Args         []string          `json:"args,omitempty" max:"64" desc:"本機程式的參數，例：[\"-y\", \"@acme/mcp-server\"]"`
	Auth         AuthKind          `json:"auth,omitempty" desc:"遠端網址的驗證方式；預設 none"`
	Token        string            `json:"token,omitempty" max:"4000" secret:"true" desc:"auth 是 api_key 時的 Token"`
	TokenHeader  string            `json:"token_header,omitempty" max:"100" desc:"Token 放哪個標頭；預設 Authorization（自動加 Bearer）"`
	Headers      map[string]string `json:"headers,omitempty" secret:"true" desc:"其他標頭（遠端網址）；值加密存放，畫面只看得到名字"`
	Env          map[string]string `json:"env,omitempty" secret:"true" desc:"本機程式的環境變數；值是空的表示還缺（連接會停用並標出缺哪個）"`
	ClientID     string            `json:"client_id,omitempty" max:"500" desc:"auth 是 oauth2、而且服務不開放自動註冊時，你的 OAuth 用戶端 ID"`
	ClientSecret string            `json:"client_secret,omitempty" max:"2000" secret:"true"`
	BotIDs       []string          `json:"bot_ids,omitempty" resource:"bot" max:"50" desc:"只給這些 Bot 用；不填是所有 Bot"`
	Disabled     bool              `json:"disabled,omitempty" desc:"先加進來但不啟用"`
	RedirectBase string            `json:"redirect_base,omitempty" max:"300" desc:"OAuth 登入完成後回到的 XBot 網址開頭"`
}

var headerName = regexp.MustCompile(`^[A-Za-z0-9-]{1,100}$`)
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,99}$`)

func validateServer(in *ServerIn) error {
	var bad []ops.FieldError
	in.Name = strings.TrimSpace(strings.ToLower(in.Name))
	if !hermes.AppNameRE.MatchString(in.Name) {
		bad = append(bad, ops.FieldError{Field: "name", Message: "用英文小寫、數字、- 或 _，最多 48 個字"})
	} else if hermes.ReservedApp(in.Name) {
		bad = append(bad, ops.FieldError{Field: "name", Message: "computer、xbot 是 XBot 自己用的名字"})
	}
	if in.Auth == "" {
		in.Auth = AuthNone
	}
	switch in.Transport {
	case TransportHTTP:
		if !webURL(in.URL) {
			bad = append(bad, ops.FieldError{Field: "url", Message: "要是 http:// 或 https:// 開頭的網址"})
		}
		if in.Auth == AuthAPIKey && strings.TrimSpace(in.Token) == "" {
			bad = append(bad, ops.FieldError{Field: "token", Message: "請填 Token"})
		}
	case TransportStdio:
		if strings.TrimSpace(in.Command) == "" {
			bad = append(bad, ops.FieldError{Field: "command", Message: "請填啟動指令，例：npx"})
		}
		if in.Auth != AuthNone {
			bad = append(bad, ops.FieldError{Field: "auth", Message: "本機程式用環境變數帶金鑰，不支援這種驗證"})
		}
	}
	if in.TokenHeader != "" && !headerName.MatchString(in.TokenHeader) {
		bad = append(bad, ops.FieldError{Field: "token_header", Message: "標頭名稱不對"})
	}
	if len(in.Headers) > limits.AppEnvMax || len(in.Env) > limits.AppEnvMax {
		bad = append(bad, ops.FieldError{Field: "headers", Message: fmt.Sprintf("最多 %d 個", limits.AppEnvMax)})
	}
	for k, v := range in.Headers {
		if !headerName.MatchString(k) || strings.ContainsAny(v, "\r\n") {
			bad = append(bad, ops.FieldError{Field: "headers", Message: "標頭格式不對：" + k})
		}
	}
	for k := range in.Env {
		if !envName.MatchString(k) {
			bad = append(bad, ops.FieldError{Field: "env", Message: "環境變數名稱不對：" + k})
		}
	}
	if len(bad) > 0 {
		return ops.InvalidInput(bad...)
	}
	return nil
}

func (s *Service) addServer(ctx context.Context, call *ops.Call, in ServerIn) (ConnectOut, error) {
	if err := validateServer(&in); err != nil {
		return ConnectOut{}, err
	}
	if _, err := s.o.DB.GetAppConnectionByName(ctx, in.Name); err == nil {
		return ConnectOut{}, ops.Conflict("已經有叫「" + in.Name + "」的連接了")
	}
	if call.DryRun {
		return ConnectOut{Connection: Connection{Name: in.Name, Title: cmpOr(in.Title, in.Name), Custom: true, Transport: in.Transport}}, nil
	}
	conns, err := s.o.DB.ListAppConnections(ctx)
	if err != nil {
		return ConnectOut{}, ops.Internal(err)
	}
	if len(conns) >= limits.AppConnectionsMax {
		return ConnectOut{}, ops.Limit(fmt.Sprintf("最多 %d 個連接", limits.AppConnectionsMax))
	}
	a := store.AppConnection{AppID: CustomApp, Name: in.Name, Title: cmpOr(strings.TrimSpace(in.Title), in.Name), Transport: string(in.Transport),
		Auth: string(in.Auth), AllBots: len(in.BotIDs) == 0, Enabled: !in.Disabled, Status: StatusChecking, CreatedBy: call.Identity.Actor()}
	if in.Transport == TransportHTTP {
		a.URL = strings.TrimSpace(in.URL)
	} else {
		a.Command, a.Args, a.Auth = strings.TrimSpace(in.Command), in.Args, string(AuthNone)
	}
	if a, err = s.o.DB.CreateAppConnection(ctx, a); err != nil {
		return ConnectOut{}, ops.Internal(err)
	}
	if len(in.BotIDs) > 0 {
		if err := s.setGrants(ctx, a.ID, in.BotIDs); err != nil {
			return ConnectOut{}, err
		}
	}
	sec := secret{}
	s.applySecret(&a, &sec, in.Token, in.TokenHeader, in.Headers, in.Env, in.Transport)
	if a.Secret, err = s.seal(a.ID, sec); err != nil {
		return ConnectOut{}, ops.Internal(err)
	}
	if a, err = s.o.DB.UpdateAppConnection(ctx, a); err != nil {
		return ConnectOut{}, ops.Internal(err)
	}
	if !a.Enabled {
		_ = s.o.DB.SetAppStatus(ctx, a.ID, StatusDisabled, "")
		c, err := s.get(ctx, a.ID)
		return ConnectOut{Connection: c}, err
	}
	return s.afterSave(ctx, a, []string{}, in.RedirectBase, in.ClientID, in.ClientSecret, "")
}

// applySecret 把 Token、標頭、環境變數放進祕密，並更新畫面看得到的名字與末四碼。
// headers／env 是 nil 表示不改；Token 空白表示不改。
func (s *Service) applySecret(a *store.AppConnection, sec *secret, token, tokenHeader string, headers, env map[string]string, transport Transport) {
	if t := strings.TrimSpace(token); t != "" {
		sec.APIKey = t
		a.SecretHint = hint(t)
	}
	if tokenHeader != "" {
		sec.TokenHeader = tokenHeader
	}
	if headers != nil {
		sec.Headers = map[string]string{}
		maps.Copy(sec.Headers, headers)
		a.HeaderNames = slices.Sorted(maps.Keys(headers))
	}
	if env != nil {
		if transport == TransportStdio || Transport(a.Transport) == TransportStdio {
			old := sec.Env
			sec.Env = map[string]string{}
			for k, v := range env {
				if v == "" && old[k] != "" {
					v = old[k] // 畫面只送名字（值是空的）時保留原本的值
				}
				sec.Env[k] = v
			}
			a.EnvNames = slices.Sorted(maps.Keys(env))
		}
	}
}

// UpdateIn 是 xbot.apps.update 的輸入：有給的欄位才改（I02 進階、I04 編輯器）。
type UpdateIn struct {
	ID            string            `json:"id" required:"true" max:"64"`
	Title         *string           `json:"title,omitempty" min:"1" max:"80"`
	Enabled       *bool             `json:"enabled,omitempty"`
	DisabledTools []string          `json:"disabled_tools,omitempty" max:"500" desc:"關掉的工具（整份換掉；給空陣列表示全開）"`
	ResetTools    bool              `json:"reset_tools,omitempty" desc:"工具全開（清掉 disabled_tools）"`
	URL           *string           `json:"url,omitempty" max:"2000" desc:"自訂遠端網址"`
	Command       *string           `json:"command,omitempty" max:"500"`
	Args          []string          `json:"args,omitempty" max:"64"`
	Token         string            `json:"token,omitempty" max:"4000" secret:"true" desc:"換新的 Token；空白不改"`
	TokenHeader   string            `json:"token_header,omitempty" max:"100"`
	Headers       map[string]string `json:"headers,omitempty" secret:"true" desc:"整份換掉其他標頭；不給不改"`
	Env           map[string]string `json:"env,omitempty" secret:"true" desc:"整份換掉環境變數；值空白保留原本的值"`
}

func (s *Service) update(ctx context.Context, call *ops.Call, in UpdateIn) (Connection, error) {
	a, err := s.load(ctx, in.ID)
	if err != nil {
		return Connection{}, err
	}
	custom := a.AppID == CustomApp
	if !custom && (in.URL != nil || in.Command != nil || in.Args != nil || in.Headers != nil || in.Env != nil) {
		return Connection{}, ops.InvalidInput(ops.FieldError{Field: "url", Message: "目錄裡的應用程式不能改網址、指令、標頭或環境變數"})
	}
	if in.URL != nil && !webURL(*in.URL) {
		return Connection{}, ops.InvalidInput(ops.FieldError{Field: "url", Message: "要是 http:// 或 https:// 開頭的網址"})
	}
	if in.TokenHeader != "" && !headerName.MatchString(in.TokenHeader) {
		return Connection{}, ops.InvalidInput(ops.FieldError{Field: "token_header", Message: "標頭名稱不對"})
	}
	if in.Env != nil {
		for k := range in.Env {
			if !envName.MatchString(k) {
				return Connection{}, ops.InvalidInput(ops.FieldError{Field: "env", Message: "環境變數名稱不對：" + k})
			}
		}
	}
	if call.DryRun {
		return s.get(ctx, a.ID)
	}
	retest := false
	if in.Title != nil {
		a.Title = strings.TrimSpace(*in.Title)
	}
	if in.Enabled != nil && *in.Enabled != a.Enabled {
		a.Enabled = *in.Enabled
		retest = true
	}
	if in.ResetTools {
		a.DisabledTools = []string{}
	} else if in.DisabledTools != nil {
		names := map[string]bool{}
		for _, t := range in.DisabledTools {
			if t = strings.TrimSpace(t); t != "" {
				names[t] = true
			}
		}
		a.DisabledTools = slices.Sorted(maps.Keys(names))
	}
	if in.URL != nil && *in.URL != a.URL {
		a.URL, retest = strings.TrimSpace(*in.URL), true
	}
	if in.Command != nil {
		a.Command, retest = strings.TrimSpace(*in.Command), true
	}
	if in.Args != nil {
		a.Args, retest = in.Args, true
	}
	if in.Token != "" || in.TokenHeader != "" || in.Headers != nil || in.Env != nil {
		sec, err := s.open(a.ID, a.Secret)
		if err != nil {
			return Connection{}, ops.Internal(err)
		}
		s.applySecret(&a, &sec, in.Token, in.TokenHeader, in.Headers, in.Env, Transport(a.Transport))
		if a.Secret, err = s.seal(a.ID, sec); err != nil {
			return Connection{}, ops.Internal(err)
		}
		retest = true
	}
	if !a.Enabled {
		a.Status = StatusDisabled
	} else if a.Status == StatusDisabled {
		a.Status = StatusChecking
	}
	if a, err = s.o.DB.UpdateAppConnection(ctx, a); err != nil {
		return Connection{}, ops.Internal(err)
	}
	if retest && a.Enabled {
		if Transport(a.Transport) == TransportHTTP && (AuthKind(a.Auth) != AuthOAuth2 || a.Status == StatusOK || a.Status == StatusChecking) {
			if _, err := s.test(ctx, a.ID); err != nil {
				return Connection{}, err
			}
		} else if Transport(a.Transport) == TransportStdio {
			_ = s.o.DB.SetAppStatus(ctx, a.ID, s.stdioStatus(a), s.stdioNote(a))
		}
	}
	cur, err := s.load(ctx, a.ID)
	if err != nil {
		return Connection{}, err
	}
	s.changed(ctx, cur, nil)
	return s.get(ctx, a.ID)
}

// ── 中斷 ─────────────────────────────────────────────

func (s *Service) disconnect(ctx context.Context, call *ops.Call, id string) (Connection, error) {
	a, err := s.load(ctx, id)
	if err != nil {
		return Connection{}, err
	}
	c, err := s.view(ctx, s.o.DB.Queries, a)
	if err != nil {
		return c, ops.Internal(err)
	}
	if call.DryRun {
		return c, nil
	}
	before := c.BotIDs
	s.mu.Lock()
	for fid, f := range s.flows {
		if f.connectionID == a.ID {
			delete(s.flows, fid)
		}
	}
	s.mu.Unlock()
	s.sessions.Range(func(k, _ any) bool {
		if strings.HasSuffix(k.(string), "\x00"+a.ID) {
			s.sessions.Delete(k)
		}
		return true
	})
	if err := s.o.DB.DeleteAppConnection(ctx, a.ID); err != nil {
		return c, ops.Internal(err)
	}
	if a.AllBots {
		s.changed(ctx, a, nil)
	} else {
		s.o.OnChangeOr(ctx, before)
	}
	c.Status = "removed"
	return c, nil
}

// OnChangeOr 通知指定的 Bot（連接已經刪掉，不能再從資料庫查授權）。
func (o Options) OnChangeOr(ctx context.Context, bots []string) {
	if o.OnChange != nil {
		o.OnChange(context.WithoutCancel(ctx), bots)
	}
}

// ── 授權 ─────────────────────────────────────────────

func (s *Service) setGrants(ctx context.Context, id string, botIDs []string) error {
	for _, b := range botIDs {
		if _, err := s.o.DB.GetBot(ctx, b); errors.Is(err, store.ErrNotFound) {
			return ops.NotFound("找不到這個 Bot：" + b)
		} else if err != nil {
			return ops.Internal(err)
		}
	}
	if err := s.o.DB.SetAppGrants(ctx, id, botIDs); err != nil {
		return ops.Internal(err)
	}
	return nil
}

// GrantIn 是 xbot.apps.grant_to_bot 的輸入。
type GrantIn struct {
	ID      string `json:"id" required:"true" max:"64" desc:"連接 id"`
	BotID   string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"授權給這個 Bot"`
	AllBots bool   `json:"all_bots,omitempty" desc:"改成所有 Bot 都能用"`
}

// RevokeIn 是 xbot.apps.revoke_from_bot 的輸入。
type RevokeIn struct {
	ID    string `json:"id" required:"true" max:"64" desc:"連接 id"`
	BotID string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"不再給這個 Bot 用；原本是所有 Bot 時，改成「其他 Bot 都能用」"`
	All   bool   `json:"all,omitempty" desc:"改成只有指定的 Bot（目前沒有任何 Bot）"`
}

func (s *Service) grant(ctx context.Context, call *ops.Call, in GrantIn) (Connection, error) {
	if in.BotID == "" && !in.AllBots {
		return Connection{}, ops.InvalidInput(ops.FieldError{Field: "bot_id", Message: "要給 bot_id 或 all_bots"})
	}
	a, err := s.load(ctx, in.ID)
	if err != nil {
		return Connection{}, err
	}
	before, err := s.o.DB.AppGrants(ctx, a.ID)
	if err != nil {
		return Connection{}, ops.Internal(err)
	}
	if call.DryRun {
		return s.get(ctx, a.ID)
	}
	switch {
	case in.AllBots:
		a.AllBots = true
		if err := s.o.DB.SetAppGrants(ctx, a.ID, nil); err != nil {
			return Connection{}, ops.Internal(err)
		}
	case !a.AllBots:
		if err := s.setGrants(ctx, a.ID, append(slices.Clone(before), in.BotID)); err != nil {
			return Connection{}, err
		}
	}
	if a, err = s.o.DB.UpdateAppConnection(ctx, a); err != nil {
		return Connection{}, ops.Internal(err)
	}
	if a.AllBots {
		s.changed(ctx, a, nil)
	} else {
		s.changed(ctx, a, before)
	}
	if cur, err := s.load(ctx, a.ID); err == nil {
		s.closeSigninCards(ctx, call, cur)
	}
	return s.get(ctx, a.ID)
}

func (s *Service) revoke(ctx context.Context, call *ops.Call, in RevokeIn) (Connection, error) {
	if in.BotID == "" && !in.All {
		return Connection{}, ops.InvalidInput(ops.FieldError{Field: "bot_id", Message: "要給 bot_id 或 all"})
	}
	a, err := s.load(ctx, in.ID)
	if err != nil {
		return Connection{}, err
	}
	if call.DryRun {
		return s.get(ctx, a.ID)
	}
	var before []string
	if a.AllBots {
		bots, err := s.o.DB.ListBots(ctx, true)
		if err != nil {
			return Connection{}, ops.Internal(err)
		}
		var keep []string
		for _, b := range bots {
			before = append(before, b.ID)
			if !in.All && b.ID != in.BotID {
				keep = append(keep, b.ID)
			}
		}
		a.AllBots = false
		if err := s.o.DB.SetAppGrants(ctx, a.ID, keep); err != nil {
			return Connection{}, ops.Internal(err)
		}
	} else {
		if before, err = s.o.DB.AppGrants(ctx, a.ID); err != nil {
			return Connection{}, ops.Internal(err)
		}
		var keep []string
		for _, b := range before {
			if !in.All && b != in.BotID {
				keep = append(keep, b)
			}
		}
		if err := s.o.DB.SetAppGrants(ctx, a.ID, keep); err != nil {
			return Connection{}, ops.Internal(err)
		}
	}
	if a, err = s.o.DB.UpdateAppConnection(ctx, a); err != nil {
		return Connection{}, ops.Internal(err)
	}
	s.sessions.Delete(in.BotID + "\x00" + a.ID)
	s.changed(ctx, a, before)
	return s.get(ctx, a.ID)
}
