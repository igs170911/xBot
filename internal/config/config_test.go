package config

import (
	"testing"
)

// TestLocalNoAuthDefault 驗證 LocalNoAuth 預設關閉（B.6）。
func TestLocalNoAuthDefault(t *testing.T) {
	// 清除環境變數
	t.Setenv("XBOT_LOCAL_NOAUTH", "")
	t.Setenv("XBOT_PUBLIC_URL", "")
	t.Setenv("XBOT_TRUSTED_PROXIES", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	// 預設應該關閉
	if cfg.LocalNoAuth {
		t.Error("LocalNoAuth should default to false for security")
	}
}

// TestLocalNoAuthExplicitEnable 驗證明確設為 1 時啟用。
func TestLocalNoAuthExplicitEnable(t *testing.T) {
	t.Setenv("XBOT_LOCAL_NOAUTH", "1")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if !cfg.LocalNoAuth {
		t.Error("LocalNoAuth should be true when explicitly set to 1")
	}
}

// TestLocalNoAuthExplicitDisable 驗證明確設為 0 時關閉。
func TestLocalNoAuthExplicitDisable(t *testing.T) {
	t.Setenv("XBOT_LOCAL_NOAUTH", "0")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if cfg.LocalNoAuth {
		t.Error("LocalNoAuth should be false when explicitly set to 0")
	}
}

// TestLocalNoAuthInvalidValue 驗證無效值時回錯誤。
func TestLocalNoAuthInvalidValue(t *testing.T) {
	t.Setenv("XBOT_LOCAL_NOAUTH", "maybe")

	_, err := Load()
	if err == nil {
		t.Error("should reject invalid XBOT_LOCAL_NOAUTH value")
	}
}
