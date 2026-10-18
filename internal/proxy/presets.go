package proxy

import (
	_ "embed"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
)

// presets.json 由 LazyBoy server/hermes_providers.json 轉來（Hermes 認得的供應商：名稱、服務網址、呼叫方式、常用模型）。
// protocol 換成 Hermes 的 api_mode 名稱（responses → codex_responses、messages → anthropic_messages）；
// 沒有固定網址的（azure-foundry）略過；OpenCode 另外加上 session_header 與預設模型。
// 更新方式：python3 scripts/gen-model-presets.py <LazyBoy>/server/hermes_providers.json internal/proxy/presets.json
//
//go:embed presets.json
var presetsJSON []byte

// Preset 是內建的供應商範本：使用者選了它，只要再貼金鑰。
type Preset struct {
	ID        string   `json:"id"`
	Label     string   `json:"label"`
	BaseURL   string   `json:"base_url"`
	Transport string   `json:"transport"`
	SignupURL string   `json:"signup_url"`
	Local     bool     `json:"local"` // 本機模型（LM Studio 等），不需要金鑰
	Models    []string `json:"models"`
	// ModelTransports 是和供應商預設不同呼叫方式的模型（例：OpenCode 的 claude-* 走 messages）。
	ModelTransports map[string]string `json:"model_transports"`
	// SessionHeader 是供應商要求的對話識別標頭（OpenCode：x-opencode-session，沒有就回 MissingSessionID）。
	// Model Proxy 會帶上這次對話串的 id（同一段對話固定不變）。
	SessionHeader string `json:"session_header,omitempty"`
	// DefaultModel 是建議的預設模型（空白時用 Models 的第一個）。
	DefaultModel string `json:"default_model,omitempty"`
}

// SuggestedModel 回傳建議先用的模型。
func (p Preset) SuggestedModel() string {
	if p.DefaultModel != "" {
		return p.DefaultModel
	}
	if len(p.Models) > 0 {
		return p.Models[0]
	}
	return ""
}

// SessionHeaderFor 回傳這個供應商要帶的對話識別標頭；不需要時回空白。
// 範本有寫就用範本的；自訂的供應商指到 opencode.ai 也一樣要帶。
func SessionHeaderFor(p Provider) string {
	if pre, ok := PresetByID(p.Preset); ok && pre.SessionHeader != "" {
		return pre.SessionHeader
	}
	if u, err := url.Parse(p.BaseURL); err == nil {
		if h := strings.ToLower(u.Hostname()); h == "opencode.ai" || strings.HasSuffix(h, ".opencode.ai") {
			return "X-Opencode-Session"
		}
	}
	return ""
}

var presets = func() []Preset {
	var ps []Preset
	if err := json.Unmarshal(presetsJSON, &ps); err != nil {
		panic("presets.json 格式不對：" + err.Error())
	}
	return ps
}()

// Presets 回傳所有內建範本（複本）。
func Presets() []Preset {
	out := make([]Preset, len(presets))
	for i, p := range presets {
		p.Models = slices.Clone(p.Models)
		out[i] = p
	}
	return out
}

// PresetByID 找一個內建範本。
func PresetByID(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == id {
			p.Models = slices.Clone(p.Models)
			return p, true
		}
	}
	return Preset{}, false
}

// TransportFor 回傳這個範本裡某個模型的呼叫方式（沒有特別記載就用範本預設）。
func (p Preset) TransportFor(model string) string {
	if t, ok := p.ModelTransports[model]; ok {
		return t
	}
	return p.Transport
}
