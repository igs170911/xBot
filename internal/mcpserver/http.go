package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"xbot/internal/auth"
	"xbot/internal/limits"
	"xbot/internal/ops"
)

// Path 是 Streamable HTTP 出口的路徑。
const Path = "/mcp"

// HTTPOptions 是 Streamable HTTP 出口的設定。
type HTTPOptions struct {
	Auth *auth.Authenticator // 權杖 → 身分（必填）
	// LocalOwner 為 true 時，從這台機器直接連進來、沒帶權杖的請求當成使用者本人（XBOT_MCP_LOCAL_OWNER=1）。
	// 預設關閉：同一台機器上的其他程式（包括代理）也算 localhost，應該要配對。
	LocalOwner bool
	Logger     *slog.Logger
}

// Handler 回傳掛在 /mcp 的 Streamable HTTP 處理器。每個 HTTP 請求都重新驗證權杖，所以撤銷立刻生效；
// 一條 MCP 連線（session）綁定建立它的身分，換權杖不能接手別人的連線。
func Handler(reg *ops.Registry, o HTTPOptions) http.Handler {
	if o.Auth == nil {
		panic("mcpserver.Handler：要有 Auth")
	}
	srv := NewServer(reg, TokenIdentity)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		SessionTimeout: limits.MCPSessionIdle,
		// SDK 的 DNS rebinding 保護會擋掉經 Tailscale serve／反向代理進來的請求（來源 127.0.0.1、Host 不是 localhost）。
		// 這裡每個請求都要權杖，免權杖的本機模式另外檢查 Host（auth.IsLoopbackRequest），所以關掉 SDK 的版本。
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        limits.MCPBodyBytes,
		Logger:                     o.Logger,
	})
	verify := func(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		id, err := o.Auth.Identify(ctx, token, "mcp")
		if errors.Is(err, auth.ErrInvalidToken) {
			return nil, fmt.Errorf("%w：權杖無效、已撤銷或已過期，請重新配對", sdkauth.ErrInvalidToken)
		}
		if err != nil {
			return nil, err
		}
		scopes := make([]string, len(id.Scopes))
		for i, s := range id.Scopes {
			scopes[i] = string(s)
		}
		return &sdkauth.TokenInfo{UserID: id.Actor(), Scopes: scopes, Extra: map[string]any{identityKey: id}}, nil
	}
	protected := sdkauth.RequireBearerToken(verify, &sdkauth.RequireBearerTokenOptions{AllowMissingExpiration: true})(mcpHandler)
	withLocal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o.LocalOwner && o.Auth.AccessToken != "" && r.Header.Get("Authorization") == "" && auth.IsLoopbackRequest(r) {
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+o.Auth.AccessToken)
		}
		protected.ServeHTTP(w, r)
	})
	// 擋掉瀏覽器的跨站請求（代理不是瀏覽器，不受影響）。
	return http.NewCrossOriginProtection().Handler(withLocal)
}
