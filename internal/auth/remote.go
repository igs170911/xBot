package auth

import (
	"context"
	"net/http"
)

// 請求從哪裡進來（C35）：內建 Tailscale 的 tailnet 入口、或外部入口（Funnel／反向代理）。
// 標記放在 context 裡（不是 header），請求本身沒辦法偽造。

// ViaExternal 是經外部入口（Funnel／反向代理）呼叫 MCP 的身分來源；寫進稽核，也決定用比較嚴的速率上限。
const ViaExternal = "mcp-external"

type originKey struct{}

type origin struct {
	tailnet  bool
	external bool
	user     string // Tailscale 使用者（tailnet 入口才有；只給登入提示用，不代表已登入）
}

// WithTailnet 標記這個請求是經內建 Tailscale 的 tailnet 入口來的。user 是 Tailscale 的登入名稱（查不到可空白）。
// tailnet 來的請求一律要登入或權杖，永遠不算本機（IsLocal、IsLoopbackRequest 都回 false）。
func WithTailnet(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, originKey{}, origin{tailnet: true, user: user})
}

// WithExternal 標記這個請求是經外部入口（Funnel／反向代理）來的。
func WithExternal(ctx context.Context) context.Context {
	return context.WithValue(ctx, originKey{}, origin{external: true})
}

func originOf(ctx context.Context) origin {
	o, _ := ctx.Value(originKey{}).(origin)
	return o
}

// IsExternal 回答請求是不是經外部入口來的。
func IsExternal(ctx context.Context) bool { return originOf(ctx).external }

// IsTailnet 回答請求是不是經內建 Tailscale 的 tailnet 入口來的。
func IsTailnet(ctx context.Context) bool { return originOf(ctx).tailnet }

// TailnetUser 是 tailnet 入口查到的 Tailscale 使用者（登入畫面提示用）；不是 tailnet 來的回空白。
func TailnetUser(ctx context.Context) string { return originOf(ctx).user }

// remote 回答請求是不是被標記成從別的機器來（tailnet 或外部入口）。
func remote(r *http.Request) bool {
	o := originOf(r.Context())
	return o.tailnet || o.external
}
