// Package skills 是技能（PRD 6.6 SK-*，C31）：每個 Bot 的技能（Hermes 的 SKILL.md）與工作區技能庫。
package skills

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"xbot/internal/boxfs"
	"xbot/internal/engine/hermes"
	"xbot/internal/limits"
)

// 一個 Bot 的技能放在它電腦裡的 Hermes 家目錄（/hermes/skills/<分類>/<名稱>/SKILL.md），和 Hermes 自己用的同一份
// （照 LazyBoy server/skills.py）。開關記在 /hermes/config.yaml：skills.disabled（Hermes 不載入的名稱）與
// xbot_skill_library（放在技能庫、平常不佔用的名稱，也在 skills.disabled 裡）。
//
// 技能的來源：
//   - hermes：Hermes 內建、沒改過（.bundled_manifest 的雜湊對得上，同 Hermes tools/skills_sync.py）
//   - xbot：XBot 內建（builtin/，第一次開機前放進去）
//   - learned：Bot 自己在對話裡學到的（.usage.json 的 created_by＝agent）
//   - imported：從外面匯入、或從工作區技能庫裝上的
//   - proposed：Bot 提議、你核准的
//   - custom：你寫的
const (
	SkillsDir  = hermes.HomeDir + "/skills"
	configPath = hermes.HomeDir + "/" + hermes.ConfigFile
	// stateFile 是 XBot 在 /hermes/skills 裡的紀錄：放過哪些內建技能、哪些是匯入的。
	stateFile = ".xbot.json"
	// libraryKey 是 config.yaml 裡技能庫的名稱清單（LazyBoy 用 product_skill_library）。
	libraryKey = "xbot_skill_library"
)

// Place 是技能在哪：list＝技能清單（每次對話帶上名稱與用途）、library＝技能庫（需要時才載入）、off＝已停用。
type Place string

// 技能的位置。
const (
	PlaceList    Place = "list"
	PlaceLibrary Place = "library"
	PlaceOff     Place = "off"
)

// EnumValues 讓 JSON Schema 列出可用值。
func (Place) EnumValues() []string { return []string{"list", "library", "off"} }

// Skill 是一個技能。
type Skill struct {
	ID           string   `json:"id" desc:"技能的資料夾（Bot 的技能：/hermes/skills 底下的相對路徑；技能庫：資料夾名稱）"`
	Name         string   `json:"name"`
	Description  string   `json:"description" desc:"什麼時候使用"`
	Instructions string   `json:"instructions,omitempty" desc:"怎麼做（SKILL.md 的內文）"`
	Place        Place    `json:"place" desc:"list：技能清單；library：技能庫（需要時才載入）；off：已停用"`
	Enabled      bool     `json:"enabled"`
	Source       string   `json:"source" desc:"hermes（Hermes 內建）、xbot（XBot 內建）、learned（從對話學到）、imported（匯入）、proposed（Bot 提議、你核准）、custom（自訂）"`
	Files        []string `json:"files,omitempty" desc:"附帶的檔案（scripts/、references/…）"`
	LastUsedAt   string   `json:"last_used_at,omitempty"`
}

//go:embed builtin
var builtinFS embed.FS

// Builtin 回傳 XBot 內建技能：資料夾名稱 → SKILL.md。
func Builtin() map[string][]byte {
	out := map[string][]byte{}
	_ = fs.WalkDir(builtinFS, "builtin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Base(p) != "SKILL.md" {
			return err
		}
		data, err := builtinFS.ReadFile(p)
		if err == nil {
			out[path.Base(path.Dir(p))] = data
		}
		return err
	})
	return out
}

// state 是 /hermes/skills/.xbot.json。
type state struct {
	Seeded  []string          `json:"seeded,omitempty"`
	Sources map[string]string `json:"sources,omitempty"` // 資料夾 → imported／proposed
}

// retired 是內建技能以前的版本（SKILL.md 的 sha256）：Bot 電腦裡的那份還是舊版、沒被改過，開機前換成新版；
// 使用者或 Bot 改過的（雜湊對不上）照舊保留。XBot 新增（LazyBoy 只放一次）。
//
// deep-research 舊版要子代理（delegate_task）平行查，但 2026-10-19 起 Bot 沒有子代理了：用了這個技能的 Bot 只讀了說明就
// 回答、一次都沒有上網查（2026-10-20 使用者：「我看不到有任何真的去搜索的樣子」）。
var retired = map[string][]string{
	"deep-research": {"b48cb4bba4d7587660c808379fb2ba2fef08c76a3cee6cf65ebaa986484c3877"},
}

// Seed 把 XBot 內建技能放進 Bot 的電腦，每個只放一次；使用者之後改了或刪了都保留（LazyBoy computers.py seed_skills）。
// 還是舊版沒改過的（retired）換成新版。開機前（box Prepare）呼叫。
func Seed(ctx context.Context, f boxfs.FS) error {
	st, err := readState(ctx, f)
	if err != nil {
		return err
	}
	var files []boxfs.File
	names := Builtin()
	changed := false
	for _, name := range mapsKeys(names) {
		if slices.Contains(st.Seeded, name) {
			if len(retired[name]) == 0 {
				continue
			}
			cur, err := f.ReadFile(ctx, path.Join(SkillsDir, name, "SKILL.md"))
			if errors.Is(err, boxfs.ErrNotExist) {
				continue // 刪掉了：尊重
			} else if err != nil {
				return err
			}
			sum := sha256.Sum256(cur)
			if slices.Contains(retired[name], hex.EncodeToString(sum[:])) && !bytes.Equal(cur, names[name]) {
				files = append(files, boxfs.File{Name: name + "/SKILL.md", Data: names[name], Mode: 0o644})
				changed = true
			}
			continue
		}
		if _, err := f.ReadFile(ctx, path.Join(SkillsDir, name, "SKILL.md")); errors.Is(err, boxfs.ErrNotExist) {
			files = append(files, boxfs.File{Name: name + "/SKILL.md", Data: names[name], Mode: 0o644})
		} else if err != nil {
			return err
		}
		st.Seeded = append(st.Seeded, name)
		changed = true
	}
	if !changed {
		return nil
	}
	sort.Strings(st.Seeded)
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return f.WriteFiles(ctx, SkillsDir, append(files, boxfs.File{Name: stateFile, Data: raw, Mode: 0o644}))
}

func readState(ctx context.Context, f boxfs.FS) (state, error) {
	var st state
	raw, err := f.ReadFile(ctx, path.Join(SkillsDir, stateFile))
	if errors.Is(err, boxfs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	_ = json.Unmarshal(raw, &st) // 壞掉就當沒有
	return st, nil
}

func writeState(ctx context.Context, f boxfs.FS, st state) error {
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return f.WriteFiles(ctx, SkillsDir, []boxfs.File{{Name: stateFile, Data: raw, Mode: 0o644}})
}

func mapsKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// 不算技能內容的資料夾（Hermes agent/skill_utils.py EXCLUDED_SKILL_DIRS）。
var excludedDirs = map[string]bool{".git": true, ".github": true, ".hub": true, ".archive": true, ".curator_backups": true, ".locks": true,
	".venv": true, "venv": true, "node_modules": true, "site-packages": true, "__pycache__": true, ".tox": true, ".nox": true,
	".pytest_cache": true, ".mypy_cache": true, ".ruff_cache": true}

// 執行時產生的快取（Hermes tools/skills_sync_optional.py _RUNTIME_CACHE_DIRS），不算進雜湊。
var cacheDirs = map[string]bool{"__pycache__": true, ".pytest_cache": true, ".mypy_cache": true, ".ruff_cache": true}

// tree 是一個 Bot 的 /hermes/skills。
type tree struct {
	files map[string][]byte // 相對路徑 → 內容
}

func readTree(ctx context.Context, f boxfs.FS) (tree, error) {
	list, err := f.ReadDir(ctx, SkillsDir, limits.SkillTreeBytes)
	if err != nil {
		return tree{}, err
	}
	t := tree{files: map[string][]byte{}}
	for _, x := range list {
		t.files[x.Name] = x.Data
	}
	return t, nil
}

// dirs 回傳所有技能的資料夾（有 SKILL.md、不在排除的資料夾裡），排序。
func (t tree) dirs() []string {
	var out []string
	for name := range t.files {
		if path.Base(name) != "SKILL.md" || name == "SKILL.md" {
			continue
		}
		dir := path.Dir(name)
		if slices.ContainsFunc(strings.Split(dir, "/"), func(p string) bool { return excludedDirs[p] }) {
			continue
		}
		out = append(out, dir)
	}
	sort.Strings(out)
	// 技能裡面的 SKILL.md（例：scripts/x/SKILL.md）不算另一個技能。
	var top []string
	for _, d := range out {
		if !slices.ContainsFunc(top, func(p string) bool { return strings.HasPrefix(d, p+"/") }) {
			top = append(top, d)
		}
	}
	return top
}

// hash 是 Hermes 的技能雜湊：依路徑排序，每個檔案的相對路徑與內容做 MD5；執行時的快取不算（tools/skills_sync.py _dir_hash）。
func (t tree) hash(dir string) string {
	type item struct {
		rel   string
		parts []string
	}
	var items []item
	for name := range t.files {
		if !strings.HasPrefix(name, dir+"/") {
			continue
		}
		rel := strings.TrimPrefix(name, dir+"/")
		parts := strings.Split(rel, "/")
		if slices.ContainsFunc(parts[:len(parts)-1], func(p string) bool { return cacheDirs[p] }) {
			continue
		}
		if ext := path.Ext(rel); ext == ".pyc" || ext == ".pyo" {
			if _, ok := t.files[strings.TrimSuffix(name, ext)+".py"]; ok {
				continue
			}
		}
		items = append(items, item{rel, parts})
	}
	// Python 的 sorted(Path) 依路徑的每一段比較（不是整串字串）。
	sort.Slice(items, func(i, j int) bool { return slices.Compare(items[i].parts, items[j].parts) < 0 })
	h := md5.New()
	for _, it := range items {
		h.Write([]byte(it.rel))
		h.Write(t.files[dir+"/"+it.rel])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// bundledUnchanged 回傳沒改過的 Hermes 內建技能（資料夾）。
func (t tree) bundledUnchanged() map[string]bool {
	origin := map[string]string{}
	for _, line := range strings.Split(string(t.files[".bundled_manifest"]), "\n") {
		if name, h, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
			origin[strings.TrimSpace(name)] = strings.TrimSpace(h)
		}
	}
	out := map[string]bool{}
	for _, dir := range t.dirs() {
		if h := origin[path.Base(dir)]; h != "" && t.hash(dir) == h {
			out[dir] = true
		}
	}
	return out
}

type usageRecord struct {
	CreatedBy  string `json:"created_by"`
	LastUsedAt string `json:"last_used_at"`
}

func (t tree) usage() map[string]usageRecord {
	var raw map[string]json.RawMessage
	if json.Unmarshal(t.files[".usage.json"], &raw) != nil {
		return map[string]usageRecord{}
	}
	out := map[string]usageRecord{}
	for k, v := range raw {
		var r usageRecord
		if json.Unmarshal(v, &r) == nil {
			out[k] = r
		}
	}
	return out
}

// config 是 /hermes/config.yaml（讀不到或壞掉時是空的）。
func readConfig(ctx context.Context, f boxfs.FS) (map[string]any, error) {
	raw, err := f.ReadFile(ctx, configPath)
	if errors.Is(err, boxfs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if yaml.Unmarshal(raw, &cfg) != nil || cfg == nil {
		cfg = map[string]any{}
	}
	return cfg, nil
}

func writeConfig(ctx context.Context, f boxfs.FS, cfg map[string]any) error {
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return f.WriteFiles(ctx, hermes.HomeDir, []boxfs.File{{Name: hermes.ConfigFile, Data: out, Mode: 0o600}})
}

func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	if list, ok := v.([]any); ok {
		for _, x := range list {
			if s, ok := x.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}

func sortedList(m map[string]bool) []any {
	out := []any{}
	for _, k := range mapsKeys(m) {
		out = append(out, k)
	}
	return out
}

func skillsCfg(cfg map[string]any) map[string]any {
	s, ok := cfg["skills"].(map[string]any)
	if !ok {
		s = map[string]any{}
		cfg["skills"] = s
	}
	return s
}

// places 讀 config 裡的開關：停用的名稱、技能庫的名稱。
func places(cfg map[string]any) (disabled, library map[string]bool) {
	s, _ := cfg["skills"].(map[string]any)
	return stringSet(s["disabled"]), stringSet(cfg[libraryKey])
}

// setPlace 改一個技能的位置（同 LazyBoy set_place）；rel 是它的資料夾（舊版用資料夾當停用的名稱，一併清掉）。
func setPlace(cfg map[string]any, name, rel string, p Place) {
	disabled, library := places(cfg)
	delete(disabled, name)
	delete(disabled, rel)
	delete(library, name)
	switch p {
	case PlaceOff:
		disabled[name] = true
	case PlaceLibrary:
		disabled[name] = true
		library[name] = true
	}
	skillsCfg(cfg)["disabled"] = sortedList(disabled)
	cfg[libraryKey] = sortedList(library)
}

// Bot 是一個 Bot 的技能（讀寫它電腦裡的檔案）。
type Bot struct {
	FS    boxfs.FS
	BotID string
}

// List 列出 Bot 的所有技能。
func (b Bot) List(ctx context.Context) ([]Skill, error) {
	t, err := readTree(ctx, b.FS)
	if err != nil {
		return nil, err
	}
	cfg, err := readConfig(ctx, b.FS)
	if err != nil {
		return nil, err
	}
	return b.list(t, cfg), nil
}

func (b Bot) list(t tree, cfg map[string]any) []Skill {
	disabled, library := places(cfg)
	builtin := t.bundledUnchanged()
	var st state
	_ = json.Unmarshal(t.files[stateFile], &st)
	usage := t.usage()
	out := []Skill{}
	for _, dir := range t.dirs() {
		d := Parse(string(t.files[dir+"/SKILL.md"]))
		name := d.Name
		if name == "" {
			name = path.Base(dir)
		}
		place := PlaceList
		switch {
		case library[name]:
			place = PlaceLibrary
		case disabled[name] || disabled[dir]:
			place = PlaceOff
		}
		source := "custom"
		switch {
		case builtin[dir]:
			source = "hermes"
		case slices.Contains(st.Seeded, dir):
			source = "xbot"
		case st.Sources[dir] != "":
			source = st.Sources[dir]
		case usage[name].CreatedBy == "agent":
			source = "learned"
		}
		var files []string
		for name := range t.files {
			if strings.HasPrefix(name, dir+"/") && name != dir+"/SKILL.md" {
				rel := strings.TrimPrefix(name, dir+"/")
				if !slices.ContainsFunc(strings.Split(rel, "/"), func(p string) bool { return excludedDirs[p] }) {
					files = append(files, rel)
				}
			}
		}
		sort.Strings(files)
		out = append(out, Skill{ID: dir, Name: name, Description: d.Description, Instructions: d.Instructions, Place: place,
			Enabled: place != PlaceOff, Source: source, Files: files, LastUsedAt: usage[name].LastUsedAt})
	}
	return out
}

// ErrNotFound 表示找不到這個技能。
var ErrNotFound = errors.New("找不到這個技能")

// ErrExists 表示同名的技能已經在了。
var ErrExists = errors.New("已經有同名的技能")

// Get 讀一個技能（id 是資料夾，也接受技能名稱）。
func (b Bot) Get(ctx context.Context, id string) (Skill, error) {
	all, err := b.List(ctx)
	if err != nil {
		return Skill{}, err
	}
	return find(all, id)
}

func find(all []Skill, id string) (Skill, error) {
	for _, s := range all {
		if s.ID == id {
			return s, nil
		}
	}
	for _, s := range all {
		if s.Name == id || path.Base(s.ID) == id {
			return s, nil
		}
	}
	return Skill{}, ErrNotFound
}

// Edit 是新增或修改技能的內容；nil 的欄位不改。
type Edit struct {
	Name         *string
	Description  *string
	Instructions *string
	// Place 只在新增時用（空白＝技能清單）。
	Place Place
	// Source 只在新增時用：imported、proposed（空白＝custom）。
	Source string
	// Files 是附帶的檔案（匯入、從技能庫裝上時）。
	Files []boxfs.File
}

func randomHex() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Create 新增一個技能，回傳它。
func (b Bot) Create(ctx context.Context, e Edit) (Skill, error) {
	d := Doc{Name: deref(e.Name), Description: deref(e.Description), Instructions: deref(e.Instructions)}
	d.Name, d.Description = oneLine(d.Name), oneLine(d.Description)
	if err := d.Validate(); err != nil {
		return Skill{}, err
	}
	defer hermes.LockConfig(b.BotID)()
	t, err := readTree(ctx, b.FS)
	if err != nil {
		return Skill{}, err
	}
	cfg, err := readConfig(ctx, b.FS)
	if err != nil {
		return Skill{}, err
	}
	all := b.list(t, cfg)
	if slices.ContainsFunc(all, func(s Skill) bool { return s.Name == d.Name }) {
		return Skill{}, fmt.Errorf("%w：%s", ErrExists, d.Name)
	}
	dir := DirName(d.Name, randomHex)
	for slices.ContainsFunc(all, func(s Skill) bool { return s.ID == dir }) || t.files[dir+"/SKILL.md"] != nil {
		dir = "skill-" + randomHex()
	}
	files := []boxfs.File{{Name: dir + "/SKILL.md", Data: []byte(d.String()), Mode: 0o644}}
	for _, f := range e.Files {
		files = append(files, boxfs.File{Name: dir + "/" + f.Name, Data: f.Data, Mode: f.Mode})
	}
	if e.Source != "" {
		var st state
		_ = json.Unmarshal(t.files[stateFile], &st)
		if st.Sources == nil {
			st.Sources = map[string]string{}
		}
		st.Sources[dir] = e.Source
		raw, err := json.MarshalIndent(st, "", "  ")
		if err != nil {
			return Skill{}, err
		}
		files = append(files, boxfs.File{Name: stateFile, Data: raw, Mode: 0o644})
	}
	if err := b.FS.WriteFiles(ctx, SkillsDir, files); err != nil {
		return Skill{}, err
	}
	place := e.Place
	if place == "" {
		place = PlaceList
	}
	setPlace(cfg, d.Name, dir, place)
	if err := writeConfig(ctx, b.FS, cfg); err != nil {
		return Skill{}, err
	}
	return b.Get(ctx, dir)
}

// Update 改一個技能的名稱、用途或內文。frontmatter 的其他欄位保留；位置不變（技能庫的還在技能庫，LazyBoy 同）。
func (b Bot) Update(ctx context.Context, id string, e Edit) (Skill, error) {
	defer hermes.LockConfig(b.BotID)()
	t, err := readTree(ctx, b.FS)
	if err != nil {
		return Skill{}, err
	}
	cfg, err := readConfig(ctx, b.FS)
	if err != nil {
		return Skill{}, err
	}
	all := b.list(t, cfg)
	prev, err := find(all, id)
	if err != nil {
		return Skill{}, err
	}
	d := Parse(string(t.files[prev.ID+"/SKILL.md"]))
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
		return Skill{}, err
	}
	if d.Name != prev.Name && slices.ContainsFunc(all, func(s Skill) bool { return s.Name == d.Name && s.ID != prev.ID }) {
		return Skill{}, fmt.Errorf("%w：%s", ErrExists, d.Name)
	}
	if err := b.FS.WriteFiles(ctx, SkillsDir, []boxfs.File{{Name: prev.ID + "/SKILL.md", Data: []byte(d.String()), Mode: 0o644}}); err != nil {
		return Skill{}, err
	}
	if d.Name != prev.Name {
		setPlace(cfg, prev.Name, prev.ID, PlaceList) // 舊名稱不留在設定裡
		setPlace(cfg, d.Name, prev.ID, prev.Place)
		if err := writeConfig(ctx, b.FS, cfg); err != nil {
			return Skill{}, err
		}
	}
	return b.Get(ctx, prev.ID)
}

// SetPlace 把技能放到技能清單、技能庫或停用。只改設定，不動 SKILL.md（改檔案會讓 Hermes 內建技能看起來被改過，就收不到更新）。
func (b Bot) SetPlace(ctx context.Context, id string, p Place) (Skill, error) {
	defer hermes.LockConfig(b.BotID)()
	t, err := readTree(ctx, b.FS)
	if err != nil {
		return Skill{}, err
	}
	cfg, err := readConfig(ctx, b.FS)
	if err != nil {
		return Skill{}, err
	}
	prev, err := find(b.list(t, cfg), id)
	if err != nil {
		return Skill{}, err
	}
	setPlace(cfg, prev.Name, prev.ID, p)
	if err := writeConfig(ctx, b.FS, cfg); err != nil {
		return Skill{}, err
	}
	prev.Place, prev.Enabled = p, p != PlaceOff
	return prev, nil
}

// Delete 刪掉技能的資料夾，名稱也從設定清掉（LazyBoy：刪掉的技能不能留在停用或技能庫清單裡）。
func (b Bot) Delete(ctx context.Context, id string) (Skill, error) {
	defer hermes.LockConfig(b.BotID)()
	t, err := readTree(ctx, b.FS)
	if err != nil {
		return Skill{}, err
	}
	cfg, err := readConfig(ctx, b.FS)
	if err != nil {
		return Skill{}, err
	}
	prev, err := find(b.list(t, cfg), id)
	if err != nil {
		return Skill{}, err
	}
	if err := b.FS.Remove(ctx, path.Join(SkillsDir, prev.ID)); err != nil {
		return Skill{}, err
	}
	setPlace(cfg, prev.Name, prev.ID, PlaceList)
	if err := writeConfig(ctx, b.FS, cfg); err != nil {
		return Skill{}, err
	}
	var st state
	if json.Unmarshal(t.files[stateFile], &st) == nil && st.Sources[prev.ID] != "" {
		delete(st.Sources, prev.ID)
		if err := writeState(ctx, b.FS, st); err != nil {
			return Skill{}, err
		}
	}
	return prev, nil
}

// Search 找技能庫裡的技能（Bot 用；LazyBoy library_search）：名稱命中 3 分、資料夾或用途命中 1 分。
func Search(all []Skill, query string) []Skill {
	words := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return strings.ContainsRune(" \t\n,，、/|", r) })
	if len(words) == 0 {
		return all
	}
	type scored struct {
		s     Skill
		score int
	}
	var hits []scored
	for _, s := range all {
		name, text := strings.ToLower(s.Name), strings.ToLower(s.ID+" "+s.Description)
		n := 0
		for _, w := range words {
			if strings.Contains(name, w) {
				n += 3
			}
			if strings.Contains(text, w) {
				n++
			}
		}
		if n > 0 {
			hits = append(hits, scored{s, n})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := []Skill{}
	for i, h := range hits {
		if i >= limits.SkillLibraryResults {
			break
		}
		out = append(out, h.s)
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
