package apps

import (
	"context"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"xbot/internal/ops"
)

// CallbackHandler 是 GET /api/v1/apps/oauth/callback：授權伺服器把你的瀏覽器導回這裡。
// 不需要登入（state 是一次性、15 分鐘內有效的憑證），完成後顯示一頁「可以關掉這個分頁」，原本的畫面會自己更新。
func (s *Service) CallbackHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f, err := s.finish(r.Context(), q.Get("state"), q.Get("code"), q.Get("error"), q.Get("error_description"))
		title, msg, ok := "已連接", "可以關掉這個分頁，回到 XBot。", true
		if err != nil {
			title, msg, ok = "登入沒有完成", err.Error(), false
		} else if f != nil {
			if a, err := s.o.DB.GetAppConnection(r.Context(), f.connectionID); err == nil {
				title = "已連接 " + a.Title
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
		}
		_ = callbackPage.Execute(w, map[string]any{"Title": title, "Message": msg, "OK": ok})
	})
}

var callbackPage = template.Must(template.New("cb").Parse(`<!doctype html>
<html lang="zh-Hant-TW"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}} · XBot</title>
<style>body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#111;color:#fcfcfc;
font-family:Huninn,'PingFang TC','Noto Sans TC',sans-serif}main{max-width:420px;padding:24px;text-align:center}
h1{font-size:20px;font-weight:400;margin:0 0 8px}p{margin:0;color:#fcfcfc99;font-size:14px;line-height:1.6}
.ok{color:#3ecf8e}.bad{color:#ff6b6b}a{color:#7fc0ff}</style></head>
<body><main><h1 class="{{if .OK}}ok{{else}}bad{{end}}">{{.Title}}</h1><p>{{.Message}}</p><p style="margin-top:16px"><a href="/apps">回到連接應用程式</a></p></main>
{{if .OK}}<script>setTimeout(function(){try{window.close()}catch(e){}},1200)</script>{{end}}</body></html>`))

// finishURL 用貼上的網址（登入後停住、打不開的那一頁）完成登入（I03 備案）。
func (s *Service) finishURL(ctx context.Context, raw string) (SigninResult, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.RawQuery == "" {
		return SigninResult{}, ops.InvalidInput(ops.FieldError{Field: "url", Message: "請貼上登入後網址列的完整網址（裡面要有 code= 和 state=）"})
	}
	q := u.Query()
	if q.Get("state") == "" || (q.Get("code") == "" && q.Get("error") == "") {
		return SigninResult{}, ops.InvalidInput(ops.FieldError{Field: "url", Message: "請貼上登入後網址列的完整網址（裡面要有 code= 和 state=）"})
	}
	f, err := s.finish(ctx, q.Get("state"), q.Get("code"), q.Get("error"), q.Get("error_description"))
	if f == nil {
		return SigninResult{Status: "gone", Error: err.Error()}, nil
	}
	return s.signinStatus(ctx, f.id)
}
