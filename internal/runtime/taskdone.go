package runtime

// X04 任務完成判定（PRD §19、§20 用途 2）：一輪結束時判斷做完了沒（judge），記在任務與回覆上、放進 chat.done；
// 只說要做、把工作丟回來、計畫沒做完、背景子任務失敗沒人接手的，叫 Bot 自己接著做（最多 limits.AutoContinueMax() 次，
// LazyBoy auto_continue）；要你本人的開一張「需要你」卡；丟回來又不能再續做的通知你。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"xbot/internal/judge"
	"xbot/internal/limits"
	"xbot/internal/notify"
	"xbot/internal/store"
)

// KindActivity 是系統在對話裡記的一行（sender=system）；meta.activity 說是哪一種。
// 自動續做的那一行是 activity=auto_continue，內容是「自動繼續：…」。
const (
	KindActivity     = "activity"
	ActivityContinue = "auto_continue"
)

// UseTaskDone 是「任務完成了沒」這個判斷用途（PRD §20.2 用途 2）；改正紀錄用它分類。
const UseTaskDone = "task_done"

// judgeRefPrefix 是判斷開的「需要你」卡的 ref 前綴（後面接工作 id）。
const judgeRefPrefix = "judge:"

// Judgment 是一件工作的完成判定：存在 task.meta.judge、回覆訊息的 meta.judge，也放在 chat.done 的 judge。
type Judgment struct {
	Verdict judge.Verdict `json:"verdict" desc:"done 已完成、needs_person 需要你、handed_back 交回給你、only_promised 只說要做、stuck 卡住了"`
	Label   string        `json:"label" desc:"介面上的白話，例：已完成"`
	Reason  string        `json:"reason" desc:"一行原因"`
	By      string        `json:"by" desc:"rules（規則）或 model（快判模型）"`
	// Confidence 是模型給的機率；規則判斷時沒有。
	Confidence float64 `json:"confidence,omitempty"`
	// ContinuedBy 是系統叫 Bot 自己接著做的那件工作（自動續做）。
	ContinuedBy string `json:"continued_by,omitempty" desc:"系統叫 Bot 自己接著做的那件工作 id"`
}

func judgmentOf(r judge.Result) Judgment {
	return Judgment{Verdict: r.Verdict, Label: r.Verdict.Label(), Reason: r.Reason, By: r.By, Confidence: r.Confidence}
}

// TaskJudgment 讀 task.meta 裡的判定；沒有時回 nil。
func TaskJudgment(t store.Task) *Judgment {
	var m struct {
		Judge *Judgment `json:"judge"`
	}
	if json.Unmarshal(t.Meta, &m) != nil || m.Judge == nil || m.Judge.Verdict == "" {
		return nil
	}
	return m.Judge
}

// autoMeta 是自動續做記在 task.meta 的欄位（LazyBoy auto_continue、continue_root）。
type autoMeta struct {
	Depth int    `json:"auto_continue,omitempty"`
	Root  string `json:"continue_root,omitempty"`
}

// planLeft 從引擎的計畫（ACP plan 轉成的「計畫：」狀態，☐ 未做、▶ 進行中、☑ 完成）找出還沒做完的項目。
func planLeft(status string) ([]string, bool) {
	if !strings.HasPrefix(status, "計畫：") {
		return nil, false
	}
	left := []string{}
	for _, line := range strings.Split(status, "\n")[1:] {
		line = strings.TrimSpace(line)
		for _, mark := range []string{"☐ ", "▶ "} {
			if strings.HasPrefix(line, mark) {
				left = append(left, strings.TrimSpace(strings.TrimPrefix(line, mark)))
			}
		}
	}
	return left, true
}

func strandedKey(botID, goal string) string {
	if r := []rune(goal); len(r) > limits.StrandedGoalKey {
		goal = string(r[:limits.StrandedGoalKey])
	}
	return botID + "\x00" + goal
}

// judgeTurn 判斷這一輪做完了沒。停掉的、中斷的不判斷（回 nil）。判斷器出錯時退回規則。
func (r *Runtime) judgeTurn(t *turn, task store.Task, verdict, final string, stalled bool) (judge.Input, *judge.Result) {
	t.mu.Lock()
	in := judge.Input{Outcome: verdict, Text: final, UsedTools: t.usedTools, Stalled: stalled, PlanLeft: append([]string(nil), t.plan...),
		TaskID: task.ID, BotID: t.botID}
	t.mu.Unlock()
	if r.o.Stranded != nil && verdict == VerdictAnswer {
		goals, background := r.o.Stranded(context.Background(), task)
		in.Background = background
		r.stallMu.Lock()
		for _, g := range goals {
			if r.stallNudged[strandedKey(t.botID, g)] < limits.StallNudgesMax {
				in.Stranded = append(in.Stranded, g)
			}
		}
		r.stallMu.Unlock()
	}
	j := r.o.Judge
	if j == nil {
		j = judge.Rules{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), limits.JudgeTaskDoneWait)
	defer cancel()
	res, err := j.Judge(ctx, in)
	if errors.Is(err, judge.ErrSkip) {
		return in, nil
	}
	if err != nil || !res.Verdict.Valid() {
		if res, err = (judge.Rules{}).Judge(context.Background(), in); err != nil {
			return in, nil
		}
	}
	return in, &res
}

// continuation 決定要不要叫 Bot 自己接著做（LazyBoy auto_continue）。不續做時回 nil。
// 你的工作、定時任務、Webhook：只說要做、丟回來、計畫沒做完、背景子任務失敗都續做；要你本人的不續做。
// 隊友交辦的：只有「只說要做」才續做（Hooks.ContinueMeta 帶上原本的交辦，續做的那一輪的回答才是給隊友的答案）；
// 群組的一輪不續做。同一個請求最多續做 limits.AutoContinueMax() 次；有待核准的動作、你又傳了新訊息時不續做。
func (r *Runtime) continuation(t *turn, task store.Task, in judge.Input, res judge.Result) *ChatRequest {
	if !judge.Continues(in, res) {
		return nil
	}
	var am autoMeta
	_ = json.Unmarshal(task.Meta, &am)
	if am.Depth >= limits.AutoContinueMax() {
		return nil
	}
	meta := map[string]any{}
	switch task.Source {
	case SourceOwner, SourceRoutine, SourceWebhook:
	case SourceBot:
		h := r.hooks()
		if res.Verdict != judge.OnlyPromised || h.ContinueMeta == nil {
			return nil
		}
		raw, ok := h.ContinueMeta(task)
		if !ok {
			return nil
		}
		_ = json.Unmarshal(raw, &meta)
	default:
		return nil
	}
	ctx := context.Background()
	if open, err := r.o.DB.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, Kinds: []string{"approval"}, TaskID: task.ID}); err != nil || len(open) > 0 {
		return nil
	}
	// 你在這之間傳的訊息先處理，它可能改了計畫。
	tasks, err := r.o.DB.ListTasks(ctx, t.botID, true)
	if err != nil {
		return nil
	}
	for _, q := range tasks {
		if q.State == store.TaskQueued && !q.LowPriority {
			return nil
		}
	}
	root := am.Root
	if root == "" {
		root = task.ID
	}
	meta["auto_continue"], meta["continue_root"] = am.Depth+1, root
	raw, _ := json.Marshal(meta)
	n := judge.NudgeFor(in)
	if task.Source == SourceBot {
		name, _ := meta["from_name"].(string)
		n.Prompt += "\n（這件事是隊友 " + firstNonEmpty(name, "隊友") + " 交辦的；你這次的最後回答會自動送回給它，請直接寫給它看。）"
	}
	return &ChatRequest{BotID: t.botID, ThreadID: t.threadID, Text: n.Shown, Prompt: n.Prompt, Sender: "system", Kind: KindActivity,
		MessageMeta: map[string]any{"activity": ActivityContinue},
		Source:      task.Source, LowPriority: true, Queue: true, TaskMeta: raw}
}

// continueTurn 送出自動續做；成功時回新工作的 id，並把提醒過的背景子任務記一次。
func (r *Runtime) continueTurn(t *turn, in judge.Input, req *ChatRequest) string {
	res, err := r.Start(context.Background(), *req)
	if err != nil {
		r.log.Warn("自動續做失敗", "bot", t.botID, "task", t.taskID, "err", err)
		return ""
	}
	r.stallMu.Lock()
	for _, g := range in.Stranded {
		r.stallNudged[strandedKey(t.botID, g)]++
	}
	r.stallMu.Unlock()
	return res.TaskID
}

// askCard 開一張「需要你」卡（kind=question、在「要你決定」）：Bot 這一輪結束時在要你本人做事或把工作交回給你。
// 同一條對話串已經有一張還開著的就不再開（你在那條對話串回話時會收掉，見 closeJudgeCards）。
func (r *Runtime) askCard(t *turn, res judge.Result, title string) {
	ctx := context.Background()
	open, err := r.o.DB.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, Kinds: []string{"question"}, BotID: t.botID})
	if err != nil {
		return
	}
	for _, it := range open {
		if it.ThreadID == t.threadID && (strings.HasPrefix(it.Ref, judgeRefPrefix) || it.TaskID == t.taskID) {
			return
		}
	}
	raw, _ := json.Marshal(map[string]any{"bot_name": t.botName, "task_id": t.taskID, "verdict": res.Verdict, "reason": res.Reason,
		"input": clip(t.shownInput(), 120)})
	it := store.InboxItem{Kind: "question", BotID: t.botID, ThreadID: t.threadID, Ref: judgeRefPrefix + t.taskID, Title: title,
		Body: clip(firstNonEmpty(res.Ask, res.Reason), limits.JudgeAskMax), Payload: raw}
	created, err := r.o.DB.CreateInboxItem(ctx, it)
	if err != nil {
		r.log.Warn("寫入需要你卡失敗", "task", t.taskID, "err", err)
		return
	}
	r.emitStored(t, notify.Event(notify.KindCreated, created))
}

// closeJudgeCards 在你對那條對話串說話時，收掉判斷開的「需要你」卡（你已經在回了）。
func (r *Runtime) closeJudgeCards(ctx context.Context, botID, threadID string) {
	open, err := r.o.DB.ListInboxFiltered(ctx, store.InboxFilter{Status: store.InboxOpen, Kinds: []string{"question"}, BotID: botID})
	if err != nil {
		return
	}
	for _, it := range open {
		if it.ThreadID != threadID || !strings.HasPrefix(it.Ref, judgeRefPrefix) {
			continue
		}
		if err := r.o.DB.SettleInboxItem(ctx, it.ID, store.InboxResolved, ResolutionAnswered, "owner", nil); err != nil {
			continue
		}
		if cur, err := r.o.DB.GetInboxItem(ctx, it.ID); err == nil {
			r.publishStored(notify.Event(notify.KindUpdated, cur))
		}
	}
}

// unfinishedVerdict：沒做完的判斷（不含要你處理的需要你）。
func unfinishedVerdict(v judge.Verdict) bool {
	return v == judge.OnlyPromised || v == judge.HandedBack || v == judge.Stuck
}
