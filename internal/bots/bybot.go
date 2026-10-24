package bots

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"xbot/internal/engine/hermes"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Bot 自己建立幫手 Bot、改自己的資料（X03、LP-9；LazyBoy collab.py create_bot、update_profile）。

// KindMessageCreated 是新訊息的事件種類（同 runtime.KindMessageCreated；不 import runtime，避免循環）。
const KindMessageCreated = "message.created"

// ProfileIn 是 xbot.bots.update_my_profile 的輸入：有給的欄位才改。
type ProfileIn struct {
	Title string `json:"title,omitempty" max:"100" desc:"新的職稱（60 字內）"`
	Color Color  `json:"color,omitempty" desc:"頭像顏色"`
	Shape Shape  `json:"shape,omitempty" desc:"頭像形狀"`
}

// ProfileOut 是 xbot.bots.update_my_profile 的輸出。
type ProfileOut struct {
	Updated []string `json:"updated" desc:"改了哪些欄位"`
}

// Clip 把空白壓成一個空格，超過 n 個字時截斷並加「…」（LazyBoy plain）。
func Clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// CheckQuota 檢查能不能再建 n 個 Bot（PRD §18、LP-9）：全部最多 50 個；creator 是 Bot 時，
// 它每 24 小時（滾動）最多建 3 個（有團隊管理權限 20 個）。creator 是 nil 表示你本人或代理。
func CheckQuota(ctx context.Context, db *store.DB, creator *store.Bot, n int) error {
	total, err := db.CountBots(ctx)
	if err != nil {
		return err
	}
	if total+n > limits.BotsMax() {
		return ops.Limit(fmt.Sprintf("Bot 最多 %d 個，目前已有 %d 個；請先整理（封存或刪除不用的 Bot）", limits.BotsMax(), total))
	}
	if creator == nil {
		return nil
	}
	made, err := db.CountBotsCreatedSince(ctx, "bot:"+creator.ID, store.NowMS()-limits.BotCreateWindow.Milliseconds())
	if err != nil {
		return err
	}
	limit := limits.BotCreatesPerDay()
	if creator.CanManage {
		limit = limits.ManagerBotCreatesPerDay()
	}
	if made+n > limit {
		if n == 1 && !creator.CanManage {
			return ops.Limit(fmt.Sprintf("你今天已經建立 %d 個 Bot；需要更多請使用者到「Bot 設定 → 權限與花費」開啟你的「團隊管理權限」（每天可建 %d 個），或到「設定 → 進階 → 限制」調高", made, limits.ManagerBotCreatesPerDay()))
		}
		if n == 1 {
			return ops.Limit(fmt.Sprintf("你今天已經建立 %d 個 Bot；需要更多請先問使用者", made))
		}
		return ops.Limit(fmt.Sprintf("每天最多建立 %d 個 Bot，今天已建立 %d 個", limit, made))
	}
	return nil
}

// Spec 是要新建的一個 Bot（建團隊、Bot 建幫手時用）。
type Spec struct {
	Name, Title, Persona string
	Color, Shape         string
	TeamID, ReportsTo    string
}

// Build 照 Spec 組出一個新 Bot（還沒存）：名字、職稱、人設截到 LazyBoy 的長度，顏色形狀沒給就輪流挑。
// existing 是現有 Bot 數（輪流挑顏色用）。
func Build(sp Spec, existing int, actor string) store.Bot {
	color, shape := sp.Color, sp.Shape
	if color == "" {
		color = Colors[existing%len(Colors)]
	}
	if shape == "" {
		shape = Shapes[existing%len(Shapes)]
	}
	av, _ := json.Marshal(avatar{Shape: shape})
	persona := hermes.PersonaOnly(sp.Persona)
	if r := []rune(persona); len(r) > limits.BotSoulMax {
		persona = string(r[:limits.BotSoulMax])
	}
	return store.Bot{Name: Clip(sp.Name, limits.BotCreateNameMax), Title: Clip(sp.Title, limits.BotProfileTitleMax),
		Description: Clip(persona, limits.BotDescriptionMax), Soul: persona, Color: color, Avatar: av, Engine: string(EngineHermes),
		ApprovalMode: string(DefaultMode), MemoryEnabled: true, TeamID: sp.TeamID, ReportsTo: sp.ReportsTo, CreatedBy: actor}
}

func (d Deps) createByBot(ctx context.Context, reg *ops.Registry, call *ops.Call, in CreateIn) (View, error) {
	creator, err := getBot(ctx, call.DB, call.Identity.ID)
	if err != nil {
		return View{}, err
	}
	if err := CheckQuota(ctx, call.DB, &creator, 1); err != nil {
		return View{}, err
	}
	name := Clip(in.Name, limits.BotCreateNameMax)
	if name == "" {
		return View{}, ops.InvalidInput(ops.FieldError{Field: "name", Message: "請提供新 Bot 的名字"})
	}
	if !ValidColor(string(in.Color)) {
		return View{}, colorErr()
	}
	all, err := call.DB.ListBots(ctx, true)
	if err != nil {
		return View{}, err
	}
	for _, b := range all {
		if b.Name == name {
			return View{}, ops.Conflict(fmt.Sprintf("已經有叫「%s」的 Bot，請直接用 xbot.collab.message_bot 聯絡它", name))
		}
	}
	persona := in.Soul
	if strings.TrimSpace(persona) == "" {
		persona = in.Description
	}
	b := Build(Spec{Name: name, Title: in.Title, Persona: persona, Color: string(in.Color), Shape: string(in.Shape), TeamID: creator.TeamID},
		len(all), call.Identity.Actor())
	if in.Description != "" {
		b.Description = Clip(in.Description, limits.BotDescriptionMax)
	}
	if in.ApprovalMode != "" {
		b.ApprovalMode = string(in.ApprovalMode) // 比預設寬時登錄表已經先要你核准
	}
	if in.Model != "" {
		b.Model = strings.TrimSpace(in.Model)
	}
	b.Effort = string(in.Effort)
	if call.DryRun {
		return ViewOf(b), nil
	}
	b, err = call.DB.CreateBot(ctx, b)
	if err != nil {
		return View{}, err
	}
	d.note(ctx, reg, call.DB, creator.ID, "bot_created", b.Name, map[string]any{"peer_id": b.ID, "peer_name": b.Name})
	return ViewOf(b), nil
}

func (d Deps) updateProfile(ctx context.Context, reg *ops.Registry, call *ops.Call, in ProfileIn) (ProfileOut, error) {
	if call.Identity.Kind != ops.KindBot {
		return ProfileOut{}, ops.Forbidden("這是給 Bot 改自己資料用的；你本人請用 xbot.bots.update")
	}
	b, err := getBot(ctx, call.DB, call.Identity.ID)
	if err != nil {
		return ProfileOut{}, err
	}
	out := ProfileOut{Updated: []string{}}
	labels := []string{}
	if t := Clip(in.Title, limits.BotProfileTitleMax); t != "" {
		b.Title = t
		out.Updated, labels = append(out.Updated, "title"), append(labels, "職稱")
	}
	if in.Color != "" {
		if !ValidColor(string(in.Color)) {
			return out, colorErr()
		}
		b.Color = string(in.Color)
		out.Updated, labels = append(out.Updated, "color"), append(labels, "頭像顏色")
	}
	if in.Shape != "" {
		a := map[string]any{}
		_ = json.Unmarshal(b.Avatar, &a)
		if a == nil {
			a = map[string]any{}
		}
		a["shape"] = string(in.Shape)
		b.Avatar, _ = json.Marshal(a)
		out.Updated, labels = append(out.Updated, "shape"), append(labels, "頭像形狀")
	}
	if len(out.Updated) == 0 {
		return out, ops.InvalidInput(ops.FieldError{Message: "沒有要更新的欄位"})
	}
	if call.DryRun {
		return out, nil
	}
	b.UpdatedBy = call.Identity.Actor()
	if _, err := call.DB.UpdateBot(ctx, b); err != nil {
		return out, err
	}
	d.note(ctx, reg, call.DB, b.ID, "profile_updated", "更新了"+strings.Join(labels, "、"), map[string]any{"fields": out.Updated})
	return out, nil
}

// note 在 Bot 正在進行的對話加一行事件（「建立了 Bot：X」「更新了職稱」；LazyBoy collab.note）。沒有進行中的對話就不寫。
func (d Deps) note(ctx context.Context, reg *ops.Registry, db *store.DB, botID, event, text string, extra map[string]any) {
	if d.Active == nil {
		return
	}
	thread, task := d.Active(botID)
	if thread == "" {
		return
	}
	peer := map[string]any{"event": event}
	for k, v := range extra {
		peer[k] = v
	}
	meta, _ := json.Marshal(map[string]any{"peer": peer, "task_id": task})
	var ev store.Event
	err := db.Tx(ctx, func(q *store.Queries) error {
		m, err := q.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: thread, Sender: "bot:" + botID, Kind: "peer", Content: text, Meta: meta})
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(m)
		ev, err = q.AppendEvent(ctx, store.Event{BotID: botID, ThreadID: thread, TaskID: task, Kind: KindMessageCreated, Payload: payload})
		return err
	})
	if err == nil {
		reg.Notify(ev)
	}
}
