package ops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"

	"xbot/internal/store"
)

// Version 是整組操作的版本（MCP-3）。改不相容就出 xbot.v2，舊版至少保留一個大版本。
const Version = "xbot.v1"

var nameRE = regexp.MustCompile(`^xbot\.[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

// Op 定義一個操作（TECH-DESIGN §3）。In、Out 都要是 struct；它們的 JSON Schema 由欄位與標籤產生（見 Schema）。
type Op[In, Out any] struct {
	Name        string // xbot.<領域>.<動作>，例：xbot.bots.create
	Summary     string // 一句白話中文，給 MCP 工具說明、CLI 說明、待我處理卡片用
	Description string // 補充說明（可空白）
	Scope       Scope  // 需要的權限範圍
	Risk        Risk   // 預設風險；RiskOf 有設時以它為準
	// Category 是代為核准的類別（MCP-10，見 Categories），例：external_send；空白表示不能代為核准。
	Category string
	// NoDelegate 表示永遠不能代為核准（刪除 Bot 或資料、移除主機、提高花費上限、修改代為核准授權）。
	// 和 Category 同時設定時以 NoDelegate 為準。
	NoDelegate bool
	// ForBot 表示 Bot 自己（經 product.sock 的 XBot MCP）可以用這個操作（C11）。Bot 只看得到、也只能呼叫這些。
	ForBot bool
	// RiskOf 依輸入決定風險（例：降低花費上限＝safe、提高＝confirm）。可空白。
	RiskOf func(In) Risk
	// AmountOf 回傳這次動作的金額（美元百萬分之一），花錢類的動作才設；代為核准用它算每日金額上限。
	AmountOf func(In) int64
	// Revert 復原一次已經執行的動作（代為核准的紀錄卡 10 分鐘內按「復原」時呼叫）。
	// 沒設表示這個動作做了就不能復原（例：已經寄出的信）。
	Revert func(ctx context.Context, call *Call, in In, out Out) error
	// Precheck 在送出核准卡之前先檢查（confirm 級的操作才用得到）：權限不夠、對象不存在時直接回錯誤，
	// 不要讓使用者白按一次允許。可空白；允許之後 Handle 照樣會再檢查一次。
	Precheck func(ctx context.Context, call *Call, in In) error
	// Explain 寫在核准卡上的說明：這次具體要做什麼（例：要刪的是哪幾個 Bot）。空白用預設「要允許嗎？」。
	// 在 Precheck 通過之後、送出核准卡之前呼叫。
	Explain func(ctx context.Context, call *Call, in In) (string, error)
	// Handle 執行操作。call.DryRun 為 true 時只能讀、回傳「會發生什麼」；這時 call.DB 是唯讀的。
	Handle func(ctx context.Context, call *Call, in In) (Out, error)
}

// Info 是一個操作對外公開的描述：HTTP 路由、MCP 工具、CLI、前端型別都由它產生。
type Info struct {
	Name        string  `json:"name"`
	Domain      string  `json:"domain"` // 例：bots
	Action      string  `json:"action"` // 例：create
	Summary     string  `json:"summary"`
	Description string  `json:"description,omitempty"`
	Scope       Scope   `json:"scope"`
	Risk        Risk    `json:"risk"` // 預設風險；DynamicRisk 為 true 時依輸入可能變成 confirm
	DynamicRisk bool    `json:"dynamic_risk,omitempty"`
	Category    string  `json:"category,omitempty"`
	NoDelegate  bool    `json:"no_delegate,omitempty"`
	Reversible  bool    `json:"reversible,omitempty"` // 有 Revert：代為核准後 10 分鐘內可以復原
	HasAmount   bool    `json:"has_amount,omitempty"` // 有 AmountOf：花錢類，算進代為核准的每日金額
	ForBot      bool    `json:"for_bot,omitempty"`    // Bot 經 product.sock 可以用（C11）
	Mutates     bool    `json:"mutates"`              // 不是 read：出口應該提供 request_id 與 dry_run（MCP-4）
	Input       *Schema `json:"input_schema"`
	Output      *Schema `json:"output_schema"`
}

// Call 是一次呼叫的內容，交給 Handle。
type Call struct {
	Op         *Info
	Identity   Identity
	RequestID  string
	DryRun     bool
	ApprovalID string    // 核准後才執行時，是那個核准的 id
	DB         *store.DB // DryRun 時是唯讀的
}

// Options 是呼叫時的附加選項（出口從請求取出）。
type Options struct {
	RequestID string // 會改東西的操作應該帶（MCP-4）；同一個身分＋request_id 只會做一次
	DryRun    bool   // 只回傳會發生什麼，不寫任何資料
}

// Result 是成功呼叫的結果。
type Result struct {
	Op     string          `json:"op"`
	Output json.RawMessage `json:"output"`
	// DryRun：這是預演；NeedsApproval：真的執行時會先送到「待我處理」。
	DryRun        bool `json:"dry_run,omitempty"`
	NeedsApproval bool `json:"needs_approval,omitempty"`
	Replayed      bool `json:"replayed,omitempty"` // 同一個 request_id 重送，回的是第一次的結果
}

// Decision 是風險檢查的結果。
type Decision int

const (
	Execute      Decision = iota // 直接執行
	NeedApproval                 // 建立核准與待我處理卡片，回 pending_approval
)

// ApprovalPolicy 決定 confirm 級的呼叫要直接執行還是等核准。
// 預設是 DefaultPolicy；之後代為核准（C21, MCP-10）換成檢查授權類別、次數、金額的版本。
type ApprovalPolicy func(ctx context.Context, call *Call) (Decision, error)

// DefaultPolicy：使用者本人直接執行（介面上按下去本身就是核准），其他身分一律等核准。
func DefaultPolicy(_ context.Context, call *Call) (Decision, error) {
	if call.Identity.Kind == KindOwner {
		return Execute, nil
	}
	return NeedApproval, nil
}

// Limiter 在執行前檢查呼叫者的上限（MCP-12：代理的每分鐘呼叫、每日呼叫、每日花費）。
// 回傳 nil 表示可以繼續；超過時回 limit 錯誤。
type Limiter func(ctx context.Context, id Identity, info *Info) *Error

// Config 是建立登錄表的設定。
type Config struct {
	DB      *store.DB
	Policy  ApprovalPolicy    // 空白用 DefaultPolicy
	Limiter Limiter           // 空白表示不限制
	Notify  func(store.Event) // 事件寫入後通知（SSE 用，C03）；可空白
	Logger  *slog.Logger      // 空白用 slog.Default()
	StaleMS int64             // 處理中多久可接手；0 用 limits.OpRequestStale
}

// Registry 是操作登錄表。
type Registry struct {
	cfg Config
	mu  sync.RWMutex
	ops map[string]*entry
}

type entry struct {
	info    Info
	run     func(ctx context.Context, call *Call, raw json.RawMessage) (json.RawMessage, error)
	risk    func(raw json.RawMessage) Risk
	amount  func(raw json.RawMessage) int64
	revert  func(ctx context.Context, call *Call, in, out json.RawMessage) error
	refs    func(raw json.RawMessage) []resourceRef
	check   func(ctx context.Context, call *Call, raw json.RawMessage) error
	explain func(ctx context.Context, call *Call, raw json.RawMessage) (string, error)
}

type resourceRef struct{ kind, id string }

// New 建立一個空的登錄表。
func New(cfg Config) *Registry {
	if cfg.Policy == nil {
		cfg.Policy = DefaultPolicy
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.StaleMS == 0 {
		cfg.StaleMS = defaultStaleMS
	}
	return &Registry{cfg: cfg, ops: map[string]*entry{}}
}

// SetPolicy 換掉風險檢查（C21 代為核准用）。
func (r *Registry) SetPolicy(p ApprovalPolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg.Policy = p
}

// SetLimiter 換掉上限檢查（MCP-12）。
func (r *Registry) SetLimiter(l Limiter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cfg.Limiter = l
}

// Register 登錄一個操作。定義有錯（名稱格式、重複、型別不支援）會 panic：這是程式錯誤，啟動時就該發現。
func Register[In, Out any](r *Registry, op Op[In, Out]) {
	if err := register(r, op); err != nil {
		panic(fmt.Sprintf("ops.Register %s：%v", op.Name, err))
	}
}

func register[In, Out any](r *Registry, op Op[In, Out]) error {
	if !nameRE.MatchString(op.Name) {
		return fmt.Errorf("名稱要是 xbot.<領域>.<動作>（小寫英文、數字、底線）")
	}
	if strings.TrimSpace(op.Summary) == "" {
		return fmt.Errorf("要有 Summary")
	}
	if !slices.Contains(Scopes, op.Scope) {
		return fmt.Errorf("Scope 不對：%q", op.Scope)
	}
	if op.Risk != RiskSafe && op.Risk != RiskConfirm {
		return fmt.Errorf("Risk 不對：%q", op.Risk)
	}
	if op.Handle == nil {
		return fmt.Errorf("要有 Handle")
	}
	if op.Category != "" && !slices.Contains(Categories, op.Category) {
		return fmt.Errorf("Category 不對：%q（可用：%s）", op.Category, strings.Join(Categories, "、"))
	}
	inT, outT := reflect.TypeFor[In](), reflect.TypeFor[Out]()
	if inT.Kind() != reflect.Struct || outT.Kind() != reflect.Struct {
		return fmt.Errorf("輸入與輸出都要是 struct")
	}
	in, err := schemaFor(inT)
	if err != nil {
		return fmt.Errorf("輸入：%w", err)
	}
	out, err := schemaFor(outT)
	if err != nil {
		return fmt.Errorf("輸出：%w", err)
	}
	parts := strings.Split(op.Name, ".")
	e := &entry{info: Info{
		Name: op.Name, Domain: parts[1], Action: parts[2], Summary: op.Summary, Description: op.Description,
		Scope: op.Scope, Risk: op.Risk, DynamicRisk: op.RiskOf != nil, Category: op.Category, NoDelegate: op.NoDelegate, ForBot: op.ForBot,
		Reversible: op.Revert != nil, HasAmount: op.AmountOf != nil, Mutates: op.Scope != ScopeRead, Input: in, Output: out,
	}}
	decode := func(raw json.RawMessage) (In, error) {
		var v In
		if len(strings.TrimSpace(string(raw))) == 0 {
			return v, nil
		}
		return v, json.Unmarshal(raw, &v)
	}
	e.run = func(ctx context.Context, call *Call, raw json.RawMessage) (json.RawMessage, error) {
		v, err := decode(raw)
		if err != nil {
			return nil, InvalidInput(FieldError{Message: "格式不對：" + err.Error()})
		}
		res, err := op.Handle(ctx, call, v)
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	}
	e.risk = func(raw json.RawMessage) Risk {
		if op.RiskOf == nil {
			return op.Risk
		}
		v, err := decode(raw)
		if err != nil {
			return RiskConfirm
		}
		if risk := op.RiskOf(v); risk == RiskSafe {
			return RiskSafe
		}
		return RiskConfirm // 不認得的值當成 confirm，寧可多問
	}
	e.refs = func(raw json.RawMessage) []resourceRef { return collectRefs(raw, in) }
	if op.Precheck != nil {
		e.check = func(ctx context.Context, call *Call, raw json.RawMessage) error {
			v, err := decode(raw)
			if err != nil {
				return InvalidInput(FieldError{Message: "格式不對：" + err.Error()})
			}
			return op.Precheck(ctx, call, v)
		}
	}
	if op.Explain != nil {
		e.explain = func(ctx context.Context, call *Call, raw json.RawMessage) (string, error) {
			v, err := decode(raw)
			if err != nil {
				return "", InvalidInput(FieldError{Message: "格式不對：" + err.Error()})
			}
			return op.Explain(ctx, call, v)
		}
	}
	e.amount = func(raw json.RawMessage) int64 {
		if op.AmountOf == nil {
			return 0
		}
		v, err := decode(raw)
		if err != nil {
			return 0
		}
		return max(op.AmountOf(v), 0)
	}
	if op.Revert != nil {
		e.revert = func(ctx context.Context, call *Call, inRaw, outRaw json.RawMessage) error {
			v, err := decode(inRaw)
			if err != nil {
				return err
			}
			var o Out
			if len(outRaw) > 0 {
				if err := json.Unmarshal(outRaw, &o); err != nil {
					return err
				}
			}
			return op.Revert(ctx, call, v, o)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.ops[op.Name]; dup {
		return fmt.Errorf("名稱重複")
	}
	r.ops[op.Name] = e
	return nil
}

// Amount 回傳一次呼叫的金額（美元百萬分之一；不是花錢類的動作是 0）。
func (r *Registry) Amount(name string, raw json.RawMessage) int64 {
	r.mu.RLock()
	e, ok := r.ops[name]
	r.mu.RUnlock()
	if !ok {
		return 0
	}
	return e.amount(raw)
}

// List 回傳所有操作，依名稱排序。
func (r *Registry) List() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Info, 0, len(r.ops))
	for _, name := range slices.Sorted(maps.Keys(r.ops)) {
		out = append(out, r.ops[name].info)
	}
	return out
}

// Get 依名稱取出一個操作的描述。
func (r *Registry) Get(name string) (Info, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.ops[name]
	if !ok {
		return Info{}, false
	}
	return e.info, true
}

// collectRefs 依 schema 的 x-resource 找出輸入裡提到的資源 id（含巢狀物件與陣列）。
func collectRefs(raw json.RawMessage, s *Schema) []resourceRef {
	v, err := decodeJSON(raw)
	if err != nil {
		return nil
	}
	var refs []resourceRef
	var walk func(v any, s *Schema)
	walk = func(v any, s *Schema) {
		if s == nil || v == nil {
			return
		}
		switch val := v.(type) {
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(val)) {
				walk(val[k], s.Properties[k])
			}
		case []any:
			for _, item := range val {
				if s.Resource != "" && s.Items != nil {
					if id, ok := item.(string); ok && id != "" {
						refs = append(refs, resourceRef{s.Resource, id})
					}
					continue
				}
				walk(item, s.Items)
			}
		case string:
			if s.Resource != "" && val != "" {
				refs = append(refs, resourceRef{s.Resource, val})
			}
		}
	}
	walk(v, s)
	return refs
}

func paramsHash(op string, raw json.RawMessage) string {
	// 先轉成標準形式（key 排序、去空白），同樣內容不同排版算同一個。
	canon := []byte(raw)
	if v, err := decodeJSON(raw); err == nil {
		if b, err := json.Marshal(v); err == nil {
			canon = b
		}
	}
	h := sha256.New()
	h.Write([]byte(op))
	h.Write([]byte{0})
	h.Write(canon)
	return hex.EncodeToString(h.Sum(nil))
}

// decodeJSON 解開任意 JSON；數字保留原文（json.Number），空白當成 {}。
func decodeJSON(raw json.RawMessage) (any, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("JSON 後面還有多餘的內容")
	}
	return v, nil
}
