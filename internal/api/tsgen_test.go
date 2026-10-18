package api

import (
	"context"
	"strings"
	"testing"

	"xbot/internal/ops"
)

type genNested struct {
	Color string `json:"color,omitempty" enum:"red,blue"`
}

type genIn struct {
	BotID  string            `json:"bot_id" required:"true" desc:"哪個 Bot" resource:"bot"`
	Tags   []string          `json:"tags,omitempty"`
	Opts   genNested         `json:"opts,omitzero"`
	Labels map[string]int    `json:"labels,omitempty"`
	Key    string            `json:"api_key,omitempty" secret:"true"`
	Scope  ops.Scope         `json:"scope,omitempty"`
	Extra  map[string]string `json:"x-extra,omitempty"`
}

type genOut struct {
	ID    string      `json:"id"`
	Items []genNested `json:"items"`
	Note  string      `json:"note,omitempty"`
}

func TestGenerateTS(t *testing.T) {
	reg := ops.New(ops.Config{})
	ops.Register(reg, ops.Op[genIn, genOut]{
		Name: "xbot.chat.list_threads", Summary: "列出對話串", Scope: ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(context.Context, *ops.Call, genIn) (genOut, error) { return genOut{}, nil },
	})
	src := string(GenerateTS(reg.List()))
	for _, want := range []string{
		"export interface ChatListThreadsInput {",
		"  /** 哪個 Bot */\n  bot_id: string\n",
		"  tags?: string[]\n",
		"  opts?: {\n    color?: 'red' | 'blue'\n  }\n",
		"  labels?: Record<string, number>\n",
		"（密鑰：只能寫、不能讀）",
		"  scope?: 'read' | 'operate' | 'manage' | 'admin'\n",
		"  'x-extra'?: Record<string, string>\n",
		// 輸出：沒有 omitempty 的欄位一定有。
		"export interface ChatListThreadsOutput {\n  id: string\n  items: {\n    color?: 'red' | 'blue'\n  }[]\n  note?: string\n}",
		// 有必填欄位時 input 不能省略。
		"export function chatListThreads(input: ChatListThreadsInput, opts?: CallOptions): Promise<ChatListThreadsOutput> {",
		"return invoke('xbot.chat.list_threads', input, opts)",
		"'xbot.chat.list_threads': { path: '/api/v1/chat.list_threads', scope: 'read', risk: 'safe', mutates: false, summary: '列出對話串' },",
		"export function subscribeEvents(",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("產生的 TypeScript 少了：\n%s\n\n全文：\n%s", want, src)
		}
	}
	if string(GenerateTS(reg.List())) != src {
		t.Fatal("同樣的清單要產生同樣的內容")
	}
}
