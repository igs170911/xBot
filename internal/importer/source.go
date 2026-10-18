// Package importer 從 LazyBoy 與 OpenMausBot 匯入（V10，PRD ON-5、BT-8、SEC-1，畫板 A05、F10、G14）：
// xbot.import.detect 找這台電腦上的舊資料，xbot.import.plan 列出可以帶哪些（預演、逐項勾選、重名），
// xbot.import.run 照勾選的建立，xbot.import.status 看進度。
//
// 一律只讀舊資料：資料庫先複製到暫存資料夾再開，其他檔案只開來讀，不改、不刪舊系統的任何東西。
// 帶進來的 Bot 一律從「先問我」開始（SEC-1），技能放進它自己的技能並先關閉，定時任務先暫停，
// 模型金鑰與瀏覽器登入要你明確勾選才帶。對話紀錄不帶（見 NotCarried）。
// 每帶進一樣東西就記在 import_map，再匯入一次時已經帶過的不會重複建立。
package importer

import (
	"io/fs"
	"strings"

	"xbot/internal/boxfs"
	"xbot/internal/routines"
)

// 舊系統的種類（import_map.source）。
const (
	KindLazyBoy     = "lazyboy"
	KindOpenMausBot = "openmausbot"
)

// Labels 是舊系統給人看的名稱。
var Labels = map[string]string{KindLazyBoy: "LazyBoy", KindOpenMausBot: "OpenMausBot"}

// Source 是讀出來的一份舊資料。
type Source struct {
	Kind          string
	Path          string
	Bots          []Bot
	Teams         []Team
	Rooms         []Room
	Routines      []Routine
	Keys          []Key
	ApprovalItems []string
	// Notes 是讀的時候遇到的小問題（例：某個技能讀不到），列在預演裡。
	Notes []string
}

// Skill 是一個技能：SKILL.md 的內容與附帶的檔案（相對於技能資料夾）。
type Skill struct {
	Name         string
	Description  string
	Instructions string
	Files        []boxfs.File
	Bytes        int64
}

// Tree 是要複製進新電腦的一個本機資料夾。
type Tree struct {
	Label string // 給人看：工作資料夾、家目錄的檔案、瀏覽器登入
	Dir   string // 本機的來源資料夾
	Dest  string // 電腦裡的目的資料夾（絕對路徑）
	// Skip 回答要不要略過（rel 用 /）；nil 表示都帶。
	Skip func(rel string, d fs.DirEntry) bool
}

// Bot 是一個舊 Bot。
type Bot struct {
	ID          string
	Name        string
	Title       string
	Description string
	Soul        string
	Color       string
	Shape       string
	Eyes        string
	Image       string // data URL；沒有時空白
	TeamID      string // 舊系統的團隊 id
	ReportsTo   string // 舊系統的 Bot id
	Chief       bool   // 團隊的主管（OpenMausBot chiefOfStaff）
	CanManage   bool
	Approval    string // 舊系統的權限設定（只供說明；匯入一律先問我）
	Model       string // 舊系統用的模型（只供說明）
	Skills      []Skill
	Memory      []string // Bot 記住的事（MEMORY.md 的條目）
	User        []string // 關於你（USER.md 的條目）
	// MemoryFiles 是放不進記憶上限、或本來就是分開的記憶檔，複製到工作資料夾的「匯入的記憶」。
	MemoryFiles []boxfs.File
	Data        []Tree
	Login       *Tree
}

// HasMemory 回答有沒有記憶可以帶。
func (b Bot) HasMemory() bool { return len(b.Memory)+len(b.User)+len(b.MemoryFiles) > 0 }

// Team 是一個舊團隊。
type Team struct {
	ID      string
	Name    string
	About   string
	ChiefID string // 舊系統的 Bot id
	Members []string
}

// Room 是一個舊群組。
type Room struct {
	ID       string
	Name     string
	Bulletin string
	Members  []string
}

// Routine 是一個舊定時任務。Problem 不是空白時表示帶不過來（例：排程格式不支援）。
type Routine struct {
	ID       string
	BotID    string
	Title    string
	Prompt   string
	Schedule routines.Schedule
	Jitter   int
	Enabled  bool
	Problem  string
	Note     string // 帶得過來但要注意的事
}

// Key 是一把模型供應商的金鑰。APIKey 只在服務端，永遠不出現在任何輸出。
type Key struct {
	ID        string // XBot 的供應商代號
	Label     string
	BaseURL   string
	Preset    string
	Transport string
	APIKey    string `json:"-"`
	Origin    string // 舊系統裡的名稱
}

// NotCarried 是一律不帶的東西（PRD §15 與 SEC-1）。
var NotCarried = []string{
	"對話紀錄（舊的對話留在原本的系統）",
	"舊系統的權限設定：一律從「先問我」開始",
	"團隊管理權限：要你重新打開",
	"連接的應用程式與 MCP 的登入：之後在「連接應用程式」重新登入",
	"Bot 電腦裡的設定與快取（隱藏的資料夾）、引擎的工作階段",
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
