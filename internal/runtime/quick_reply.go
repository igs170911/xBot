package runtime

import (
	"context"
	"strings"

	"xbot/internal/store"
)

// quickReply 不開電腦直接回覆（閒聊、查進度）。
func (r *Runtime) quickReply(ctx context.Context, b store.Bot, threadID, userText, reply string) (ChatResult, error) {
	var th store.Thread
	var err error
	
	if threadID == "" {
		// 開新對話串
		t := title(userText)
		if th, err = r.o.DB.CreateThread(ctx, store.Thread{BotID: b.ID, Title: t}); err != nil {
			return ChatResult{}, err
		}
		threadID = th.ID
	} else {
		if th, err = r.o.DB.GetThread(ctx, threadID); err != nil {
			return ChatResult{}, err
		}
	}
	
	var userMsg, botMsg store.Message
	err = r.o.DB.Tx(ctx, func(q *store.Queries) error {
		// 使用者訊息
		var err error
		userMsg, err = q.AddMessage(ctx, store.Message{
			Scope: store.ScopeThread, ScopeID: threadID,
			Sender: "owner", Kind: "text", Content: userText,
		})
		if err != nil {
			return err
		}
		
		// Bot 回覆
		content := reply
		if content == "" {
			// 查進度：組裝進度資訊
			if p, err := r.Progress(ctx, b.ID, ""); err == nil {
				content = p.Text
			} else {
				content = "目前沒有進行中的工作。"
			}
		}
		
		botMsg, err = q.AddMessage(ctx, store.Message{
			Scope: store.ScopeThread, ScopeID: threadID,
			Sender: "bot:" + b.ID, Kind: "text", Content: content,
		})
		return err
	})
	if err != nil {
		return ChatResult{}, err
	}
	
	// 發布事件
	r.publish(b.ID, threadID, "", KindMessageCreated, userMsg, true)
	r.publish(b.ID, threadID, "", KindMessageCreated, botMsg, true)
	
	return ChatResult{ThreadID: threadID, MessageID: userMsg.ID}, nil
}

// sendQuickAck 發送即時確認訊息（「好的，我來處理」）。
func (r *Runtime) sendQuickAck(ctx context.Context, botID, threadID, ack string) error {
	msg, err := r.o.DB.AddMessage(ctx, store.Message{
		Scope: store.ScopeThread, ScopeID: threadID,
		Sender: "bot:" + botID, Kind: "text", Content: ack,
	})
	if err != nil {
		return err
	}
	r.publish(botID, threadID, "", KindMessageCreated, msg, true)
	return nil
}
