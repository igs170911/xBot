package runtime

import (
	"encoding/json"
	"regexp"
	"strings"

	"xbot/internal/engine"
)

// 只讀的程式不用你核准（2026-10-20 使用者決定）。
//
// 「先問我」「一般的事自己做」的 Bot，Hermes 每次跑 execute_code 都要整段核准：它以為自己在主機上，不知道其實在 Bot 的
// 隔離電腦裡（Hermes 在容器後端時本來就不問）。結果連讀幾個檔案都要你按允許，交代的事做一半就停（2026-10-20 基準測試）。
// 現在 XBot 先看程式碼：只讀檔、算資料、印結果、上網查（web_search／web_extract）的，自動允許這一次；
// 會寫檔、刪檔、改權限、執行指令、自己連網路送資料，或寫法看不懂（exec、getattr、別名匯入…）的，照舊送到「待我處理」。
// 判斷寧可多問：看不懂就當成不是只讀。

// readOnlyDeny 是只要出現就不算只讀的寫法。
var readOnlyDeny = regexp.MustCompile(strings.Join([]string{
	// 寫檔、刪檔、搬檔、改權限
	`\.write_text\s*\(`, `\.write_bytes\s*\(`, `\.unlink\s*\(`, `\.rmdir\s*\(`, `\.mkdir\s*\(`, `\.touch\s*\(`, `\.rename\s*\(`,
	`\.chmod\s*\(`, `\.symlink_to\s*\(`, `\.hardlink_to\s*\(`, `\.to_csv\s*\(`, `\.to_excel\s*\(`, `\.to_json\s*\(`, `\.to_parquet\s*\(`,
	`\.save\s*\(`, `\.savefig\s*\(`, `\.writelines?\s*\(`, `\.truncate\s*\(`, `\.extractall\s*\(`, `\.extract\s*\(`,
	`\bos\s*\.\s*(remove|unlink|rmdir|removedirs|rename|renames|replace|makedirs|mkdir|chmod|chown|lchown|link|symlink|truncate|system|popen|kill|killpg|fork|spawn\w*|exec\w*|write|open|putenv|chdir)\b`,
	`\bshutil\b`, `\bsubprocess\b`, `\bpty\b`, `\bsignal\b`, `\bctypes\b`, `\bmultiprocessing\b`, `\bthreading\b`,
	`\btempfile\b`, `\bzipfile\b`, `\btarfile\b`, `\bsqlite3\b`, `\bdbm\b`, `\bshelve\b`, `\bpickle\b`, `\bmarshal\b`,
	// 自己連網路（XBot 的工具 web_search、web_extract 不在這裡）
	`\bsocket\b`, `\brequests\b`, `\burllib\d?\b`, `\bhttp\.client\b`, `\bhttpx\b`, `\baiohttp\b`, `\bftplib\b`, `\bsmtplib\b`,
	`\bparamiko\b`, `\bwebbrowser\b`,
	// Hermes 工具裡會改東西、跑指令的
	`\bterminal\s*\(`, `\bwrite_file\s*\(`, `\bpatch\s*\(`, `\bbrowser_\w+\s*\(`, `\bcdp\s*\(`, `\bprocess\w*\s*\(`,
	// 看不懂的寫法：動態執行、取屬性、別名、改內建
	`\bexec\s*\(`, `\beval\s*\(`, `\bcompile\s*\(`, `__import__`, `\bimportlib\b`, `\bgetattr\s*\(`, `\bsetattr\s*\(`,
	`\bdelattr\s*\(`, `\bglobals\s*\(`, `\blocals\s*\(`, `\bvars\s*\(`, `__builtins__`, `__dict__`, `__class__`, `__subclasses__`,
	`\bsys\s*\.\s*modules\b`, `\bimport\s+(os|sys|shutil|subprocess|pathlib|io|builtins)\s+as\b`,
	`\bfrom\s+(os|shutil|subprocess|builtins|io|sys|ctypes|socket)\s+import\b`, `\bbuiltins\b`,
	`\bpip\b`, `\buv\s`,
}, "|"))

// openCall 找出 open(...) 的模式：第二個參數或 mode=。
var (
	openCall   = regexp.MustCompile(`\bopen\s*\(`)
	quotedMode = regexp.MustCompile(`^\s*['"]([^'"]*)['"]`)
	modeKw     = regexp.MustCompile(`\bmode\s*=\s*['"]([^'"]*)['"]`)
	anyModeKw  = regexp.MustCompile(`\bmode\s*=`)
	kwArg      = regexp.MustCompile(`^\s*\w+\s*=`)
	// 只讀模式只能有 r、b、t（「w」「a」「x」「+」都會改檔）。
	readMode = regexp.MustCompile(`^[rbt]*$`)
)

// ReadOnlyScript 回答一段 Python 是不是只讀（讀檔、計算、印出、web_search／web_extract）。看不懂就回 false。
func ReadOnlyScript(code string) bool {
	if strings.TrimSpace(code) == "" || readOnlyDeny.MatchString(code) {
		return false
	}
	for _, loc := range openCall.FindAllStringIndex(code, -1) {
		args, ok := callArgs(code[loc[1]:])
		if !ok {
			return false
		}
		if m := modeKw.FindStringSubmatch(args); m != nil {
			if !readMode.MatchString(m[1]) {
				return false
			}
			continue
		}
		if anyModeKw.MatchString(args) {
			return false // mode= 不是寫死的字串
		}
		// 第二個位置參數：第一個逗號之後（檔名裡的逗號在引號裡，callArgs 已經分好）。
		parts := splitTopLevel(args)
		if len(parts) >= 2 && !kwArg.MatchString(parts[1]) {
			m := quotedMode.FindStringSubmatch(parts[1])
			if m == nil || !readMode.MatchString(m[1]) {
				return false // 模式不是寫死的字串，或會寫
			}
		}
	}
	return true
}

// callArgs 取出呼叫的參數文字（到對應的右括號）；括號沒有對上時 ok 是 false。
func callArgs(s string) (string, bool) {
	depth, quote := 1, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
			if depth == 0 {
				return s[:i], true
			}
		}
	}
	return "", false
}

// splitTopLevel 用最外層的逗號切參數。
func splitTopLevel(s string) []string {
	var out []string
	depth, quote, start := 0, byte(0), 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// readOnlyExecuteCode 認出 Hermes 對一段只讀的 execute_code 要的整段核准（pattern_key execute_code）。
func readOnlyExecuteCode(req *engine.Request) bool {
	if req == nil || req.Kind != "approval" || len(req.Params) == 0 {
		return false
	}
	var p struct {
		Command     string   `json:"command"`
		PatternKey  string   `json:"pattern_key"`
		PatternKeys []string `json:"pattern_keys"`
	}
	if json.Unmarshal(req.Params, &p) != nil || p.PatternKey != "execute_code" || len(p.PatternKeys) > 1 {
		return false
	}
	code, ok := strings.CutPrefix(p.Command, "execute_code <<'PY'\n")
	if !ok {
		return false
	}
	code, ok = strings.CutSuffix(code, "\nPY")
	return ok && ReadOnlyScript(code)
}
