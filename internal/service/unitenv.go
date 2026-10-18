package service

import (
	"os"
	"path/filepath"
	"strings"
)

// 服務檔的環境變數（V10）：make install／make update 不再用當下的環境變數整份重寫服務檔。
// 已安裝時沿用原本服務檔裡的 Environment=，只有這次執行時明確設了的服務變數（ServiceEnvKeys）才覆寫。
// 原本服務檔裡不是 XBot 的變數（例：DOCKER_HOST）也原樣保留。

// ServiceEnvKeys 是會寫進服務檔的變數：執行 make install／make update 時有設就寫進去（覆寫原本的值）。
// 不含登入碼（XBOT_ACCESS_TOKEN）：服務檔是 0644，任何人都讀得到。
var ServiceEnvKeys = []string{
	"XBOT_DATA_DIR", "XBOT_BIND", "XBOT_PORT",
	"XBOT_LOCAL_NOAUTH", "XBOT_PUBLIC_URL", "XBOT_TRUSTED_PROXIES", "XBOT_MCP_LOCAL_OWNER",
	"XBOT_BOX_IMAGE", "XBOT_NETGUARD", "XBOT_PUSH_SUBJECT", "XBOT_IMPORT_PATHS",
	"LAZYHERMES_DATA_DIR", "OMB_DATA_DIR",
}

// coreKeys 由 RenderUnit 一定寫出（值來自 Paths、Settings）。
var coreKeys = map[string]bool{"XBOT_DATA_DIR": true, "XBOT_BIND": true, "XBOT_PORT": true}

// EnvVar 是服務檔裡的一個環境變數。
type EnvVar struct{ Key, Value string }

// UnitPath 是服務檔的位置。
func (p Paths) UnitPath() string { return filepath.Join(p.UnitDir, Unit) }

// ReadUnitEnv 讀服務檔裡的 Environment=（沒有服務檔時回傳 nil）。
func ReadUnitEnv(unitFile string) []EnvVar {
	b, err := os.ReadFile(unitFile)
	if err != nil {
		return nil
	}
	return parseUnitEnv(string(b))
}

// reportEnvChanges 印出服務檔裡改了哪些環境變數（更新時不該有意外的變化）。
func (m *Manager) reportEnvChanges(oldUnit, newUnit string) {
	before := map[string]string{}
	for _, e := range parseUnitEnv(oldUnit) {
		before[e.Key] = e.Value
	}
	after := parseUnitEnv(newUnit)
	seen := map[string]bool{}
	for _, e := range after {
		seen[e.Key] = true
		if v, ok := before[e.Key]; !ok {
			m.say("   服務設定新增 %s=%s", e.Key, e.Value)
		} else if v != e.Value {
			m.say("   服務設定 %s：%s → %s", e.Key, v, e.Value)
		}
	}
	for k := range before {
		if !seen[k] {
			m.say("   服務設定拿掉 %s", k)
		}
	}
}

func parseUnitEnv(text string) []EnvVar {
	var out []EnvVar
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "Environment=")
		if !ok {
			continue
		}
		for _, w := range splitUnitWords(rest) {
			k, v, ok := strings.Cut(w, "=")
			if !ok || k == "" {
				continue
			}
			out = append(out, EnvVar{k, strings.ReplaceAll(v, "%%", "%")})
		}
	}
	return out
}

// splitUnitWords 依 systemd 的規則切開一行 Environment=（空白分隔，雙引號或單引號包住的算一個，\ 跳脫）。
func splitUnitWords(s string) []string {
	var out []string
	var b strings.Builder
	in, quote, esc := false, byte(0), false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			b.WriteByte(c)
			esc = false
		case c == '\\':
			esc = true
			in = true
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				b.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote, in = c, true
		case c == ' ' || c == '\t':
			if in {
				out = append(out, b.String())
				b.Reset()
				in = false
			}
		default:
			b.WriteByte(c)
			in = true
		}
	}
	if in {
		out = append(out, b.String())
	}
	return out
}

// ApplyUnitEnv 把已安裝服務檔裡的 XBot 變數補進這個行程的環境（已經有設的不動），讓 xbot service／backup／restore／token
// 看到的資料目錄、埠號和服務一樣。回傳補了哪些。
func ApplyUnitEnv(unitFile string) []string {
	var set []string
	for _, e := range ReadUnitEnv(unitFile) {
		if !isServiceKey(e.Key) {
			continue
		}
		if _, ok := os.LookupEnv(e.Key); ok {
			continue
		}
		if os.Setenv(e.Key, e.Value) == nil {
			set = append(set, e.Key)
		}
	}
	return set
}

func isServiceKey(k string) bool {
	for _, s := range ServiceEnvKeys {
		if s == k {
			return true
		}
	}
	return false
}

// MergeEnv 算出服務檔裡 XBOT_DATA_DIR／BIND／PORT 以外的變數：原本服務檔裡的照順序保留，
// lookup（這次執行的環境）有設的服務變數覆寫或新增。
func MergeEnv(unit []EnvVar, lookup func(string) (string, bool)) []EnvVar {
	var out []EnvVar
	seen := map[string]bool{}
	for _, e := range unit {
		if coreKeys[e.Key] || seen[e.Key] {
			continue
		}
		seen[e.Key] = true
		if isServiceKey(e.Key) {
			if v, ok := lookup(e.Key); ok {
				e.Value = v
			}
		}
		out = append(out, e)
	}
	for _, k := range ServiceEnvKeys {
		if coreKeys[k] || seen[k] {
			continue
		}
		if v, ok := lookup(k); ok {
			out = append(out, EnvVar{k, v})
		}
	}
	return out
}

// unitLine 把一個變數寫成服務檔的 Environment= 一行（需要時加引號；% 要寫成 %%）。
func unitLine(e EnvVar) string {
	kv := strings.ReplaceAll(e.Key+"="+e.Value, "%", "%%")
	if strings.ContainsAny(kv, " \t\"'\\") {
		kv = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(kv) + `"`
	}
	return "Environment=" + kv
}
