package api

import (
	"net/http"
	"testing"

	"xbot/internal/ops"
)

// B14：畫面的 WebSocket 路由也在 guard 後面：沒登入（非本機）不能連；登入後把身分交給處理器；跨站的連線擋掉。
func TestVNCRouteGuarded(t *testing.T) {
	e := newEnv(t)
	var got ops.Identity
	e.h = New(Options{Registry: e.reg, Bus: e.bus, Auth: e.auth, DB: e.db, VNC: func(w http.ResponseWriter, r *http.Request, id ops.Identity) {
		got = id
		w.WriteHeader(http.StatusTeapot)
	}})
	const path = "/api/v1/computers/vnc?bot=b1"
	if rec := e.do(req{method: http.MethodGet, path: path}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("沒登入：%d", rec.Code)
	}
	rec := e.do(req{method: http.MethodGet, path: path, header: map[string]string{"Authorization": "Bearer " + testToken}})
	if rec.Code != http.StatusTeapot || got.Kind != ops.KindOwner {
		t.Fatalf("登入後：%d %+v", rec.Code, got)
	}
	rec = e.do(req{method: http.MethodGet, path: path, local: true, header: map[string]string{"Origin": "https://other.example"}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("跨站：%d", rec.Code)
	}
}
