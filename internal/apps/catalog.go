package apps

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// 應用程式目錄（I01、SK-6）：catalog.json 由 Hermes 的 optional-mcps（廠商自己架的遠端 MCP）加上 LazyBoy server/connectors.py
// 的中文名稱、說明與分類產生，另外補上 GitHub（API 金鑰）與 Slack（要你自己的 Slack App）。
// 每一項都是遠端網址（HTTP）的 MCP，一律由 XBot 代為轉送（權杖留在服務端，SEC-5）。

//go:embed catalog.json
var catalogJSON []byte

// App 是目錄裡的一個應用程式。
type App struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Site        string   `json:"site"`
	Icon        string   `json:"icon" desc:"Lucide 圖示名稱（中性圓形圖示，不放品牌標誌）"`
	Transport   string   `json:"transport"`
	URL         string   `json:"url"`
	Auth        AuthKind `json:"auth"`
	Exclude     []string `json:"exclude,omitempty" desc:"預設關掉的工具（Hermes manifest 的 default_excluded）"`
	TokenHeader string   `json:"token_header,omitempty"`
	TokenHint   string   `json:"token_hint,omitempty"`
	TokenURL    string   `json:"token_url,omitempty"`
	NeedsClient bool     `json:"needs_client,omitempty" desc:"授權伺服器不開放動態註冊：要你自己的 OAuth 用戶端 ID"`
	ClientHint  string   `json:"client_hint,omitempty"`
	Featured    bool     `json:"featured,omitempty"`
	Source      string   `json:"source,omitempty"`
}

type catalogFile struct {
	Categories []string `json:"categories"`
	Apps       []App    `json:"apps"`
}

var loadCatalog = sync.OnceValue(func() catalogFile {
	var c catalogFile
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		panic("apps/catalog.json 格式不對：" + err.Error())
	}
	return c
})

// Catalog 回傳目錄（依畫面順序：常用的在前）與分類。
func Catalog() ([]App, []string) {
	c := loadCatalog()
	return c.Apps, c.Categories
}

// Lookup 依 id 找目錄裡的應用程式。
func Lookup(id string) (App, bool) {
	for _, a := range loadCatalog().Apps {
		if a.ID == id {
			return a, true
		}
	}
	return App{}, false
}
