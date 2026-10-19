package runtime

// 服務重開時做到一半的工作（AA-10；LazyBoy 標成 interrupted、不重播）。
//
// 自動接著做（RecoverTasks → requeue）：你交辦的、隊友交辦的工作，同一件工作重新排隊（最多 limits.RestartResumeMax 次），
// 送給引擎的文字前面加一段「服務剛重開，接著做」的說明，對話裡記一行。隊友交辦的交辦紀錄與傳話都還指著這件工作，
// 做完照常把結果送回交辦人，不會先收到「做到一半被中斷」（2026-10-19：每次部署 Bot 之間的交辦都變成失敗）。
// 再被中斷、或不自動接著做的（群組的一輪、定時任務、Webhook）才結束成 interrupted，等它的 Bot 收到「做到一半被中斷」；
// 你按了停止、還沒停好服務就重開的照停止收尾（cancelled）。
//
// 對話裡被中斷的工作有「接著做」（ResumeInterrupted，xbot.chat.resume）：在同一條對話串開一件你交辦的新工作接著做。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"xbot/internal/limits"
	"xbot/internal/store"
)

// ActivityResume 是自動接著做那一行（kind=activity，sender=system）的 activity。
const ActivityResume = "restart_resume"

// ResumeText 是自動接著做時對話裡的那一行。
const ResumeText = "服務剛重開，接著做"

// ResumeAsk 是你按「接著做」時對話裡你的那一則。
const ResumeAsk = "接著做"

// resumeNote 放在接著做的那一輪前面（送給引擎的文字）。
const resumeNote = "（服務剛重開，你剛才這件工作做到一半被中斷了。電腦裡的檔案都還在：先看看已經做到哪裡，接著把它做完，" +
	"不要從頭重做已經完成的部分。原本的要求如下。）"

// resumeFromBot 接在隊友交辦的工作後面（你按「接著做」時）：交辦人已經收到沒有完成的通知，結果不會自動送回去。
const resumeFromBot = "\n\n（這件事原本是隊友交辦的，交辦人已經收到「沒有完成」的通知。做完後在這裡回報使用者；" +
	"交辦人還需要結果時，用 xbot.collab.message_bot 傳給它。）"

// 不能「接著做」的原因。
var (
	ErrNotInterrupted = errors.New("這件工作沒有做到一半被中斷，不用接著做")
	ErrResumed        = errors.New("這件工作已經接著做了")
)

// restartMeta 是接著做記在 task.meta 的欄位。
type restartMeta struct {
	Resumes   int    `json:"restart_resumes,omitempty"` // 服務重開後自動接著做了幾次
	ResumedBy string `json:"resumed_by,omitempty"`      // 你按「接著做」開的那件工作
}

// resumePrompt 是接著做的那一輪送給引擎的文字（再接著做時不會疊兩次說明）。
func resumePrompt(input string) string {
	return resumeNote + "\n\n" + strings.TrimPrefix(input, resumeNote+"\n\n")
}

// autoResumes：你的、隊友交辦的工作服務重開後自動接著做。群組的一輪下次輪到時會重讀群組；
// 定時任務、Webhook 由它們自己的紀錄收尾（routines.Reconcile 記成這次沒做完）。
func autoResumes(source string) bool { return source == SourceOwner || source == SourceBot }

// requeue 讓服務重開時做到一半的工作重新排隊、接著做。不能接著做時回 false（照舊結束）。
func (r *Runtime) requeue(ctx context.Context, task store.Task) bool {
	if task.State == store.TaskStopping || task.ThreadID == "" || !autoResumes(task.Source) {
		return false
	}
	var rm restartMeta
	_ = json.Unmarshal(task.Meta, &rm)
	if rm.Resumes >= limits.RestartResumeMax {
		return false
	}
	if b, err := r.o.DB.GetBot(ctx, task.BotID); err != nil || b.Archived {
		return false
	}
	if err := r.o.DB.RequeueTask(ctx, task.ID, resumePrompt(task.Input), map[string]any{"restart_resumes": rm.Resumes + 1}); err != nil {
		r.log.Warn("服務重開後接著做失敗", "task", task.ID, "err", err)
		return false
	}
	r.closeReply(ctx, task, "")
	meta, _ := json.Marshal(map[string]any{"task_id": task.ID, "activity": ActivityResume})
	if m, err := r.o.DB.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: task.ThreadID, Sender: "system", Kind: KindActivity,
		Content: ResumeText, Meta: meta}); err == nil {
		r.publish(task.BotID, task.ThreadID, task.ID, KindMessageCreated, m, true)
	}
	r.publish(task.BotID, task.ThreadID, task.ID, KindTaskState, map[string]string{"state": store.TaskQueued}, true)
	return true
}

// closeReply 收掉服務重開時還在寫的那則回覆，畫面上才不會一直顯示在寫。verdict 空白表示接著做了：只是不再串流，
// 下一則回覆接在後面。interrupted：這件工作就此結束（status=interrupted、restart；對話裡有「接著做」），
// 還沒有回覆時補一則空的放「接著做」。cancelled：照停止收尾。
func (r *Runtime) closeReply(ctx context.Context, task store.Task, verdict string) {
	restart := verdict == VerdictInterrupted && task.Source != SourceRoom
	m, err := r.o.DB.TaskReply(ctx, task.ThreadID, task.ID)
	if errors.Is(err, store.ErrNotFound) {
		if !restart {
			return
		}
		meta, _ := json.Marshal(map[string]any{"task_id": task.ID, "status": "interrupted", "restart": true})
		if m, err = r.o.DB.AddMessage(ctx, store.Message{Scope: store.ScopeThread, ScopeID: task.ThreadID, Sender: "bot:" + task.BotID,
			Meta: meta}); err == nil {
			r.publish(task.BotID, task.ThreadID, task.ID, KindMessageCreated, m, true)
		}
		return
	}
	if err != nil {
		return
	}
	meta := map[string]any{}
	_ = json.Unmarshal(m.Meta, &meta)
	if s, _ := meta["status"].(string); s != "" {
		return // 已經寫完的回覆（服務剛好在寫完之後、記下結束之前重開）
	}
	delete(meta, "streaming")
	switch {
	case verdict == "":
		meta["restart"] = true
	case restart:
		meta["status"], meta["restart"] = "interrupted", true
	default:
		meta["status"] = "interrupted"
	}
	m.Meta, _ = json.Marshal(meta)
	if err := r.o.DB.UpdateMessage(ctx, m.ID, m.Content, m.Attachments, m.Meta); err != nil {
		r.log.Warn("收掉被中斷的回覆失敗", "message", m.ID, "err", err)
		return
	}
	r.publish(task.BotID, task.ThreadID, task.ID, KindMessageCreated, m, true)
}

// ResumeInterrupted 是對話裡被中斷的工作按「接著做」（xbot.chat.resume）：在同一條對話串開一件你交辦的新工作接著做，排在 Bot
// 手上的工作後面。req 帶的是誰按的（Actor 等）；其餘欄位由這裡決定。群組的一輪不能這樣接著做（下次輪到時會重讀群組）。
func (r *Runtime) ResumeInterrupted(ctx context.Context, taskID string, req ChatRequest) (ChatResult, error) {
	r.resumeMu.Lock()
	defer r.resumeMu.Unlock()
	task, err := r.o.DB.GetTask(ctx, taskID)
	if err != nil {
		return ChatResult{}, err
	}
	if task.State != store.TaskDone || task.Verdict != VerdictInterrupted || task.ThreadID == "" || task.Source == SourceRoom {
		return ChatResult{}, ErrNotInterrupted
	}
	var rm restartMeta
	_ = json.Unmarshal(task.Meta, &rm)
	if rm.ResumedBy != "" {
		return ChatResult{}, ErrResumed
	}
	prompt := resumePrompt(task.Input)
	if task.Source == SourceBot {
		prompt += resumeFromBot
	}
	meta, _ := json.Marshal(map[string]any{"resume_of": task.ID})
	req.BotID, req.ThreadID, req.Text, req.Prompt, req.Source, req.TaskMeta, req.Queue = task.BotID, task.ThreadID, ResumeAsk, prompt, SourceOwner, meta, true
	req.Sender, req.Kind, req.LowPriority, req.Title = "", "", false, ""
	res, err := r.Start(ctx, req)
	if err != nil {
		return res, err
	}
	ctx = context.WithoutCancel(ctx)
	if err := r.o.DB.PatchTaskMeta(ctx, task.ID, map[string]any{"resumed_by": res.TaskID}); err != nil {
		r.log.Warn("記下接著做失敗", "task", task.ID, "err", err)
	}
	// 被中斷的那則回覆不再放「接著做」。
	if m, err := r.o.DB.TaskReply(ctx, task.ThreadID, task.ID); err == nil {
		fields := map[string]any{}
		_ = json.Unmarshal(m.Meta, &fields)
		fields["resumed"] = true
		m.Meta, _ = json.Marshal(fields)
		if err := r.o.DB.UpdateMessage(ctx, m.ID, m.Content, m.Attachments, m.Meta); err == nil {
			r.publish(task.BotID, task.ThreadID, task.ID, KindMessageCreated, m, true)
		}
	}
	return res, nil
}
