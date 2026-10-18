package importer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// 合成的舊資料：格式照 LazyBoy server/store.py、scheduling.py、skills.py 與 OpenMausBot server/store.ts、routines.ts、
// config.ts（不用這台電腦上真的資料）。

const (
	lbBoss    = "11111111-0000-4000-8000-000000000001" // 主管（報告給使用者）
	lbWorker  = "11111111-0000-4000-8000-000000000002" // 向主管報告
	lbSolo    = "11111111-0000-4000-8000-000000000003" // 和 XBot 現有的 Bot 同名
	lbGroupID = "22222222-0000-4000-8000-000000000001"
	lbRoomID  = "33333333-0000-4000-8000-000000000001"
)

func write(t *testing.T, p string, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func js(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

// lazyBoyFixture 建一份 LazyBoy 的 .data。
func lazyBoyFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "LazyBoyHermes", ".data")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, lbDB))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;
		CREATE TABLE objects(bucket TEXT,id TEXT,data TEXT NOT NULL,PRIMARY KEY(bucket,id));
		CREATE TABLE messages(id TEXT PRIMARY KEY,agent TEXT NOT NULL,data TEXT NOT NULL,position INTEGER NOT NULL);`); err != nil {
		t.Fatal(err)
	}
	put := func(bucket, id string, v any) {
		if _, err := db.Exec(`INSERT INTO objects VALUES(?,?,?)`, bucket, id, js(v)); err != nil {
			t.Fatal(err)
		}
	}
	put("agents", lbBoss, map[string]any{"id": lbBoss, "name": "分潤研究主管", "title": "主管", "description": "帶研究員",
		"persona": "繁體中文。你帶兩位研究員。", "group_id": lbGroupID, "reports_to": "", "can_manage": true, "provider": "opencode-go",
		"model": "deepseek", "avatar_shape": "round", "avatar_color": "#F1F2F2", "created_at": "2026-10-17T04:00:31+00:00"})
	put("agents", lbWorker, map[string]any{"id": lbWorker, "name": "分潤平台比較員", "title": "研究員", "description": "比較分潤平台",
		"persona": "比較分潤平台，向主管報告。", "group_id": lbGroupID, "reports_to": lbBoss})
	put("agents", lbSolo, map[string]any{"id": lbSolo, "name": "小秘", "title": "助理", "persona": "幫忙整理", "group_id": "", "reports_to": ""})
	put("groups", lbGroupID, map[string]any{"id": lbGroupID, "name": "蝦皮分潤研究", "position": 0})
	put("rooms", lbRoomID, map[string]any{"id": lbRoomID, "name": "研究群組", "member_ids": []string{lbBoss, lbWorker, "gone"}, "seen": map[string]any{}})
	put("routines", "r-daily", map[string]any{"id": "r-daily", "agent_id": lbBoss, "title": "每日整理", "instructions": "整理昨天的研究",
		"schedule": map[string]any{"frequency": "weekdays", "time": "09:00", "timezone": "Asia/Taipei"}, "enabled": true, "hook_token": "secret"})
	put("routines", "r-weekly", map[string]any{"id": "r-weekly", "agent_id": lbWorker, "title": "週報", "instructions": "寫週報",
		"schedule": map[string]any{"frequency": "weekly", "time": "18:00", "weekdays": []int{5}}, "enabled": false})
	put("routines", "r-bad", map[string]any{"id": "r-bad", "agent_id": lbWorker, "title": "壞掉的", "instructions": "x",
		"schedule": map[string]any{"frequency": "fortnightly", "time": "09:00"}, "enabled": true})
	put("org", "approvals", map[string]any{"approval_items": []string{"新商品定價", "簽約"}})
	put("org", "token", map[string]any{"token": "do-not-import"})
	if _, err := db.Exec(`INSERT INTO messages VALUES('m1',?,?,1)`, lbBoss, js(map[string]any{"role": "user", "content": "舊的對話"})); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "settings.json"), js(map[string]any{"active": "opencode-go", "model": "x", "providers": map[string]any{
		"opencode-go": map[string]any{"label": "opencode-go", "base_url": "https://opencode.ai/zen/go/v1", "api_key": "sk-lb-opencode-0000000000000000"},
		"xai":         map[string]any{"label": "xai", "base_url": "https://api.x.ai/v1", "api_key": ""},
	}}))
	write(t, filepath.Join(dir, "access-token"), "token\n")
	// 主管的電腦
	boss := filepath.Join(dir, "bots", lbBoss)
	skills := filepath.Join(boss, "hermes", "skills")
	write(t, filepath.Join(skills, "deep-research", "SKILL.md"), "---\nname: deep-research\ndescription: LazyBoy 放的\n---\n\nDo research.")
	write(t, filepath.Join(skills, ".lazyboy-seeded"), "deep-research\n")
	// Hermes 內建、沒改過（雜湊同 internal/skills TestHashMatchesHermes）
	pdf := filepath.Join(skills, "productivity", "pdf")
	write(t, filepath.Join(pdf, "SKILL.md"), "---\nname: pdf\n---\n\nUse pypdf.")
	write(t, filepath.Join(pdf, "scripts", "merge.py"), "print('merge')")
	write(t, filepath.Join(pdf, "a-b", "c.txt"), "x")
	write(t, filepath.Join(pdf, "scripts-x.txt"), "y")
	write(t, filepath.Join(pdf, "a", "z.txt"), "z")
	write(t, filepath.Join(pdf, "__pycache__", "x.pyc"), "cache")
	write(t, filepath.Join(pdf, "scripts", "merge.pyc"), "cache")
	write(t, filepath.Join(skills, ".bundled_manifest"), "pdf:e9cc998166942d36aa2d79759f3420aa\nyoutube:0000\n")
	// Bot 自己學的技能（附帶檔案）
	write(t, filepath.Join(skills, "data", "taiwan-opendata", "SKILL.md"), "---\nname: taiwan-opendata\ndescription: 抓台灣開放資料\n---\n\n1. 找資料集\n2. 下載")
	write(t, filepath.Join(skills, "data", "taiwan-opendata", "references", "api.md"), "# API")
	write(t, filepath.Join(boss, "hermes", "memories", "MEMORY.md"), "使用者在台北\n§\n報告要附來源")
	write(t, filepath.Join(boss, "hermes", "memories", "USER.md"), "叫他王先生")
	write(t, filepath.Join(boss, "hermes", "SOUL.md"), "舊的 SOUL")
	write(t, filepath.Join(boss, "hermes", ".env"), "OPENAI_API_KEY=sk-should-not-move")
	write(t, filepath.Join(boss, "hermes", "state.db"), "對話")
	write(t, filepath.Join(boss, "workspace", "report.md"), "# 報告")
	write(t, filepath.Join(boss, "workspace", "data", "a.csv"), "a,b\n1,2\n")
	write(t, filepath.Join(boss, "workspace", "node_modules", "x", "index.js"), "cache")
	write(t, filepath.Join(boss, "home", "Downloads", "file.pdf"), "pdf")
	write(t, filepath.Join(boss, "home", ".grok", "auth.json"), `{"token":"secret"}`)
	write(t, filepath.Join(boss, "home", ".bash_history"), "ls")
	write(t, filepath.Join(boss, "home", "chrome-profile", "Default", "Cookies"), "cookies")
	write(t, filepath.Join(boss, "home", "chrome-profile", "Default", "Cache", "data_0"), "cache")
	if err := os.Symlink("box-123", filepath.Join(boss, "home", "chrome-profile", "SingletonLock")); err != nil {
		t.Fatal(err)
	}
	// 研究員：只有記憶
	write(t, filepath.Join(dir, "bots", lbWorker, "hermes", "memories", "MEMORY.md"), "平台清單在 /workspace/list.md")
	return dir
}

const (
	ombChief  = "aaaaaaaa-0000-4000-8000-000000000001"
	ombWriter = "aaaaaaaa-0000-4000-8000-000000000002"
)

// openMausBotFixture 建一份 OpenMausBot 的 ~/.openmausbot。
func openMausBotFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), ".openmausbot")
	write(t, filepath.Join(dir, "bots.json"), js([]map[string]any{
		{"id": ombChief, "threadId": "t1", "name": "小編主管", "title": "幕僚長", "description": "整理團隊", "soul": "你是幕僚長。",
			"soulHash": "x", "color": "purple", "mascotBody": "star", "section": "社群", "chiefOfStaff": true, "approvalMode": "full",
			"modelSelection": map[string]any{"instanceId": "claude", "model": "claude-sonnet"}, "createdAt": 1, "tasks": []any{},
			"avatarUrl": "/api/attachments/face.png"},
		{"id": ombWriter, "threadId": "t2", "name": "寫手", "title": "社群小編", "soul": "寫貼文。", "color": "nope", "section": "社群",
			"approvalMode": "auto", "createdAt": 2, "tasks": []any{}},
	}))
	write(t, filepath.Join(dir, "groups.json"), js([]map[string]any{
		{"id": "g1", "threadId": "t3", "name": "社群討論", "memberIds": []string{ombChief, ombWriter}, "bulletin": "先給主管看",
			"defaultResponder": map[string]any{"kind": "auto"}},
		{"id": "g2", "threadId": "t4", "name": "", "memberIds": []string{ombChief, ombWriter}, "dm": true},
	}))
	write(t, filepath.Join(dir, "section-contexts.json"), js(map[string]any{"version": 1, "sections": []string{"社群"},
		"contexts": map[string]any{"社群": map[string]any{"text": "對外文案先給主管看", "updatedAt": 1}}}))
	write(t, filepath.Join(dir, "routines.json"), js(map[string]any{"version": 1, "routines": []map[string]any{
		{"id": "o1", "name": "早安貼文", "prompt": "寫早安貼文", "target": "bot", "botId": ombWriter, "runOn": "maus", "enabled": true,
			"schedule": map[string]any{"type": "daily", "time": "08:30", "weekdays": []int{1, 2, 3, 4, 5}}},
		{"id": "o2", "name": "週日回顧", "prompt": "回顧一週", "target": "bot", "botId": ombChief, "enabled": true,
			"schedule": map[string]any{"type": "cron", "expression": "0 20 * * 0", "timeZone": "Asia/Tokyo"}},
		{"id": "o3", "name": "群組目標", "prompt": "x", "target": "room-goal", "botId": ombChief, "groupId": "g1", "enabled": true,
			"schedule": map[string]any{"type": "interval", "everyMinutes": 60}},
	}, "runs": []any{}}))
	write(t, filepath.Join(dir, "config.json"), js(map[string]any{
		"anthropic": map[string]any{"key": "sk-ant-omb-000000000000000000"}, "openai": map[string]any{"key": ""},
		"xai": map[string]any{"key": ""}, "profile": map[string]any{"name": "王"},
	}))
	write(t, filepath.Join(dir, "messages.db"), "sqlite")
	// 1×1 PNG
	png, _ := hex.DecodeString("89504e470d0a1a0a0000000d4948445200000001000000010806000000" +
		"1f15c4890000000d49444154789c6360000002000154a24f5d0000000049454e44ae426082")
	if err := os.MkdirAll(filepath.Join(dir, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "attachments", "face.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(dir, "workspaces", ombWriter)
	write(t, filepath.Join(ws, "skills", "post-format", "SKILL.md"), "---\nname: post-format\ndescription: 貼文格式\n---\n\n標題要短。")
	write(t, filepath.Join(ws, "skills", "post-format", "references", "tone.md"), "語氣")
	write(t, filepath.Join(ws, "MEMORY.md"), "# 記憶\n\n- 品牌色是紫色\n- 不用驚嘆號\n\n客戶是小店家。")
	write(t, filepath.Join(ws, "memory", "brand.md"), "品牌：小貓咖啡")
	write(t, filepath.Join(ws, "drafts", "post1.md"), "早安")
	return dir
}

// treeSum 是一個資料夾裡所有東西（名稱、種類、內容、權限、修改時間）的雜湊：匯入前後要一樣。
func treeSum(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		line := rel + "|" + info.Mode().String() + "|" + info.ModTime().String()
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			s := sha256.Sum256(raw)
			line += "|" + hex.EncodeToString(s[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}
