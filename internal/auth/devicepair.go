package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"rsc.io/qr"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// DevicePairPath 是手機用配對碼登入的端點（不需要登入；配對碼本身就是一次性的憑證）。
const DevicePairPath = "/api/v1/auth/pair"

// 配對碼的狀態（xbot.devices.pair_status）。
const (
	DevicePending = "pending" // 還沒用
	DevicePaired  = "paired"  // 手機已經用它登入
	DeviceExpired = "expired" // 過期、或根本沒有這個碼
)

type deviceCode struct {
	expires time.Time
	paired  bool
}

// DevicePairing 管理手機登入用的配對碼（MB-2、TS-2、§17；畫板 A08、L09）：
// 電腦上登入的你按「連手機」→ 產生一次性配對碼，放進 QR 的網址 → 手機打開網址就用它換一個登入 session。
// 一次性、5 分鐘有效、換錯太多次會鎖住（和代理配對同一套數字）。只放在記憶體：服務重開就失效。
type DevicePairing struct {
	DB *store.DB
	// PublicURL 是手機連得到的網址（XBOT_PUBLIC_URL，例：https://xbot.tail3a9c.ts.net）；空白時用瀏覽器傳來的 origin。
	PublicURL string
	Now       func() time.Time // 測試用；空白用 time.Now

	mu    sync.Mutex
	codes map[string]*deviceCode
	fails failTracker
}

// NewDevicePairing 建立手機配對管理。
func NewDevicePairing(db *store.DB, publicURL string) *DevicePairing {
	return &DevicePairing{DB: db, PublicURL: strings.TrimRight(publicURL, "/")}
}

func (p *DevicePairing) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *DevicePairing) sweepLocked(now time.Time) {
	for c, dc := range p.codes {
		if !now.Before(dc.expires) {
			delete(p.codes, c)
		}
	}
	p.fails.sweep(now)
}

// Start 產生一個配對碼（顯示成 XXXX-XXXX）。
func (p *DevicePairing) Start() (code string, expires time.Time, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	p.sweepLocked(now)
	pending := 0
	for _, dc := range p.codes {
		if !dc.paired {
			pending++
		}
	}
	if pending >= limits.PairMaxPending {
		return "", time.Time{}, ops.Limit(fmt.Sprintf("同時最多 %d 個還沒用的配對碼，請等舊的過期後再試", limits.PairMaxPending))
	}
	if p.codes == nil {
		p.codes = map[string]*deviceCode{}
	}
	for {
		code = randomCode()
		if _, dup := p.codes[code]; !dup {
			break
		}
	}
	expires = now.Add(limits.PairCodeTTL)
	p.codes[code] = &deviceCode{expires: expires}
	return FormatPairCode(code), expires, nil
}

// Status 回傳配對碼目前的狀態：pending、paired 或 expired。
func (p *DevicePairing) Status(code string) string {
	code = NormalizePairCode(code)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweepLocked(p.now())
	dc, ok := p.codes[code]
	switch {
	case !ok:
		return DeviceExpired
	case dc.paired:
		return DevicePaired
	}
	return DevicePending
}

// Redeem 用配對碼換登入（一次性）。source 是來源（IP），用來計算換錯的次數。
func (p *DevicePairing) Redeem(ctx context.Context, code, source string) *ops.Error {
	code = NormalizePairCode(code)
	p.mu.Lock()
	now := p.now()
	p.sweepLocked(now)
	if p.fails.locked(now, source) {
		p.mu.Unlock()
		e := ops.Limit("換錯太多次，請一分鐘後再試")
		e.Retryable = true
		return e
	}
	dc, ok := p.codes[code]
	if !ok || dc.paired {
		if p.fails == nil {
			p.fails = failTracker{}
		}
		p.fails.fail(now, source)
		p.mu.Unlock()
		return ops.Forbidden("配對碼不對或已過期，請在電腦上重新產生一個")
	}
	dc.paired = true // 一次性：留著到過期，讓電腦那邊看得到「已連上」
	delete(p.fails, source)
	p.mu.Unlock()

	if p.DB != nil {
		params, _ := json.Marshal(map[string]any{"source": source})
		if _, err := p.DB.AddAudit(ctx, store.AuditEntry{Actor: "owner", Via: "http", Op: "xbot.devices.pair_complete",
			Params: params, Status: "ok"}); err != nil {
			return ops.Internal(err)
		}
	}
	return nil
}

// LinkFor 是手機要打開的網址：<base>/pair#<配對碼>（放在 # 後面，不會進伺服器紀錄）。
// base 用 PublicURL；沒設時用瀏覽器傳來的 origin。localOnly 表示網址是 localhost，手機連不到。
func (p *DevicePairing) LinkFor(origin, code string) (link string, localOnly bool, err error) {
	base := p.PublicURL
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(origin), "/")
	}
	if base == "" {
		return "", false, ops.InvalidInput(ops.FieldError{Field: "origin", Message: "要給目前網頁的網址（例：https://xbot.example.ts.net），或在主機設定 XBOT_PUBLIC_URL"})
	}
	u, perr := url.Parse(base)
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", false, ops.InvalidInput(ops.FieldError{Field: "origin", Message: "網址格式不對，要像 https://主機名稱[:埠]"})
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	localOnly = host == "localhost" || strings.HasSuffix(host, ".localhost") || (ip != nil && ip.IsLoopback())
	return u.Scheme + "://" + u.Host + "/pair#" + NormalizePairCode(code), localOnly, nil
}

// QRPath 把文字編成 QR（含四格白邊），回傳邊長（格數）和黑格的 SVG path（每列連續的黑格合併成一段）。
// 前端直接畫 <svg viewBox="0 0 size size"><path d=…/></svg>，不用 innerHTML。
func QRPath(text string) (size int, path string, err error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return 0, "", err
	}
	const quiet = 4
	var b strings.Builder
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; {
			if !c.Black(x, y) {
				x++
				continue
			}
			start := x
			for x < c.Size && c.Black(x, y) {
				x++
			}
			b.WriteString("M" + strconv.Itoa(start+quiet) + " " + strconv.Itoa(y+quiet) + "h" + strconv.Itoa(x-start) + "v1h-" + strconv.Itoa(x-start) + "z")
		}
	}
	return c.Size + 2*quiet, b.String(), nil
}

// RegisterDeviceOps 登錄手機配對的操作：xbot.devices.pair_start、xbot.devices.pair_status。
// 換登入（pair_complete）不是操作：手機那時還沒登入，走 POST /api/v1/auth/pair（DevicePairing.Handler）。
func RegisterDeviceOps(r *ops.Registry, p *DevicePairing) {
	ops.Register(r, ops.Op[DevicePairStartIn, DevicePairStartOut]{
		Name:    "xbot.devices.pair_start",
		Summary: "產生連手機用的 QR（一次性配對碼）",
		Description: "手機掃 QR 打開網址就登入，不用輸入登入碼。配對碼一次性、5 分鐘內有效，換錯太多次會暫時鎖住。" +
			"網址用主機的 XBOT_PUBLIC_URL（建議是 Tailscale 的 https://xbot.<tailnet>.ts.net），沒設時用 origin。只有你本人能產生。",
		Scope:      ops.ScopeAdmin,
		Risk:       ops.RiskSafe,
		NoDelegate: true,
		Handle: func(_ context.Context, call *ops.Call, in DevicePairStartIn) (DevicePairStartOut, error) {
			if err := ownerOnly(call, "產生連手機的配對碼"); err != nil {
				return DevicePairStartOut{}, err
			}
			if _, _, err := p.LinkFor(in.Origin, "XXXXXXXX"); err != nil {
				return DevicePairStartOut{}, err
			}
			if call.DryRun {
				return DevicePairStartOut{ExpiresAt: p.now().Add(limits.PairCodeTTL).UnixMilli()}, nil
			}
			code, exp, err := p.Start()
			if err != nil {
				return DevicePairStartOut{}, err
			}
			link, local, err := p.LinkFor(in.Origin, code)
			if err != nil {
				return DevicePairStartOut{}, err
			}
			size, path, err := QRPath(link)
			if err != nil {
				return DevicePairStartOut{}, ops.Internal(err)
			}
			return DevicePairStartOut{Code: code, URL: link, ExpiresAt: exp.UnixMilli(), LocalOnly: local, QRSize: size, QRPath: path}, nil
		},
	})
	ops.Register(r, ops.Op[DevicePairStatusIn, DevicePairStatusOut]{
		Name:       "xbot.devices.pair_status",
		Summary:    "看連手機的配對碼用掉了沒",
		Scope:      ops.ScopeAdmin,
		Risk:       ops.RiskSafe,
		NoDelegate: true,
		Handle: func(_ context.Context, call *ops.Call, in DevicePairStatusIn) (DevicePairStatusOut, error) {
			if err := ownerOnly(call, "查看連手機的配對碼"); err != nil {
				return DevicePairStatusOut{}, err
			}
			return DevicePairStatusOut{Status: p.Status(in.Code)}, nil
		},
	})
}

// DevicePairStartIn 是 xbot.devices.pair_start 的輸入。
type DevicePairStartIn struct {
	Origin string `json:"origin,omitempty" max:"300" desc:"目前網頁的網址（window.location.origin）；主機有設 XBOT_PUBLIC_URL 時用那個"`
}

// DevicePairStartOut 是 xbot.devices.pair_start 的輸出。
type DevicePairStartOut struct {
	Code      string `json:"code,omitempty" desc:"一次性配對碼（XXXX-XXXX）；也可以在手機的登入頁手動輸入。預演時是空白"`
	URL       string `json:"url,omitempty" desc:"手機要打開的網址（QR 的內容）"`
	ExpiresAt int64  `json:"expires_at" desc:"到期時間（Unix 毫秒）"`
	LocalOnly bool   `json:"local_only" desc:"網址是 localhost：手機連不到，要先設定 Tailscale 或 XBOT_PUBLIC_URL"`
	QRSize    int    `json:"qr_size" desc:"QR 邊長（格數，含白邊）"`
	QRPath    string `json:"qr_path" desc:"QR 黑格的 SVG path（viewBox 0 0 qr_size qr_size）"`
}

// DevicePairStatusIn 是 xbot.devices.pair_status 的輸入。
type DevicePairStatusIn struct {
	Code string `json:"code" required:"true" max:"20" desc:"pair_start 拿到的配對碼"`
}

// DevicePairStatusOut 是 xbot.devices.pair_status 的輸出。
type DevicePairStatusOut struct {
	Status string `json:"status" enum:"pending,paired,expired" desc:"pending：還沒用；paired：手機已經登入；expired：過期或沒有這個碼"`
}

// RedeemHTTP 處理 POST /api/v1/auth/pair 的內容：讀 {code}，換成功後由 issue 發登入 cookie。
// 成功回 200 {ok:true}；失敗回 MCP-5 格式的錯誤（403 碼不對、429 鎖住）。
func (p *DevicePairing) RedeemHTTP(w http.ResponseWriter, r *http.Request, issue func(http.ResponseWriter, *http.Request)) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, ops.InvalidInput(ops.FieldError{Message: "內容要是 JSON：{\"code\": \"配對碼\"}"}))
		return
	}
	if strings.TrimSpace(req.Code) == "" || len(req.Code) > 40 {
		WriteError(w, http.StatusBadRequest, ops.InvalidInput(ops.FieldError{Field: "code", Message: "請輸入配對碼"}))
		return
	}
	if e := p.Redeem(r.Context(), req.Code, ClientKey(r)); e != nil {
		WriteError(w, StatusFor(e), e)
		return
	}
	issue(w, r)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(`{"ok":true}` + "\n"))
}
