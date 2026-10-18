package importer

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	_ "modernc.org/sqlite" // LazyBoy 的資料庫

	"xbot/internal/box"
	"xbot/internal/limits"
	"xbot/internal/memory"
	"xbot/internal/routines"
)

// LazyBoy 的資料目錄（server/config.py DATA，預設 <repo>/.data）：
//
//	product.sqlite3      objects(bucket,id,data)：agents、groups、rooms、routines、org…；messages 是對話（不帶）
//	settings.json        providers.<id>.{label,base_url,api_key}
//	bots/<id>/home       Bot 電腦的 /home/box（chrome-profile 是瀏覽器登入）
//	bots/<id>/workspace  Bot 電腦的 /workspace
//	bots/<id>/hermes     Hermes 的資料（skills/、memories/、SOUL.md、config.yaml、state.db…）
const lbDB = "product.sqlite3"

// IsLazyBoy 回答 dir 像不像 LazyBoy 的資料目錄。
func IsLazyBoy(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, lbDB))
	return err == nil && st.Mode().IsRegular()
}

type lbAgent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Persona     string `json:"persona"`
	GroupID     string `json:"group_id"`
	ReportsTo   string `json:"reports_to"`
	CanManage   bool   `json:"can_manage"`
	Model       string `json:"model"`
	Provider    string `json:"provider"`
	AvatarShape string `json:"avatar_shape"`
}

type lbGroup struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

type lbRoom struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	MemberIDs []string `json:"member_ids"`
	Bulletin  string   `json:"bulletin"`
}

type lbRoutine struct {
	ID           string         `json:"id"`
	AgentID      string         `json:"agent_id"`
	Title        string         `json:"title"`
	Instructions string         `json:"instructions"`
	Schedule     map[string]any `json:"schedule"`
	Enabled      *bool          `json:"enabled"`
	Goal         map[string]any `json:"goal"`
}

// copyDB 把資料庫（含 -wal、-shm）複製到暫存資料夾：開原本的檔案可能會動到它的 -shm／-wal。
func copyDB(dir, tmp string) (string, error) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		src := filepath.Join(dir, lbDB+suffix)
		in, err := os.Open(src)
		if errors.Is(err, fs.ErrNotExist) && suffix != "" {
			continue
		}
		if err != nil {
			return "", err
		}
		st, err := in.Stat()
		if err == nil && st.Size() > limits.ImportDBBytes {
			err = fmt.Errorf("LazyBoy 的資料庫太大（%d MB）", st.Size()>>20)
		}
		if err != nil {
			in.Close()
			return "", err
		}
		out, err := os.OpenFile(filepath.Join(tmp, lbDB+suffix), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err == nil {
			_, err = io.Copy(out, in)
			if cerr := out.Close(); err == nil {
				err = cerr
			}
		}
		in.Close()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(tmp, lbDB), nil
}

// ReadLazyBoy 讀 LazyBoy 的資料目錄（只讀）。tmp 是放資料庫複本的暫存資料夾（讀完可以刪）。
func ReadLazyBoy(ctx context.Context, dir, tmp string) (*Source, error) {
	dbPath, err := copyDB(dir, tmp)
	if err != nil {
		return nil, fmt.Errorf("讀不到 LazyBoy 的資料庫：%w", err)
	}
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	bucket := func(name string, each func(raw []byte) error) error {
		rows, err := db.QueryContext(ctx, `SELECT data FROM objects WHERE bucket = ? ORDER BY rowid`, name)
		if err != nil {
			return fmt.Errorf("LazyBoy 的資料庫格式不對：%w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			if err := each(raw); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	src := &Source{Kind: KindLazyBoy, Path: dir}
	var agents []lbAgent
	if err := bucket("agents", func(raw []byte) error {
		var a lbAgent
		if json.Unmarshal(raw, &a) == nil && a.ID != "" && strings.TrimSpace(a.Name) != "" {
			agents = append(agents, a)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for _, a := range agents {
		src.Bots = append(src.Bots, readLBBot(dir, a, src))
	}
	known := func(id string) bool {
		return slices.ContainsFunc(agents, func(a lbAgent) bool { return a.ID == id })
	}
	var groups []lbGroup
	_ = bucket("groups", func(raw []byte) error {
		var g lbGroup
		if json.Unmarshal(raw, &g) == nil && g.ID != "" && strings.TrimSpace(g.Name) != "" {
			groups = append(groups, g)
		}
		return nil
	})
	slices.SortStableFunc(groups, func(a, b lbGroup) int { return a.Position - b.Position })
	for _, g := range groups {
		t := Team{ID: g.ID, Name: g.Name}
		var leaders []string
		for _, a := range agents {
			if a.GroupID != g.ID {
				continue
			}
			t.Members = append(t.Members, a.ID)
			// 主管：報告對象不在這個團隊裡的成員（只有一個時才算）。
			if !slices.ContainsFunc(agents, func(m lbAgent) bool { return m.ID == a.ReportsTo && m.GroupID == g.ID }) {
				leaders = append(leaders, a.ID)
			}
		}
		if len(leaders) == 1 && len(t.Members) > 1 {
			t.ChiefID = leaders[0]
		}
		src.Teams = append(src.Teams, t)
	}
	_ = bucket("rooms", func(raw []byte) error {
		var r lbRoom
		if json.Unmarshal(raw, &r) != nil || r.ID == "" {
			return nil
		}
		room := Room{ID: r.ID, Name: r.Name, Bulletin: r.Bulletin}
		for _, m := range r.MemberIDs {
			if known(m) && !slices.Contains(room.Members, m) {
				room.Members = append(room.Members, m)
			}
		}
		src.Rooms = append(src.Rooms, room)
		return nil
	})
	_ = bucket("routines", func(raw []byte) error {
		var r lbRoutine
		if json.Unmarshal(raw, &r) != nil || r.ID == "" || !known(r.AgentID) {
			return nil
		}
		src.Routines = append(src.Routines, lbRoutineOf(r))
		return nil
	})
	_ = bucket("org", func(raw []byte) error {
		var o struct {
			ApprovalItems []string `json:"approval_items"`
		}
		if json.Unmarshal(raw, &o) == nil {
			src.ApprovalItems = append(src.ApprovalItems, o.ApprovalItems...)
		}
		return nil
	})
	src.Keys = readLBKeys(dir)
	return src, nil
}

func readLBBot(dir string, a lbAgent, src *Source) Bot {
	b := Bot{ID: a.ID, Name: a.Name, Title: a.Title, Description: a.Description, Soul: a.Persona, TeamID: a.GroupID,
		ReportsTo: a.ReportsTo, CanManage: a.CanManage, Approval: "先問我以外的設定照舊系統", Shape: lbShape(a.AvatarShape)}
	if b.Soul == "" {
		b.Soul = a.Description
	}
	if a.Model != "" {
		b.Model = a.Provider + " / " + a.Model
	}
	root := filepath.Join(dir, "bots", a.ID)
	hermes := filepath.Join(root, "hermes")
	if skills, err := readLBSkills(filepath.Join(hermes, "skills")); err != nil {
		src.Notes = append(src.Notes, fmt.Sprintf("%s 的技能讀不到：%v", a.Name, err))
	} else {
		b.Skills = skills
	}
	b.Memory = readEntries(filepath.Join(hermes, "memories", "MEMORY.md"))
	b.User = readEntries(filepath.Join(hermes, "memories", "USER.md"))
	if isDir(filepath.Join(root, "workspace")) {
		b.Data = append(b.Data, Tree{Label: "工作資料夾", Dir: filepath.Join(root, "workspace"), Dest: box.WorkspaceDir, Skip: skipCaches})
	}
	if isDir(filepath.Join(root, "home")) {
		// 家目錄只帶看得到的檔案（下載的檔案等）；隱藏的資料夾是設定、快取與工具的登入（例：.grok），不帶。
		b.Data = append(b.Data, Tree{Label: "家目錄的檔案", Dir: filepath.Join(root, "home"), Dest: box.HomeDir, Skip: func(rel string, d fs.DirEntry) bool {
			top, _, _ := strings.Cut(rel, "/")
			return strings.HasPrefix(top, ".") || top == chromeProfile || skipCaches(rel, d)
		}})
		if isDir(filepath.Join(root, "home", chromeProfile)) {
			b.Login = &Tree{Label: "瀏覽器登入", Dir: filepath.Join(root, "home", chromeProfile), Dest: path.Join(box.HomeDir, chromeProfile), Skip: skipBrowserCaches}
		}
	}
	return b
}

// chromeProfile 是 Bot 電腦裡 Chromium 的設定檔資料夾（LazyBoy 與 XBot 同一個映像）。
const chromeProfile = "chrome-profile"

func lbShape(s string) string {
	if s == "round" {
		return "circle"
	}
	return s
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// readEntries 讀一份 Hermes 記憶檔（條目用 § 分隔）。
func readEntries(p string) []string {
	raw, err := readSmall(p, limits.ImportMemoryFileBytes)
	if err != nil {
		return nil
	}
	return memory.Entries(string(raw))
}

func readSmall(p string, max int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > max {
		return nil, fmt.Errorf("%s 太大", filepath.Base(p))
	}
	return raw, nil
}

// readLBSkills 讀 Bot 自己的技能：Hermes 內建且沒改過的（.bundled_manifest 的雜湊相同）與 LazyBoy 放的
// （.lazyboy-seeded）不帶，XBot 的電腦第一次開機會有自己的那一份。
func readLBSkills(root string) ([]Skill, error) {
	if !isDir(root) {
		return nil, nil
	}
	seeded := map[string]bool{}
	if raw, err := readSmall(filepath.Join(root, ".lazyboy-seeded"), 1<<20); err == nil {
		for _, n := range strings.Fields(string(raw)) {
			seeded[n] = true
		}
	}
	bundled := map[string]string{}
	if raw, err := readSmall(filepath.Join(root, ".bundled_manifest"), 1<<20); err == nil {
		for _, line := range strings.Split(string(raw), "\n") {
			if name, hash, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
				bundled[name] = hash
			}
		}
	}
	return readSkillDirs(root, func(rel, dir string) bool {
		if seeded[rel] {
			return true
		}
		h, ok := bundled[filepath.Base(dir)]
		return ok && dirHash(dir) == h
	})
}

// cacheDirs 是 Hermes 算雜湊時略過的快取資料夾（LazyBoy server/skills.py CACHE_DIRS）。
var cacheDirs = []string{"__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache"}

// dirHash 是 Hermes 的技能雜湊：依路徑排序，每個檔案的相對路徑與內容一起算 MD5（略過快取）。
func dirHash(dir string) string {
	type entry struct {
		rel   string
		parts []string
	}
	var files []entry
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		parts := strings.Split(rel, "/")
		for _, part := range parts[:len(parts)-1] {
			if slices.Contains(cacheDirs, part) {
				return nil
			}
		}
		if ext := path.Ext(rel); ext == ".pyc" || ext == ".pyo" {
			if _, err := os.Stat(strings.TrimSuffix(p, ext) + ".py"); err == nil {
				return nil
			}
		}
		files = append(files, entry{rel, parts})
		return nil
	})
	// Python 的 sorted(Path) 逐段比較。
	slices.SortFunc(files, func(a, b entry) int { return slices.Compare(a.parts, b.parts) })
	h := md5.New()
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.rel)))
		if err != nil {
			continue
		}
		h.Write([]byte(f.rel))
		h.Write(raw)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// readLBKeys 讀 settings.json 裡有金鑰的供應商。
func readLBKeys(dir string) []Key {
	raw, err := readSmall(filepath.Join(dir, "settings.json"), limits.ImportJSONBytes)
	if err != nil {
		return nil
	}
	var s struct {
		Providers map[string]struct {
			Label    string `json:"label"`
			BaseURL  string `json:"base_url"`
			APIKey   string `json:"api_key"`
			Protocol string `json:"protocol"`
		} `json:"providers"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	var out []Key
	for id, p := range s.Providers {
		if strings.TrimSpace(p.APIKey) == "" || strings.TrimSpace(p.BaseURL) == "" {
			continue
		}
		out = append(out, keyOf(id, p.Label, p.BaseURL, p.Protocol, p.APIKey))
	}
	slices.SortFunc(out, func(a, b Key) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// lbRoutineOf 把 LazyBoy 的排程（scheduling.py normalize_schedule）換成 XBot 的。
func lbRoutineOf(r lbRoutine) Routine {
	out := Routine{ID: r.ID, BotID: r.AgentID, Title: r.Title, Prompt: r.Instructions, Enabled: r.Enabled == nil || *r.Enabled}
	s := r.Schedule
	str := func(k string) string {
		v, _ := s[k].(string)
		return strings.TrimSpace(v)
	}
	num := func(k string) int {
		switch v := s[k].(type) {
		case float64:
			return int(v)
		case string:
			var n int
			_, _ = fmt.Sscanf(v, "%d", &n)
			return n
		}
		return 0
	}
	sc := routines.Schedule{Kind: str("frequency"), Time: str("time"), Timezone: str("timezone"), Date: str("date"),
		DayOfMonth: num("day_of_month"), EveryMinutes: num("every_minutes")}
	if days, ok := s["weekdays"].([]any); ok {
		for _, d := range days {
			if f, ok := d.(float64); ok {
				sc.Weekdays = append(sc.Weekdays, int(f))
			}
		}
	}
	if len(sc.Date) > 10 {
		sc.Date = sc.Date[:10]
	}
	if sc.Kind == "" {
		sc.Kind = "daily"
	}
	out.Schedule = sc
	out.Jitter = num("jitter_minutes")
	if len(r.Goal) > 0 {
		out.Note = "有目標的定時任務：目標與進度不帶，只帶排程與工作內容"
	}
	return out
}
