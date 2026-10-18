package importer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"xbot/internal/bots"
	"xbot/internal/limits"
	"xbot/internal/proxy"
	"xbot/internal/routines"
	"xbot/internal/store"
)

// 預演項目的狀態。
const (
	StatusReady   = "ready"   // 可以帶
	StatusDone    = "done"    // 已經帶進來了（再匯入不會重複建立）
	StatusBlocked = "blocked" // 帶不過來（Detail 說明原因）
)

// Item 是預演清單的一項（F10）：一個 Bot，或 Bot 底下的技能、記憶、電腦資料、登入、定時任務，或團隊、群組、金鑰。
type Item struct {
	Key    string `json:"key" desc:"項目代號（run 的 items 用），例：bot:<舊 id>、skills:<舊 id>、key:openai"`
	Kind   string `json:"kind" desc:"bot、skills、memory、data、login、routine、team、room、key、approvals"`
	Bot    string `json:"bot,omitempty" desc:"屬於哪個 Bot 項目（技能、記憶、電腦資料、登入、定時任務）"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Status string `json:"status" desc:"ready：可以帶；done：已經帶進來了；blocked：帶不過來"`
	// Checked 是預設勾不勾：金鑰與瀏覽器登入要你明確勾選（預設不勾）。
	Checked bool `json:"checked"`
	// Explicit 表示要你明確同意才帶（金鑰、登入）。
	Explicit bool `json:"explicit,omitempty"`
	// Attention 是要你之後處理的事（橘色「要你處理」），例：技能先關閉、定時任務先暫停。
	Attention string `json:"attention,omitempty"`
	// Name 是 Bot 匯入後的名字（和現有的重複時自動加編號，可以改）；Original 是舊名字。
	Name     string `json:"name,omitempty"`
	Original string `json:"original,omitempty"`
	Renamed  bool   `json:"renamed,omitempty"`
	Count    int    `json:"count,omitempty" desc:"技能數、記憶條數、檔案數"`
	Bytes    int64  `json:"bytes,omitempty" desc:"電腦資料、登入的大小"`
	// Avatar 是 Bot 的頭像（預覽用）。
	Color string `json:"color,omitempty"`
	Shape string `json:"shape,omitempty"`
	Image string `json:"image,omitempty"`
	// Target 是已經帶進來時，在 XBot 的 id。
	Target string `json:"target,omitempty"`
}

// PlanOut 是 xbot.import.plan 的輸出。
type PlanOut struct {
	Source     Found    `json:"source"`
	Items      []Item   `json:"items"`
	NewBots    int      `json:"new_bots" desc:"全部照預設勾選時會建立幾個 Bot"`
	Remaining  int      `json:"remaining" desc:"還可以建立幾個 Bot（全部最多 50 個）"`
	CanCreate  bool     `json:"can_create"`
	Reason     string   `json:"reason,omitempty" desc:"不能全部建立的原因"`
	NotCarried []string `json:"not_carried" desc:"一律不帶的東西"`
	Notes      []string `json:"notes" desc:"讀舊資料時遇到的小問題"`
}

// plan 是預演的結果與 run 需要的對照。
type plan struct {
	out   PlanOut
	src   *Source
	m     *mapping
	items map[string]*Item
	bots  map[string]Bot
}

func botKey(id string) string { return ItemBot + ":" + id }

// uniqueName 和現有 Bot 同名時加編號：「小秘」→「小秘 2」（同 botshare）。
func uniqueName(name string, taken map[string]bool) (string, bool) {
	if !taken[name] {
		return name, false
	}
	for i := 2; ; i++ {
		n := clip(name, limits.BotNameMax-1-len(strconv.Itoa(i))) + " " + strconv.Itoa(i)
		if !taken[n] {
			return n, true
		}
	}
}

func human(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// treeSize 量一個資料夾要帶的檔案數與大小（略過的不算）。
func treeSize(t Tree) (files int, size int64) {
	_ = walkTree(t, func(rel, p string, info fs.FileInfo) error {
		files++
		size += info.Size()
		return nil
	})
	return files, size
}

// walkTree 走過要帶的每個一般檔案（連結、太大的檔案不帶）。
func walkTree(t Tree, fn func(rel, p string, info fs.FileInfo) error) error {
	return filepath.WalkDir(t.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == t.Dir {
				return err
			}
			return nil
		}
		if p == t.Dir {
			return nil
		}
		rel, _ := filepath.Rel(t.Dir, p)
		rel = filepath.ToSlash(rel)
		if t.Skip != nil && t.Skip(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > limits.ImportFileBytes {
			return nil
		}
		return fn(rel, p, info)
	})
}

// buildPlan 依舊資料與已經帶過的東西列出預演清單。renames 是 Bot 項目代號 → 想要的名字。
func (s *Service) buildPlan(ctx context.Context, db *store.DB, src *Source, found Found, renames map[string]string) (*plan, error) {
	m, err := loadMap(ctx, db, src.Kind)
	if err != nil {
		return nil, err
	}
	all, err := db.ListBots(ctx, true)
	if err != nil {
		return nil, err
	}
	taken := map[string]bool{}
	for _, b := range all {
		taken[b.Name] = true
	}
	p := &plan{src: src, m: m, items: map[string]*Item{}, bots: map[string]Bot{}}
	p.out = PlanOut{Source: found, Items: []Item{}, NotCarried: NotCarried, Notes: append([]string{}, src.Notes...),
		Remaining: max(0, limits.BotsMax-len(all)), CanCreate: true}
	add := func(it Item) {
		p.out.Items = append(p.out.Items, it)
	}
	ready := func(kind, sid string) (string, string) {
		if id, ok := m.done(ctx, db, kind, sid); ok {
			return StatusDone, id
		}
		return StatusReady, ""
	}
	for i, b := range src.Bots {
		p.bots[b.ID] = b
		st, target := StatusReady, ""
		if id, ok := m.bot(ctx, db, b.ID); ok {
			st, target = StatusDone, id
		}
		it := Item{Key: botKey(b.ID), Kind: ItemBot, Title: b.Name, Status: st, Checked: st == StatusReady, Target: target,
			Original: b.Name, Color: pick(b.Color, bots.Colors, bots.Colors[(len(all)+i)%len(bots.Colors)]), Shape: pick(b.Shape, bots.Shapes, "cursor"), Image: b.Image}
		var detail []string
		if b.Title != "" {
			detail = append(detail, b.Title)
		}
		if st == StatusReady {
			want := clip(b.Name, limits.BotNameMax)
			if n := strings.TrimSpace(renames[it.Key]); n != "" {
				want = clip(n, limits.BotNameMax)
			}
			it.Name, it.Renamed = uniqueName(want, taken)
			taken[it.Name] = true
			if it.Renamed {
				detail = append(detail, fmt.Sprintf("你已經有「%s」，先叫「%s」", want, it.Name))
			}
			p.out.NewBots++
			it.Attention = "權限從「先問我」開始"
		} else {
			it.Name = b.Name
			detail = append(detail, "已經帶進來了")
		}
		it.Detail = strings.Join(detail, "・")
		add(it)
		// Bot 底下的東西
		sub := func(kind string, title, detail string, count int, size int64, checked bool, attention string) {
			// 已經帶過的 Bot 也可以補帶還沒帶的東西（例：上次沒勾的瀏覽器登入）。
			st, target := ready(kind, b.ID)
			add(Item{Key: kind + ":" + b.ID, Kind: kind, Bot: botKey(b.ID), Title: title, Detail: detail, Status: st, Target: target,
				Checked: st == StatusReady && checked, Explicit: kind == ItemLogin, Count: count, Bytes: size, Attention: attention})
		}
		if n := len(b.Skills); n > 0 {
			var names []string
			for _, sk := range b.Skills {
				names = append(names, sk.Name)
			}
			sub(ItemSkills, fmt.Sprintf("技能 %d 個", n), clip(strings.Join(names, "、"), 120), n, 0, true, "先關閉；看過內容再打開")
		}
		if b.HasMemory() {
			n := len(b.Memory) + len(b.User)
			detail := fmt.Sprintf("%d 條", n)
			if len(b.MemoryFiles) > 0 {
				detail += fmt.Sprintf("，另有 %d 份記憶檔放進工作資料夾", len(b.MemoryFiles))
			}
			sub(ItemMemory, "記憶", detail, n, 0, true, "")
		}
		var files int
		var size int64
		var labels []string
		for _, t := range b.Data {
			n, sz := treeSize(t)
			if n > 0 {
				files += n
				size += sz
				labels = append(labels, t.Label)
			}
		}
		if files > 0 {
			sub(ItemData, "電腦裡的檔案", fmt.Sprintf("%s：%d 個檔案、%s", strings.Join(labels, "、"), files, human(size)), files, size, size <= limits.ImportDataBytes, "")
		}
		if b.Login != nil {
			if n, sz := treeSize(*b.Login); n > 0 {
				sub(ItemLogin, "瀏覽器登入", fmt.Sprintf("已登入的網站（%s）；要你勾選才帶", human(sz)), n, sz, false, "")
			}
		}
	}
	// 定時任務
	for _, r := range src.Routines {
		st, target := ready(ItemRoutine, r.ID)
		it := Item{Key: ItemRoutine + ":" + r.ID, Kind: ItemRoutine, Bot: botKey(r.BotID), Title: cmpOr(r.Title, routines.TitleOf(r.Prompt)), Status: st, Target: target}
		if st == StatusReady {
			if r.Problem != "" {
				it.Status, it.Detail = StatusBlocked, r.Problem
			} else if sc, err := routines.Normalize(r.Schedule, r.Jitter, s.now()); err != nil {
				it.Status, it.Detail = StatusBlocked, "排程帶不過來："+err.Error()
			} else {
				it.Detail = routines.Describe(sc)
				if r.Note != "" {
					it.Detail += "（" + r.Note + "）"
				}
				it.Checked = true
				it.Attention = "先暫停；你確認後再啟用"
			}
		}
		add(it)
	}
	// 團隊、群組
	name := func(id string) string {
		if b, ok := p.bots[id]; ok {
			return b.Name
		}
		return "?"
	}
	for _, t := range src.Teams {
		st, target := ready(ItemTeam, t.ID)
		var names []string
		for _, id := range t.Members {
			names = append(names, name(id))
		}
		it := Item{Key: ItemTeam + ":" + t.ID, Kind: ItemTeam, Title: t.Name, Status: st, Target: target, Count: len(t.Members),
			Detail: fmt.Sprintf("%d 位：%s", len(t.Members), strings.Join(names, "、")), Checked: st == StatusReady}
		if t.ChiefID != "" {
			it.Detail += "・主管 " + name(t.ChiefID)
		}
		if st == StatusReady && len(t.Members) == 0 {
			it.Status, it.Checked, it.Detail = StatusBlocked, false, "沒有成員"
		}
		add(it)
	}
	for _, r := range src.Rooms {
		st, target := ready(ItemRoom, r.ID)
		var names []string
		for _, id := range r.Members {
			names = append(names, name(id))
		}
		it := Item{Key: ItemRoom + ":" + r.ID, Kind: ItemRoom, Title: cmpOr(r.Name, strings.Join(names, "、")), Status: st, Target: target,
			Count: len(r.Members), Detail: strings.Join(names, "、"), Checked: st == StatusReady}
		if st == StatusReady && len(r.Members) < limits.RoomMembersMin {
			it.Status, it.Checked, it.Detail = StatusBlocked, false, fmt.Sprintf("群組至少要 %d 個 Bot", limits.RoomMembersMin)
		}
		add(it)
	}
	if len(src.ApprovalItems) > 0 && s.ApprovalItems != nil {
		st, target := ready(ItemApprovals, "org")
		add(Item{Key: ItemApprovals + ":org", Kind: ItemApprovals, Title: "只有你能決定的事", Detail: strings.Join(src.ApprovalItems, "、"),
			Status: st, Target: target, Checked: st == StatusReady, Count: len(src.ApprovalItems)})
	}
	// 金鑰：要你明確勾選；已經有同一個供應商的不覆寫。
	for _, k := range src.Keys {
		st, target := ready(ItemKey, k.ID)
		it := Item{Key: ItemKey + ":" + k.ID, Kind: ItemKey, Title: k.Label, Status: st, Target: target, Explicit: true,
			Detail: fmt.Sprintf("%s（%s）；金鑰加密後只放在 XBot 服務，不進 Bot 的電腦", k.ID, k.BaseURL)}
		if st == StatusReady && s.Providers != nil {
			if _, err := s.Providers.Get(ctx, k.ID); err == nil {
				it.Status, it.Detail = StatusBlocked, "你已經設定了 "+k.ID+"，不覆寫"
			} else if !errors.Is(err, proxy.ErrNotFound) {
				return nil, err
			}
		}
		if st == StatusReady && s.Providers == nil {
			it.Status, it.Detail = StatusBlocked, "這個服務沒有管理模型金鑰"
		}
		add(it)
	}
	for i := range p.out.Items {
		p.items[p.out.Items[i].Key] = &p.out.Items[i]
	}
	if p.out.NewBots > p.out.Remaining {
		p.out.CanCreate = false
		p.out.Reason = fmt.Sprintf("Bot 最多 %d 個，還可以建立 %d 個；請少勾幾個，或先封存、刪除不用的 Bot", limits.BotsMax, p.out.Remaining)
	}
	return p, nil
}

func pick(v string, all []string, def string) string {
	if slices.Contains(all, v) {
		return v
	}
	return def
}
