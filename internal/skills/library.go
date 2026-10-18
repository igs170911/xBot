package skills

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"xbot/internal/boxfs"
	"xbot/internal/limits"
)

// Library 是工作區技能庫（J09）：所有 Bot 共用的技能，放在資料目錄 <data>/skills/<id>/SKILL.md（附帶的檔案一起）。
// 開關與來源記在同一個資料夾的 .xbot-skill.json。從外面匯入的技能預設停用（SK-1），要你看過內容再打開。
type Library struct {
	Dir string
	mu  sync.Mutex
}

const libMeta = ".xbot-skill.json"

type libMetaData struct {
	Enabled   bool   `json:"enabled"`
	Source    string `json:"source"` // custom / imported / xbot
	Origin    string `json:"origin,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// LibSkill 是技能庫裡的一個技能。
type LibSkill struct {
	Skill
	Origin    string `json:"origin,omitempty" desc:"匯入時的檔名"`
	CreatedBy string `json:"created_by,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	// Review 是給你檢視的重點：會執行的指令、附帶的程式（J09：匯入的技能先看內容）。
	Review *Review `json:"review,omitempty"`
}

// Review 是匯入技能的檢視重點。
type Review struct {
	Commands []string `json:"commands" desc:"內文裡會在電腦執行的指令（最多 10 條）"`
	Scripts  []string `json:"scripts" desc:"附帶的程式檔"`
}

func (l *Library) dir(id string) (string, error) {
	if id == "" || id != path.Base(id) || strings.HasPrefix(id, ".") || strings.ContainsAny(id, `/\`) {
		return "", ErrNotFound
	}
	return filepath.Join(l.Dir, id), nil
}

// List 列出技能庫（依名稱排序）。
func (l *Library) List() ([]LibSkill, error) {
	entries, err := os.ReadDir(l.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []LibSkill{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []LibSkill{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		s, err := l.Get(e.Name())
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get 讀一個技能。
func (l *Library) Get(id string) (LibSkill, error) {
	dir, err := l.dir(id)
	if err != nil {
		return LibSkill{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return LibSkill{}, ErrNotFound
	}
	if err != nil {
		return LibSkill{}, err
	}
	var m libMetaData
	if b, err := os.ReadFile(filepath.Join(dir, libMeta)); err == nil {
		_ = json.Unmarshal(b, &m)
	} else {
		m.Enabled, m.Source = true, "custom"
	}
	d := Parse(string(raw))
	if d.Name == "" {
		d.Name = id
	}
	files, err := l.files(dir)
	if err != nil {
		return LibSkill{}, err
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Name)
	}
	place := PlaceList
	if !m.Enabled {
		place = PlaceOff
	}
	s := LibSkill{Skill: Skill{ID: id, Name: d.Name, Description: d.Description, Instructions: d.Instructions, Place: place, Enabled: m.Enabled,
		Source: m.Source, Files: names}, Origin: m.Origin, CreatedBy: m.CreatedBy, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt}
	if m.Source == "imported" {
		s.Review = review(d.Instructions, names)
	}
	return s, nil
}

// files 讀附帶的檔案（不含 SKILL.md 與 .xbot-skill.json）。
func (l *Library) files(dir string) ([]boxfs.File, error) {
	var out []boxfs.File
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "SKILL.md" || rel == libMeta {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, boxfs.File{Name: rel, Data: data, Mode: 0o644})
		return nil
	})
	return out, err
}

// Bundle 回傳要裝到 Bot 身上的內容：SKILL.md 以外的附帶檔案，以及技能本身。
func (l *Library) Bundle(id string) (LibSkill, []boxfs.File, error) {
	s, err := l.Get(id)
	if err != nil {
		return s, nil, err
	}
	dir, _ := l.dir(id)
	files, err := l.files(dir)
	return s, files, err
}

func (l *Library) writeMeta(dir string, m libMetaData) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, libMeta), b, 0o600)
}

func (l *Library) readMeta(dir string) libMetaData {
	m := libMetaData{Enabled: true, Source: "custom"}
	if b, err := os.ReadFile(filepath.Join(dir, libMeta)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// Create 新增一個技能。enabled 為 false 時是停用的（匯入）。
func (l *Library) Create(d Doc, files []boxfs.File, m libMetaData) (LibSkill, error) {
	d.Name, d.Description = oneLine(d.Name), oneLine(d.Description)
	if err := d.Validate(); err != nil {
		return LibSkill{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	all, err := l.List()
	if err != nil {
		return LibSkill{}, err
	}
	if slices.ContainsFunc(all, func(s LibSkill) bool { return s.Name == d.Name }) {
		return LibSkill{}, fmt.Errorf("%w：%s", ErrExists, d.Name)
	}
	id := DirName(d.Name, randomHex)
	for {
		if _, err := os.Stat(filepath.Join(l.Dir, id)); errors.Is(err, fs.ErrNotExist) {
			break
		}
		id = "skill-" + randomHex()
	}
	dir := filepath.Join(l.Dir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return LibSkill{}, err
	}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(path.Clean(f.Name)))
		if !strings.HasPrefix(p, dir+string(filepath.Separator)) {
			return LibSkill{}, fmt.Errorf("檔名不對：%s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return LibSkill{}, err
		}
		if err := os.WriteFile(p, f.Data, 0o600); err != nil {
			return LibSkill{}, err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(d.String()), 0o600); err != nil {
		return LibSkill{}, err
	}
	m.CreatedAt = time.Now().UnixMilli()
	m.UpdatedAt = m.CreatedAt
	if err := l.writeMeta(dir, m); err != nil {
		return LibSkill{}, err
	}
	return l.Get(id)
}

// Update 改名稱、用途或內文（nil 不改）。
func (l *Library) Update(id string, e Edit) (LibSkill, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	prev, err := l.Get(id)
	if err != nil {
		return prev, err
	}
	dir, _ := l.dir(id)
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return prev, err
	}
	d := Parse(string(raw))
	if d.Name == "" {
		d.Name = prev.Name
	}
	if e.Name != nil {
		d.Name = oneLine(*e.Name)
	}
	if e.Description != nil {
		d.Description = oneLine(*e.Description)
	}
	if e.Instructions != nil {
		d.Instructions = *e.Instructions
	}
	if err := d.Validate(); err != nil {
		return prev, err
	}
	if d.Name != prev.Name {
		all, err := l.List()
		if err != nil {
			return prev, err
		}
		if slices.ContainsFunc(all, func(s LibSkill) bool { return s.Name == d.Name && s.ID != id }) {
			return prev, fmt.Errorf("%w：%s", ErrExists, d.Name)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(d.String()), 0o600); err != nil {
		return prev, err
	}
	m := l.readMeta(dir)
	m.UpdatedAt = time.Now().UnixMilli()
	if err := l.writeMeta(dir, m); err != nil {
		return prev, err
	}
	return l.Get(id)
}

// SetEnabled 打開或停用。
func (l *Library) SetEnabled(id string, on bool) (LibSkill, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.Get(id); err != nil {
		return LibSkill{}, err
	}
	dir, _ := l.dir(id)
	m := l.readMeta(dir)
	m.Enabled, m.UpdatedAt = on, time.Now().UnixMilli()
	if err := l.writeMeta(dir, m); err != nil {
		return LibSkill{}, err
	}
	return l.Get(id)
}

// Delete 刪掉一個技能。
func (l *Library) Delete(id string) (LibSkill, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, err := l.Get(id)
	if err != nil {
		return s, err
	}
	dir, _ := l.dir(id)
	return s, os.RemoveAll(dir)
}

// Parsed 是讀懂的匯入內容。
type Parsed struct {
	Doc   Doc
	Files []boxfs.File
}

// ParseImport 讀一個匯入的檔案：.md（就是 SKILL.md）或 .zip（裡面有 SKILL.md，附帶的檔案一起）。
// 格式不對時回錯誤，說明哪裡有問題（J09）。
func ParseImport(filename string, data []byte) (Parsed, error) {
	if int64(len(data)) > limits.SkillImportBytes {
		return Parsed{}, fmt.Errorf("檔案太大（上限 %d MB）", limits.SkillImportBytes>>20)
	}
	if strings.HasSuffix(strings.ToLower(filename), ".zip") || bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return parseZip(data)
	}
	d, err := ValidateImport(string(data))
	if err != nil {
		return Parsed{}, err
	}
	return Parsed{Doc: d}, nil
}

func parseZip(data []byte) (Parsed, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Parsed{}, errors.New("讀不懂這個 .zip")
	}
	// SKILL.md 最淺的那一個就是技能的根。
	root := ""
	found := false
	for _, f := range zr.File {
		n := path.Clean(strings.ReplaceAll(f.Name, `\`, "/"))
		if path.Base(n) != "SKILL.md" || strings.HasPrefix(n, "__MACOSX/") {
			continue
		}
		dir := path.Dir(n)
		if !found || strings.Count(dir, "/") < strings.Count(root, "/") || (root != "." && dir == ".") {
			root, found = dir, true
		}
	}
	if !found {
		return Parsed{}, errors.New(".zip 裡找不到 SKILL.md")
	}
	var out Parsed
	var skillMD []byte
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !f.Mode().IsRegular() {
			continue
		}
		n := path.Clean(strings.ReplaceAll(f.Name, `\`, "/"))
		if strings.HasPrefix(n, "/") || slices.Contains(strings.Split(n, "/"), "..") {
			return Parsed{}, fmt.Errorf(".zip 裡的路徑不安全：%s", f.Name)
		}
		rel := n
		if root != "." {
			if !strings.HasPrefix(n, root+"/") {
				continue
			}
			rel = strings.TrimPrefix(n, root+"/")
		}
		parts := strings.Split(rel, "/")
		if strings.HasPrefix(n, "__MACOSX/") || slices.ContainsFunc(parts, func(p string) bool { return excludedDirs[p] || p == ".DS_Store" }) || rel == libMeta {
			continue
		}
		if int64(f.UncompressedSize64) > limits.SkillFileBytes && rel != "SKILL.md" {
			return Parsed{}, fmt.Errorf("%s 太大（每個檔案上限 %d MB）", rel, limits.SkillFileBytes>>20)
		}
		total += int64(f.UncompressedSize64)
		if total > limits.SkillImportBytes {
			return Parsed{}, fmt.Errorf("解開後太大（上限 %d MB）", limits.SkillImportBytes>>20)
		}
		rc, err := f.Open()
		if err != nil {
			return Parsed{}, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, limits.SkillImportBytes+1))
		rc.Close()
		if err != nil {
			return Parsed{}, err
		}
		if rel == "SKILL.md" {
			skillMD = b
			continue
		}
		out.Files = append(out.Files, boxfs.File{Name: rel, Data: b, Mode: 0o644})
		if len(out.Files) > limits.SkillImportFilesMax {
			return Parsed{}, fmt.Errorf(".zip 裡的檔案太多（上限 %d 個）", limits.SkillImportFilesMax)
		}
	}
	d, err := ValidateImport(string(skillMD))
	if err != nil {
		return Parsed{}, err
	}
	out.Doc = d
	return out, nil
}

var fence = regexp.MustCompile("(?s)```(?:bash|sh|shell|zsh|console)?[ \\t]*\\n(.*?)```")

// review 找出內文裡會執行的指令與附帶的程式（J09 警示列）。
func review(body string, files []string) *Review {
	r := &Review{Commands: []string{}, Scripts: []string{}}
	for _, m := range fence.FindAllStringSubmatch(body, -1) {
		for _, line := range strings.Split(m[1], "\n") {
			line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "$ "))
			if line == "" || strings.HasPrefix(line, "#") || len(r.Commands) >= 10 {
				continue
			}
			r.Commands = append(r.Commands, line)
		}
	}
	for _, f := range files {
		switch path.Ext(f) {
		case ".sh", ".py", ".js", ".ts", ".rb", ".pl", ".bash", ".ps1", ".bat", ".exe", "":
			r.Scripts = append(r.Scripts, f)
		default:
			if strings.HasPrefix(f, "scripts/") {
				r.Scripts = append(r.Scripts, f)
			}
		}
	}
	return r
}
