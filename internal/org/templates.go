package org

import (
	"context"
	"strings"

	"xbot/internal/store"
)

// 團隊範本（TM-6、F02；LazyBoy org.py TEMPLATES，內容照抄）。

// Person 是範本裡的一位。
type Person struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Persona string `json:"persona"`
}

// Template 是一個團隊範本。
type Template struct {
	Key     string   `json:"key" desc:"engineering、company、marketing"`
	Name    string   `json:"name"`
	About   string   `json:"about"`
	Lead    Person   `json:"lead"`
	Members []Person `json:"members"`
	// LeadManage：主管拿到團隊管理權限（公司範本的執行長替你調度七個職能）。
	LeadManage bool `json:"lead_manage"`
}

// Templates 是所有範本，依畫面順序（F02 預設選行銷團隊）。
var Templates = []Template{
	{
		Key: "marketing", Name: "行銷團隊", About: "行銷主管帶內容、社群、廣告與數據分析。",
		Lead: Person{"行銷主管", "行銷團隊主管", "規劃行銷目標與檔期，分派內容、社群、廣告與分析工作，整合成效後回報。公開發佈與花費前先確認。"},
		Members: []Person{
			{"內容企劃", "內容企劃", "撰寫文案、腳本與部落格內容，維持品牌語氣。"},
			{"社群經營", "社群經營", "經營社群帳號：排程貼文、回覆互動、追蹤話題。"},
			{"廣告投放", "廣告投放", "規劃與優化廣告：受眾、素材、預算分配與成效追蹤。花費前要經核定。"},
			{"數據分析", "行銷數據分析", "整理流量、轉換與營收數據，找出有效與無效的做法。"},
		},
	},
	{
		Key: "engineering", Name: "工程團隊", About: "技術主管帶前端、後端、QA、DBA 與 DevOps，從需求到上線。",
		Lead: Person{"技術主管", "工程團隊主管", "帶領工程團隊：拆解需求、分派給前端、後端、QA、DBA 與 DevOps，追蹤進度與風險，整合成果後回報。技術決策要說明取捨。"},
		Members: []Person{
			{"前端工程師", "前端", "負責網頁與 App 介面：React/TypeScript、版面、互動、無障礙與效能。交付可執行的程式與截圖。"},
			{"後端工程師", "後端", "負責 API、商業邏輯、資料流程與整合，重視正確性、安全與可測試性。交付程式、API 文件與測試。"},
			{"QA 工程師", "品質測試", "獨立測試功能：寫測試案例、執行端對端與回歸測試、追蹤缺陷並附重現步驟與證據。"},
			{"DBA", "資料庫管理", "負責資料庫設計、遷移、索引與查詢效能、備份與資料安全。變更前先評估風險與回復方式。"},
			{"DevOps 工程師", "部署與維運", "負責建置、部署、CI/CD、監控與基礎設施，確保服務穩定並可回復。"},
		},
	},
	{
		Key: "company", Name: "公司組織", About: "AI 執行長統籌七個平行職能，重大事項由你核定。", LeadManage: true,
		Lead: Person{"執行長", "AI 執行長／總經理", "統籌公司運作：把董事長（使用者）的目標拆成工作，調度七個職能、追蹤進度，定期回報結果與風險。新商品定價、新增支出、簽約、上架與重要對客承諾一律送董事長核定。"},
		Members: []Person{
			{"營運管理", "營運管理", "進度追蹤、資源協調、任務交接，讓各職能的工作準時完成。"},
			{"市場研究", "市場研究與產品規劃", "需求研究、競品分析與產品驗證，提出有證據的產品方向。"},
			{"業務行銷", "業務開發與行銷", "找案、確認需求、提案與管道優化。對外承諾前先經執行長送董事長核定。"},
			{"軟體研發", "軟體研發與資訊技術", "原型製作、既定專案開發與技術維護。"},
			{"財務成本", "財務與成本控管", "成本控管、報價試算與收付款紀錄。任何新增支出或報價都要送董事長核定。"},
			{"創意總監", "創意總監與新產品研究", "研究全球 App 與遊戲商機，每輪最多提出 2 個新產品案。"},
			{"品質驗收", "品質驗收", "獨立測試、缺陷追蹤與交付證據；與其他職能同級，不替任何人放水。"},
		},
	},
}

// TemplateByKey 依 key 找範本。
func TemplateByKey(key string) (Template, bool) {
	for _, t := range Templates {
		if t.Key == key {
			return t, true
		}
	}
	return Template{}, false
}

// keywords 是一句話裡會讓範本被挑中的字（規則；快判 X05 之後接上）。
var keywords = map[string][]string{
	"engineering": {"工程", "開發", "程式", "軟體", "網站", "app", "前端", "後端", "上線", "系統", "技術"},
	"marketing":   {"行銷", "社群", "廣告", "內容", "品牌", "貼文", "粉絲", "文案", "曝光", "投放"},
	"company":     {"公司", "創業", "營運", "執行長", "生意", "商品", "財務", "業務", "老闆", "組織"},
}

// Suggest 依你的一句話挑一個範本（規則：看哪個範本的關鍵字最多；都沒有就用行銷團隊，F02 的預設）。
// matched=false 表示沒有任何關鍵字。
func Suggest(sentence string) (Template, bool) {
	text := strings.ToLower(sentence)
	best, score := Templates[0], 0
	for _, t := range Templates {
		n := 0
		for _, k := range keywords[t.Key] {
			n += strings.Count(text, k)
		}
		if n > score {
			best, score = t, n
		}
	}
	return best, score > 0
}

// Spec 把範本轉成要建立的團隊。
func (t Template) Spec(name string) TeamSpec {
	if strings.TrimSpace(name) == "" {
		name = t.Name
	}
	sp := TeamSpec{Name: name, About: t.About, Template: t.Key, GroupChat: true, LeadManage: t.LeadManage,
		Lead: Member{Name: t.Lead.Name, Title: t.Lead.Title, Persona: t.Lead.Persona}}
	for _, p := range t.Members {
		sp.Members = append(sp.Members, Member{Name: p.Name, Title: p.Title, Persona: p.Persona})
	}
	return sp
}

// ── 團隊的對外資料 ─────────────────────────────────────

// TeamView 是一個團隊。
type TeamView struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	About     string   `json:"about"`
	ChiefID   string   `json:"chief_id" desc:"主管 Bot"`
	MemberIDs []string `json:"member_ids" desc:"成員（主管在最前面），依建立順序"`
	Memory    []string `json:"memory" desc:"團隊記憶：整個團隊共用的事實"`
	RoomID    string   `json:"room_id" desc:"建立團隊時一起開的群組"`
	Template  string   `json:"template"`
	Position  int      `json:"position"`
	CreatedBy string   `json:"created_by"`
	CreatedAt int64    `json:"created_at"`
}

// View 組出團隊的對外資料。bots 是所有 Bot（找成員用）。
func View(t store.Team, all []store.Bot) TeamView {
	v := TeamView{ID: t.ID, Name: t.Name, About: t.About, ChiefID: t.ChiefID, MemberIDs: []string{}, Memory: t.Memory, RoomID: t.RoomID,
		Template: t.Template, Position: t.Position, CreatedBy: t.CreatedBy, CreatedAt: t.CreatedAt}
	if v.Memory == nil {
		v.Memory = []string{}
	}
	if t.ChiefID != "" {
		v.MemberIDs = append(v.MemberIDs, t.ChiefID)
	}
	for _, b := range all {
		if b.TeamID == t.ID && b.ID != t.ChiefID && !b.Archived {
			v.MemberIDs = append(v.MemberIDs, b.ID)
		}
	}
	return v
}

// Teams 列出所有團隊（側欄順序）。
func (s *Service) Teams(ctx context.Context) ([]TeamView, error) {
	ts, err := s.db.ListTeams(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.db.ListBots(ctx, false)
	if err != nil {
		return nil, err
	}
	out := []TeamView{}
	for _, t := range ts {
		out = append(out, View(t, all))
	}
	return out, nil
}

// Team 讀一個團隊。
func (s *Service) Team(ctx context.Context, id string) (TeamView, error) {
	t, err := s.db.GetTeam(ctx, id)
	if err != nil {
		return TeamView{}, err
	}
	all, err := s.db.ListBots(ctx, false)
	if err != nil {
		return TeamView{}, err
	}
	return View(t, all), nil
}
