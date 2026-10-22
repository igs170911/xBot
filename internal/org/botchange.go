package org

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"xbot/internal/bots"
	"xbot/internal/engine/acp"
	"xbot/internal/engine/hermes"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// 主管 Bot 換隊友的模型、引擎、思考強度、人設，或開關它的團隊管理權限（使用者回報 2026-10-19：主管想幫隊友「換模型」，
// 「update_bot 的參數裡沒有 can_manage」）。這些都要使用者本人按允許：模型、引擎、思考強度會改變花費；
// 人設會改變 Bot 的身分和行為（B.2）；團隊管理權限是放寬權限（能建 Bot、建團隊、調整別的 Bot）。
// 允許之後用你在網頁上改 Bot 的同一個操作（xbot.bots.update），和刪 Bot 一樣（remove.go）。
// 職稱、報告對象、團隊照舊直接改。

// needsOwner 回答這次調整有沒有要使用者按允許的欄位。
func (in UpdateBotIn) needsOwner() bool {
	// B.2: Persona 改為需要使用者核准（會改變 Bot 的行為和身分）
	return in.Engine != nil || in.Model != nil || in.Effort != nil || in.CanManage != nil || strings.TrimSpace(in.Persona) != ""
}

// safeUpdate 是不用核准、直接改的欄位。
func (in UpdateBotIn) safeUpdate() BotUpdate {
	// B.2: Persona 需要核准，不再直接改
	return BotUpdate{Title: in.Title, ReportsTo: in.ReportsTo, Team: in.Team}
}

func (u BotUpdate) empty() bool {
	return strings.TrimSpace(u.Title) == "" && strings.TrimSpace(u.Persona) == "" && strings.TrimSpace(u.ReportsTo) == "" && strings.TrimSpace(u.Team) == ""
}

// botTargetFor 檢查發起的是有團隊管理權限的 Bot、要調整的 Bot 找得到；送核准卡之前和允許之後都跑。
func botTargetFor(ctx context.Context, s *Service, call *ops.Call, ref string) (store.Bot, error) {
	if err := ready(s); err != nil {
		return store.Bot{}, err
	}
	if call.Identity.Kind != ops.KindBot {
		return store.Bot{}, ops.Forbidden("這是給有團隊管理權限的 Bot 用的；你本人請用 xbot.bots.update 與 xbot.teams.*")
	}
	if _, err := s.RequireManager(ctx, call.Identity.ID); err != nil {
		return store.Bot{}, err
	}
	r, err := s.roster(ctx)
	if err != nil {
		return store.Bot{}, err
	}
	return r.find(ref)
}

func engineLabel(e string) string {
	if a, ok := acp.Agents[e]; ok {
		return a.Label
	}
	if e == "" || e == string(bots.EngineHermes) {
		return "Hermes"
	}
	return e
}

var effortLabel = map[string]string{"": "中", "low": "低", "medium": "中", "high": "高"}

func onOff(on bool) string {
	if on {
		return "開"
	}
	return "關"
}

// ownerChanges 列出要使用者按允許的改動（核准卡上逐行寫出：從什麼改成什麼）。
func ownerChanges(b store.Bot, in UpdateBotIn) []string {
	var lines []string
	if in.Engine != nil {
		line := "引擎：" + engineLabel(b.Engine) + " → " + engineLabel(string(*in.Engine))
		if acp.IsACP(string(*in.Engine)) {
			line += "（用你的訂閱登入，還要你在「Bot 設定 → 進階」另外同意）"
		}
		lines = append(lines, line)
	}
	if in.Model != nil {
		model := func(m string) string {
			if strings.TrimSpace(m) == "" {
				return "預設模型"
			}
			return m
		}
		lines = append(lines, "模型："+model(b.Model)+" → "+model(*in.Model))
	}
	if in.Effort != nil {
		lines = append(lines, "思考強度："+effortLabel[b.Effort]+" → "+effortLabel[string(*in.Effort)])
	}
	if in.CanManage != nil {
		line := "團隊管理權限：" + onOff(b.CanManage) + " → " + onOff(*in.CanManage)
		if *in.CanManage {
			line += "（可以建 Bot、建團隊、調整其他 Bot 的設定、發起刪除）"
		}
		lines = append(lines, line)
	}
	return lines
}

// explainChange 是核准卡上的說明：要調整誰、每一項從什麼改成什麼、為什麼。
func explainChange(b store.Bot, in UpdateBotIn) string {
	lines := ownerChanges(b, in)
	if t := strings.TrimSpace(in.Title); t != "" {
		lines = append(lines, "職稱："+t)
	}
	if strings.TrimSpace(in.Persona) != "" {
		lines = append(lines, "人設：換成新的內容")
	}
	if r := strings.TrimSpace(in.ReportsTo); r != "" {
		lines = append(lines, "報告對象："+r)
	}
	if t := strings.TrimSpace(in.Team); t != "" {
		lines = append(lines, "團隊："+t)
	}
	text := "允許後會調整 " + label(b) + "：\n- " + strings.Join(lines, "\n- ")
	if r := strings.TrimSpace(in.Reason); r != "" {
		text += "\n\n原因：" + r
	}
	return text + "\n\n之後可以在「Bot 設定」改回來。"
}

// updateBot 執行 xbot.teams.update_bot：直接改的欄位用 Service.UpdateBot；要核准的欄位（到這裡代表使用者已經按了允許）
// 用你在網頁上改 Bot 的同一個操作 xbot.bots.update。
func updateBot(ctx context.Context, reg *ops.Registry, s *Service, call *ops.Call, in UpdateBotIn) (UpdateBotOut, error) {
	target, err := botTargetFor(ctx, s, call, in.Bot)
	if err != nil {
		return UpdateBotOut{}, err
	}
	safe := in.safeUpdate()
	if safe.empty() && !in.needsOwner() {
		return UpdateBotOut{}, bad("沒有要更新的欄位")
	}
	out := UpdateBotOut{Updated: target.Name, Fields: []string{}}
	if call.DryRun {
		return out, nil
	}
	if !safe.empty() {
		name, fields, err := s.UpdateBot(ctx, call.Identity.ID, target.ID, safe)
		if err != nil {
			return out, err
		}
		out.Updated, out.Fields = name, append(out.Fields, fields...)
	}
	if !in.needsOwner() {
		return out, nil
	}
	upd := map[string]any{"id": target.ID}
	var fields, labels []string
	if in.Engine != nil {
		upd["engine"], fields, labels = *in.Engine, append(fields, "engine"), append(labels, "引擎")
	}
	if in.Model != nil {
		upd["model"], fields, labels = strings.TrimSpace(*in.Model), append(fields, "model"), append(labels, "模型")
	}
	if in.Effort != nil {
		upd["effort"], fields, labels = *in.Effort, append(fields, "effort"), append(labels, "思考強度")
	}
	// B.2: Persona 改為需要核准（清理後再傳給 xbot.bots.update）
	if p := strings.TrimSpace(in.Persona); p != "" {
		// 使用 PersonaOnly 移除系統生成的規則
		clean := hermes.PersonaOnly(p)
		upd["soul"], fields, labels = clean, append(fields, "soul"), append(labels, "人設")
	}
	if in.CanManage != nil {
		upd["can_manage"], fields, labels = *in.CanManage, append(fields, "can_manage"), append(labels, "團隊管理權限")
	}
	raw, err := json.Marshal(upd)
	if err != nil {
		return out, err
	}
	if _, err := reg.Invoke(ctx, ops.Owner("approved"), "xbot.bots.update", raw, ops.Options{}); err != nil {
		return out, err
	}
	out.Fields = append(out.Fields, fields...)
	s.note(ctx, call.Identity.ID, "profile_updated", fmt.Sprintf("調整了 %s 的%s", target.Name, strings.Join(labels, "、")), map[string]any{"peer_id": target.ID})
	s.emit("", KindTeamUpdated, map[string]string{"team_id": target.TeamID, "bot_id": target.ID})
	return out, nil
}
