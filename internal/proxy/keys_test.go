package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKeyring(t *testing.T) *Keyring {
	t.Helper()
	k, err := NewKeyring(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestLoadKeyringCreatesPrivateFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	k1, err := LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, KeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 || info.Size() != 32 {
		t.Fatalf("金鑰檔權限 %v、大小 %d", info.Mode().Perm(), info.Size())
	}
	sealed, err := k1.Seal([]byte("sk-real"), "provider:openai")
	if err != nil {
		t.Fatal(err)
	}
	// 重新讀同一個檔案，要解得開之前的密文。
	k2, err := LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := k2.Open(sealed, "provider:openai")
	if err != nil || string(pt) != "sk-real" {
		t.Fatalf("重新載入後解不開：%q %v", pt, err)
	}
	// 權限被放寬時會收回。
	if err := os.Chmod(filepath.Join(dir, KeyFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyring(dir); err != nil {
		t.Fatal(err)
	}
	info, _ = os.Stat(filepath.Join(dir, KeyFile))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("權限沒有收回：%v", info.Mode().Perm())
	}
}

func TestSealOpen(t *testing.T) {
	k := testKeyring(t)
	sealed, err := k.Seal([]byte("sk-secret-value"), "provider:a")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("sk-secret-value")) {
		t.Fatal("密文裡看得到明文")
	}
	if _, err := k.Open(sealed, "provider:b"); !errors.Is(err, ErrSealed) {
		t.Fatalf("換了用途還解得開：%v", err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := k.Open(sealed, "provider:a"); !errors.Is(err, ErrSealed) {
		t.Fatalf("被改過還解得開：%v", err)
	}
	other, _ := NewKeyring(bytes.Repeat([]byte{8}, 32))
	good, _ := k.Seal([]byte("x"), "provider:a")
	if _, err := other.Open(good, "provider:a"); !errors.Is(err, ErrSealed) {
		t.Fatalf("別把金鑰解得開：%v", err)
	}
	if _, err := NewKeyring([]byte("short")); err == nil {
		t.Fatal("太短的主金鑰應該被拒")
	}
}

func TestProvidersStoreOnlyCiphertext(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	p := NewProviders(mem, testKeyring(t))
	if err := p.Put(ctx, Provider{ID: "openai", BaseURL: "https://api.example.com/v1/", APIKey: "sk-live-123"}); err != nil {
		t.Fatal(err)
	}
	rec, err := mem.GetProvider(ctx, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rec.SealedKey, []byte("sk-live-123")) || rec.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("存放的資料不對：%+v", rec)
	}
	got, err := p.Get(ctx, "openai")
	if err != nil || string(got.APIKey) != "sk-live-123" {
		t.Fatalf("讀回 %v %v", got, err)
	}
	if _, err := p.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的供應商：%v", err)
	}
	for _, bad := range []Provider{
		{ID: "Open AI", BaseURL: "https://x"},
		{ID: "a", BaseURL: "ftp://x"},
		{ID: "a", BaseURL: "https://u:p@x"},
		{ID: "a", BaseURL: "https://x/v1?key=1"},
	} {
		if err := p.Put(ctx, bad); err == nil {
			t.Fatalf("應該被拒：%+v", bad)
		}
	}
}

func TestSecretNeverPrinted(t *testing.T) {
	pr := Provider{ID: "a", BaseURL: "https://x", APIKey: "sk-should-not-show"}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("x", "provider", pr, "key", pr.APIKey)
	j, _ := json.Marshal(pr)
	for _, s := range []string{fmt.Sprintf("%v %+v %#v %s %q", pr, pr, pr, pr.APIKey, pr.APIKey), string(j), logs.String()} {
		if strings.Contains(s, "sk-should-not-show") {
			t.Fatalf("金鑰外流：%s", s)
		}
	}
}

func TestWorkerKeys(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	wk := NewWorkerKeys(mem, testKeyring(t))

	k1, err := wk.Issue(ctx, "bot-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(k1), "xbw_bot-1.") {
		t.Fatalf("格式不對：%s", string(k1))
	}
	again, _ := wk.Issue(ctx, "bot-1")
	if again != k1 {
		t.Fatal("同一個 nonce 應得到同一把 key")
	}
	if id, err := wk.Verify(ctx, string(k1)); err != nil || id != "bot-1" {
		t.Fatalf("驗證失敗：%q %v", id, err)
	}
	k2, _ := wk.Issue(ctx, "bot-2")

	sig := strings.SplitN(string(k1), ".", 2)[1]
	bad := []string{
		"",
		"sk-xxx",
		"xbw_bot-1",
		"xbw_bot-1." + sig[:len(sig)-2] + "AA",
		"xbw_bot-2." + sig,       // 拿別人的簽章
		"xbw_bot-3." + sig,       // 不存在的 Bot
		"xbw_bot-1." + sig + "=", // 不是合法 base64url
		strings.Replace(string(k2), "bot-2", "bot-1", 1),
	}
	for _, b := range bad {
		if _, err := wk.Verify(ctx, b); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("%q 應該被拒，結果 %v", b, err)
		}
	}

	// 換 nonce 後舊 key 失效，新 key 可用。
	k1b, err := wk.Rotate(ctx, "bot-1")
	if err != nil || k1b == k1 {
		t.Fatalf("rotate：%v", err)
	}
	if _, err := wk.Verify(ctx, string(k1)); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("rotate 後舊 key 還能用")
	}
	if _, err := wk.Verify(ctx, string(k1b)); err != nil {
		t.Fatal(err)
	}
	if err := wk.Revoke(ctx, "bot-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := wk.Verify(ctx, string(k1b)); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("revoke 後還能用")
	}
	// 另一個 Bot 不受影響。
	if _, err := wk.Verify(ctx, string(k2)); err != nil {
		t.Fatal(err)
	}
	// 不同伺服器金鑰簽出來的 key 不通用。
	other, _ := NewKeyring(bytes.Repeat([]byte{9}, 32))
	if _, err := NewWorkerKeys(mem, other).Verify(ctx, string(k2)); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("別台伺服器的 key 應該被拒")
	}
	if _, err := wk.Issue(ctx, "bad.id"); err == nil {
		t.Fatal("含 . 的 Bot 代號應該被拒")
	}
}

func TestContainerEnvHasNoRealKey(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	kr := testKeyring(t)
	if err := NewProviders(mem, kr).Put(ctx, Provider{ID: "openai", BaseURL: "https://api.example.com/v1", APIKey: "sk-real-provider-key"}); err != nil {
		t.Fatal(err)
	}
	wk, _ := NewWorkerKeys(mem, kr).Issue(ctx, "bot-1")
	env := ContainerEnv("http://host.docker.internal:8780/", "openai", wk)
	if env[EnvModelBaseURL] != "http://host.docker.internal:8780/model/openai/v1" || env[EnvWorkerKey] != string(wk) {
		t.Fatalf("環境變數不對：%v", env)
	}
	for k, v := range env {
		if strings.Contains(k+"="+v, "sk-real-provider-key") {
			t.Fatalf("容器環境變數裡有真金鑰：%s", k)
		}
	}
}
