package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureAccessTokenCreatesOnce(t *testing.T) {
	t.Setenv("XBOT_ACCESS_TOKEN", "")
	dir := t.TempDir()
	a, err := EnsureAccessToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) < 30 {
		t.Fatalf("登入碼太短：%q", a)
	}
	st, err := os.Stat(filepath.Join(dir, TokenFile))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("登入碼權限應為 600，實際 %o", st.Mode().Perm())
	}
	b, err := EnsureAccessToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("第二次應該讀回同一個登入碼")
	}
}

func TestEnsureAccessTokenFromEnv(t *testing.T) {
	t.Setenv("XBOT_ACCESS_TOKEN", "fixed")
	got, err := EnsureAccessToken(t.TempDir())
	if err != nil || got != "fixed" {
		t.Fatalf("應使用環境變數：%q %v", got, err)
	}
}
