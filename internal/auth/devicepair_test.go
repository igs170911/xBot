package auth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"xbot/internal/limits"
	"xbot/internal/ops"
)

func newDevicePairing(t *testing.T, public string) (*DevicePairing, *clock) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	p := NewDevicePairing(openDB(t), public)
	p.Now = c.now
	return p, c
}

func TestDevicePairSingleUseAndExpiry(t *testing.T) {
	p, c := newDevicePairing(t, "")
	ctx := context.Background()
	code, exp, err := p.Start()
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 9 || code[4] != '-' || !exp.Equal(c.t.Add(limits.PairCodeTTL)) {
		t.Fatalf("配對碼 %q 到期 %v", code, exp)
	}
	if s := p.Status(code); s != DevicePending {
		t.Fatalf("狀態 %s", s)
	}
	if e := p.Redeem(ctx, code, "phone"); e != nil {
		t.Fatal(e)
	}
	if s := p.Status(code); s != DevicePaired {
		t.Fatalf("用過後 %s", s)
	}
	if e := p.Redeem(ctx, code, "phone"); e == nil || e.Code != ops.CodeForbidden {
		t.Fatalf("第二次應失敗：%v", e)
	}
	// 過期。
	code2, _, _ := p.Start()
	c.advance(limits.PairCodeTTL)
	if s := p.Status(code2); s != DeviceExpired {
		t.Fatalf("過期狀態 %s", s)
	}
	if e := p.Redeem(ctx, code2, "phone2"); e == nil || e.Code != ops.CodeForbidden {
		t.Fatalf("過期應失敗：%v", e)
	}
	if s := p.Status(code); s != DeviceExpired {
		t.Fatalf("用過的碼過期後 %s", s)
	}
}

func TestDevicePairLockoutAndMaxPending(t *testing.T) {
	p, c := newDevicePairing(t, "")
	ctx := context.Background()
	first, _, _ := p.Start()
	for i := 0; i < limits.PairMaxFailures; i++ {
		_ = p.Redeem(ctx, "WRONG123", "evil")
	}
	if e := p.Redeem(ctx, first, "evil"); e == nil || e.Code != ops.CodeLimit || !e.Retryable {
		t.Fatalf("應被鎖住：%v", e)
	}
	c.advance(limits.PairLockout + time.Second)
	if e := p.Redeem(ctx, first, "evil"); e != nil {
		t.Fatalf("鎖解開後應成功：%v", e)
	}
	for i := 1; i < limits.PairMaxPending; i++ {
		if _, _, err := p.Start(); err != nil {
			t.Fatal(err)
		}
	}
	// 已用掉的那個不算在「還沒用」裡：還能再開一個。
	if _, _, err := p.Start(); err != nil {
		t.Fatalf("已配對的不算：%v", err)
	}
	if _, _, err := p.Start(); code(err) != ops.CodeLimit {
		t.Fatalf("超過上限：%v", err)
	}
}

func TestDevicePairOps(t *testing.T) {
	reg, ap, a := setupOps(t)
	dp := NewDevicePairing(ap.DB, "https://xbot.tail3a9c.ts.net/")
	RegisterDeviceOps(reg, dp)
	ctx := context.Background()
	in := []byte(`{"origin":"http://localhost:8780"}`)
	res, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.devices.pair_start", in, ops.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var out DevicePairStartOut
	_ = json.Unmarshal(res.Output, &out)
	// 有 XBOT_PUBLIC_URL 時用它，不用瀏覽器的 localhost。
	if out.URL != "https://xbot.tail3a9c.ts.net/pair#"+NormalizePairCode(out.Code) || out.LocalOnly {
		t.Fatalf("網址 %+v", out)
	}
	if !strings.HasPrefix(out.QRPath, "M4 4h7") || out.QRSize < 29 {
		t.Fatalf("QR：%d %q", out.QRSize, out.QRPath[:20])
	}
	st, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.devices.pair_status", []byte(`{"code":"`+out.Code+`"}`), ops.Options{})
	if err != nil || !strings.Contains(string(st.Output), DevicePending) {
		t.Fatalf("狀態 %s %v", st.Output, err)
	}
	// 預演不產生配對碼。
	dry, err := reg.Invoke(ctx, ops.Owner("ui"), "xbot.devices.pair_start", in, ops.Options{DryRun: true})
	if err != nil || strings.Contains(string(dry.Output), `"code"`) {
		t.Fatalf("預演 %s %v", dry.Output, err)
	}
	// 代理（就算有 admin）不能產生手機登入碼。
	admin := pair(t, reg, ap, "admin")
	adminID, err := a.Identify(ctx, admin.Token, "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Invoke(ctx, adminID, "xbot.devices.pair_start", in, ops.Options{}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理不能產生：%v", err)
	}
	if _, err := reg.Invoke(ctx, adminID, "xbot.devices.pair_status", []byte(`{"code":"x"}`), ops.Options{}); code(err) != ops.CodeForbidden {
		t.Fatalf("代理不能查：%v", err)
	}
}

func TestQRPathDecodes(t *testing.T) {
	size, path, err := QRPath("https://xbot.example.ts.net/pair#ABCD2345")
	if err != nil {
		t.Fatal(err)
	}
	// 三個定位點：左上角從 (4,4) 開始一整排 7 格黑。
	if size != 33+8 && size != 29+8 && size != 37+8 {
		t.Fatalf("大小 %d", size)
	}
	if !strings.HasPrefix(path, "M4 4h7v1h-7z") {
		t.Fatalf("path 開頭 %q", path[:30])
	}
}
