package botops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"xbot/internal/limits"
	"xbot/internal/ops"
)

// Bot 自己說「這件事在等使用者」（2026-10-24）。原本完成判定從回覆裡找「登入、核准、要我…嗎」這類字猜，
// 會誤判（自我介紹提到登入），也會漏（「你想要幾人團隊？」一個關鍵字都沒有）。Bot 知道自己在不在等人：讓它明說，
// 系統照它說的開「需要你」卡、把選項變成可以點的按鈕；關鍵字只當它忘了說時的備案。

// WaitIn 是 xbot.chat.wait_for_user 的輸入。
type WaitIn struct {
	Ask     string   `json:"ask" required:"true" max:"300" desc:"一句話寫給使用者看：要他回答、決定或動手做什麼，例：要建 3 人版還是 5 人版？"`
	Choices []string `json:"choices,omitempty" max:"4" desc:"有幾條路讓他選時給 2～4 個選項（各 20 字內），他點了就當成他的回覆；要他自己寫或動手的不用給"`
}

// WaitOut 是 xbot.chat.wait_for_user 的輸出。
type WaitOut struct {
	Note string `json:"note"`
}

const (
	waitNote = "記下了：這一輪結束時會通知使用者，在待我處理開一張卡" +
		"。把同一件事寫在你的回覆裡（要他決定的放第一句），然後結束這一輪；他回覆後你會在這條對話接著做。"
	waitPeerNote = "這件事是隊友或群組交辦的，系統不會另外通知使用者：把要使用者做的事寫在回覆裡，交辦的隊友會轉告。"
)

func registerWait(reg *ops.Registry, d Deps) {
	ops.Register(reg, ops.Op[WaitIn, WaitOut]{
		Name: "xbot.chat.wait_for_user", Summary: "說這件事在等使用者回覆",
		Description: "這件事要等使用者本人回答、決定或動手（在幾條路裡選一條、提供只有他知道的資料、登入、註冊）才能往下做時，" +
			"結束這一輪前呼叫一次：系統會通知他，選項會變成他可以直接點的按鈕。" +
			"閒聊順口問的、事情已經做完只是問還要不要別的，不要用；使用者交代過的事不要用它問「要我做嗎」，直接做完。" +
			"花錢、對外發送、簽約這類要核准的用 xbot.approvals.request。",
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe, ForBot: true, NoDelegate: true,
		Handle: func(ctx context.Context, call *ops.Call, in WaitIn) (WaitOut, error) {
			return d.wait(ctx, call, in)
		},
	})
}

func (d Deps) wait(ctx context.Context, call *ops.Call, in WaitIn) (WaitOut, error) {
	botID, err := botOnly(call)
	if err != nil {
		return WaitOut{}, err
	}
	ask := strings.TrimSpace(in.Ask)
	if ask == "" {
		return WaitOut{}, ops.InvalidInput(ops.FieldError{Field: "ask", Message: "請寫出要使用者做什麼"})
	}
	var choices []string
	for i, c := range in.Choices {
		c = oneLine(c)
		if c == "" {
			continue
		}
		if utf8.RuneCountInString(c) > limits.WaitChoiceMax {
			return WaitOut{}, ops.InvalidInput(ops.FieldError{Field: fmt.Sprintf("choices[%d]", i), Message: fmt.Sprintf("選項最多 %d 個字", limits.WaitChoiceMax)})
		}
		choices = append(choices, c)
	}
	if len(choices) > limits.WaitChoicesMax {
		return WaitOut{}, ops.InvalidInput(ops.FieldError{Field: "choices", Message: fmt.Sprintf("最多 %d 個選項", limits.WaitChoicesMax)})
	}
	if d.Wait == nil {
		return WaitOut{}, ops.Internal(errors.New("botops: 沒有設定 Wait"))
	}
	if call.DryRun {
		return WaitOut{Note: "會在這一輪結束時通知使用者。"}, nil
	}
	peer, ok := d.Wait(ctx, botID, ask, choices)
	if !ok {
		return WaitOut{}, ops.Conflict("你現在沒有進行中的工作；直接在回覆裡問使用者")
	}
	if peer {
		return WaitOut{Note: waitPeerNote}, nil
	}
	return WaitOut{Note: waitNote}, nil
}
