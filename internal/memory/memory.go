// Package memory 是 Bot 的長期記憶（PRD 6.7 MM-*，C31）：讀寫 Hermes 的 MEMORY.md／USER.md、變更紀錄與復原。
package memory

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"xbot/internal/boxfs"
	"xbot/internal/engine/hermes"
	"xbot/internal/limits"
)

// Bot 的長期記憶就是 Hermes 自己存的兩份清單（照 LazyBoy server/memory.py，已對過映像裡的 /opt/hermes/tools/memory_tool_store.py）：
//   - /hermes/memories/USER.md：關於你（學到的使用者資料）
//   - /hermes/memories/MEMORY.md：Bot 記住的事（工作與環境）
//
// 每條之間用 "\n§\n" 分隔。上限是字數（config.yaml 的 memory.user_char_limit／memory_char_limit，預設 1375／2200）。
// Hermes 在每個對話開始時把兩份清單當成固定的快照放進系統提示；對話中途寫進去的，下一個對話才看得到。
// （PRD MM-1 寫的「前 200 行／24 KB」是 OpenMausBot 的做法，Hermes 實際是上面的字數上限。）

// Dir 是記憶檔所在的資料夾。
const Dir = hermes.HomeDir + "/memories"

// Delimiter 是條目之間的分隔（Hermes ENTRY_DELIMITER）。
const Delimiter = "\n§\n"

// Target 是哪一份記憶。
type Target string

// 兩份記憶。
const (
	TargetUser   Target = "user"
	TargetMemory Target = "memory"
)

// EnumValues 讓 JSON Schema 列出可用值。
func (Target) EnumValues() []string { return []string{"user", "memory"} }

// File 是記憶檔的檔名。
func (t Target) File() string {
	if t == TargetUser {
		return "USER.md"
	}
	return "MEMORY.md"
}

func (t Target) valid() bool { return t == TargetUser || t == TargetMemory }

// List 是一份記憶。
type List struct {
	Entries []string `json:"entries"`
	Used    int      `json:"used" desc:"已用字數（含分隔）"`
	Limit   int      `json:"limit" desc:"上限字數"`
}

// State 是兩份記憶。
type State struct {
	User   List `json:"user" desc:"關於你（USER.md）"`
	Memory List `json:"memory" desc:"Bot 記住的事（MEMORY.md）"`
}

// Get 回傳某一份。
func (s State) Get(t Target) List {
	if t == TargetUser {
		return s.User
	}
	return s.Memory
}

// Entries 把記憶檔切成條目（去掉空白的）。
func Entries(raw string) []string {
	out := []string{}
	for _, e := range strings.Split(raw, Delimiter) {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// Join 把條目接回記憶檔的內容。
func Join(entries []string) string { return strings.Join(entries, Delimiter) }

func used(entries []string) int { return utf8.RuneCountInString(Join(entries)) }

// Limits 讀 Bot 的 config.yaml 裡的上限；沒有設定時用 Hermes 的預設。
func Limits(ctx context.Context, f boxfs.FS) (map[Target]int, error) {
	out := map[Target]int{TargetUser: limits.MemoryUserCharsDefault, TargetMemory: limits.MemoryCharsDefault}
	raw, err := f.ReadFile(ctx, path.Join(hermes.HomeDir, hermes.ConfigFile))
	if errors.Is(err, boxfs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg struct {
		Memory struct {
			User   int `yaml:"user_char_limit"`
			Memory int `yaml:"memory_char_limit"`
		} `yaml:"memory"`
	}
	if yaml.Unmarshal(raw, &cfg) == nil {
		if cfg.Memory.User > 0 {
			out[TargetUser] = cfg.Memory.User
		}
		if cfg.Memory.Memory > 0 {
			out[TargetMemory] = cfg.Memory.Memory
		}
	}
	return out, nil
}

// ReadRaw 讀一份記憶檔的原文；不存在時是空字串。
func ReadRaw(ctx context.Context, f boxfs.FS, t Target) (string, error) {
	b, err := f.ReadFile(ctx, path.Join(Dir, t.File()))
	if errors.Is(err, boxfs.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// Read 讀兩份記憶與用量（LazyBoy memory.read）。
func Read(ctx context.Context, f boxfs.FS) (State, error) {
	caps, err := Limits(ctx, f)
	if err != nil {
		return State{}, err
	}
	var s State
	for _, t := range []Target{TargetUser, TargetMemory} {
		raw, err := ReadRaw(ctx, f, t)
		if err != nil {
			return State{}, err
		}
		e := Entries(raw)
		l := List{Entries: e, Used: used(e), Limit: caps[t]}
		if t == TargetUser {
			s.User = l
		} else {
			s.Memory = l
		}
	}
	return s, nil
}

// ErrChanged 表示 Bot 在你編輯時改了這份記憶；存下去會蓋掉它剛學到的事（LazyBoy memory.Changed）。
var ErrChanged = errors.New("Bot 剛剛更新了這份記憶，已重新載入，請再改一次")

// InvalidError 是輸入不對（條目太長、超過上限）。
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// Clean 整理條目：去掉前後空白與空的、§ 換成空白、遮蔽密鑰；一條太長時回錯誤。
func Clean(items []string) ([]string, error) {
	out := []string{}
	for _, it := range items {
		text := strings.TrimSpace(strings.ReplaceAll(it, "§", " "))
		if text == "" {
			continue
		}
		if utf8.RuneCountInString(text) > limits.MemoryEntryMax {
			return nil, &InvalidError{fmt.Sprintf("每條記憶最多 %d 字", limits.MemoryEntryMax)}
		}
		out = append(out, Redact(text))
	}
	return out, nil
}

// Write 換掉一份記憶，回傳寫入前與寫入後的原文。previous 不是 nil 時，是你編輯的那一版：Bot 在這之間改過就不寫（ErrChanged）。
// 超過上限時拒絕，但已經超過的清單可以一條一條縮小（LazyBoy memory.write）。
//
// 和 LazyBoy 不同：這裡經 Docker API 寫檔，拿不到 Hermes 的 flock（memories/<檔名>.lock）。靠 previous 比對擋掉大部分的競爭；
// Bot 正好在讀寫之間寫入的極短空檔仍可能被蓋掉。
func Write(ctx context.Context, f boxfs.FS, t Target, items, previous []string) (before, after string, err error) {
	if !t.valid() {
		return "", "", &InvalidError{"無效的記憶類別"}
	}
	clean, err := Clean(items)
	if err != nil {
		return "", "", err
	}
	caps, err := Limits(ctx, f)
	if err != nil {
		return "", "", err
	}
	before, err = ReadRaw(ctx, f, t)
	if err != nil {
		return "", "", err
	}
	current := Entries(before)
	if previous != nil {
		prev := []string{}
		for _, p := range previous {
			if p = strings.TrimSpace(p); p != "" {
				prev = append(prev, p)
			}
		}
		if !slices.Equal(prev, current) {
			return before, before, ErrChanged
		}
	}
	size, limit := used(clean), caps[t]
	if size > limit && size >= used(current) {
		return before, before, &InvalidError{fmt.Sprintf("超過這類記憶的上限 %d 字，請刪掉或縮短幾條", limit)}
	}
	after = Join(clean)
	if after == before {
		return before, after, nil
	}
	err = f.WriteFiles(ctx, Dir, []boxfs.File{{Name: t.File(), Data: []byte(after), Mode: 0o600}})
	return before, after, err
}

// 看起來像密鑰的字串（寫入前遮蔽，MM-3）。
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(-----END [A-Z ]*PRIVATE KEY-----|$)`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9\-]{10,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{30,}`),
	regexp.MustCompile(`\bxbw_[A-Za-z0-9_.\-]{8,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`),
}

// 「密碼：xxx」「api_key=xxx」這類：留下名稱，遮蔽值。
var labeled = regexp.MustCompile(`(?i)((?:password|passwd|passphrase|密碼|口令|api[ _\-]?key|access[ _\-]?token|token|secret|金鑰|驗證碼|otp)\s*(?:是|為|[:=：])\s*)([^\s，。,;；]+)`)

// Masked 是遮蔽後的值。
const Masked = "***"

// Redact 遮蔽文字裡的密碼與金鑰。
func Redact(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, Masked)
	}
	return labeled.ReplaceAllString(s, "${1}"+Masked)
}
