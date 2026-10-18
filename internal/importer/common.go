package importer

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"xbot/internal/boxfs"
	"xbot/internal/limits"
	"xbot/internal/proxy"
	"xbot/internal/skills"
)

// readSkillDirs 找 root 底下每個有 SKILL.md 的資料夾（略過 . 開頭的），skip 回答要不要略過（rel 是相對 root 的路徑）。
func readSkillDirs(root string, skip func(rel, dir string) bool) ([]Skill, error) {
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != root && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == "SKILL.md" && d.Type().IsRegular() {
			dirs = append(dirs, filepath.Dir(p))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(dirs)
	var out []Skill
	for _, dir := range dirs {
		rel, _ := filepath.Rel(root, dir)
		rel = filepath.ToSlash(rel)
		if rel == "." || (skip != nil && skip(rel, dir)) {
			continue
		}
		if s, ok := readSkill(dir); ok {
			out = append(out, s)
		}
		if len(out) >= limits.ImportSkillsPerBot {
			break
		}
	}
	return out, nil
}

// readSkill 讀一個技能資料夾：SKILL.md 與附帶的檔案（太大的技能不帶）。
func readSkill(dir string) (Skill, bool) {
	raw, err := readSmall(filepath.Join(dir, "SKILL.md"), limits.ImportSkillBytes)
	if err != nil {
		return Skill{}, false
	}
	d := skills.Parse(string(raw))
	s := Skill{Name: strings.TrimSpace(d.Name), Description: d.Description, Instructions: d.Instructions, Bytes: int64(len(raw))}
	if s.Name == "" {
		s.Name = filepath.Base(dir)
	}
	if strings.TrimSpace(s.Instructions) == "" {
		return Skill{}, false
	}
	_ = filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() {
			if p != dir && (strings.HasPrefix(e.Name(), ".") || slices.Contains(cacheDirs, e.Name())) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "SKILL.md" || !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".") {
			return nil
		}
		info, err := e.Info()
		if err != nil || s.Bytes+info.Size() > limits.ImportSkillBytes {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		s.Bytes += int64(len(data))
		s.Files = append(s.Files, boxfs.File{Name: rel, Data: data, Mode: int64(info.Mode().Perm())})
		return nil
	})
	return s, true
}

// skipCaches 略過快取資料夾。
func skipCaches(rel string, d fs.DirEntry) bool {
	if !d.IsDir() {
		return false
	}
	n := d.Name()
	return slices.Contains(cacheDirs, n) || n == "node_modules" || n == ".cache"
}

// browserCaches 是瀏覽器設定檔裡的快取（可以重建，不影響登入）。
var browserCaches = []string{"Cache", "Code Cache", "GPUCache", "GrShaderCache", "ShaderCache", "GraphiteDawnCache", "DawnCache",
	"component_crx_cache", "Crashpad", "CacheStorage", "ScriptCache"}

func skipBrowserCaches(rel string, d fs.DirEntry) bool {
	if d.IsDir() {
		return slices.Contains(browserCaches, d.Name())
	}
	// 執行中的鎖（SingletonLock 等）
	return strings.HasPrefix(d.Name(), "Singleton")
}

var badProviderID = regexp.MustCompile(`[^a-z0-9-]+`)

// keyOf 把舊系統的一個供應商換成 XBot 的：有同名的內建範本就用範本。
func keyOf(id, label, baseURL, protocol, apiKey string) Key {
	k := Key{Origin: cmpOr(label, id), Label: cmpOr(label, id), BaseURL: strings.TrimSpace(baseURL), APIKey: strings.TrimSpace(apiKey)}
	clean := strings.Trim(badProviderID.ReplaceAllString(strings.ToLower(id), "-"), "-")
	if len(clean) > 32 {
		clean = strings.Trim(clean[:32], "-")
	}
	if clean == "" {
		clean = "imported"
	}
	k.ID = clean
	if pre, ok := proxy.PresetByID(clean); ok {
		k.Preset, k.Label = pre.ID, cmpOr(label, pre.Label)
		if k.BaseURL == "" {
			k.BaseURL = pre.BaseURL
		}
		k.Transport = pre.Transport
	}
	if proxy.ValidTransport(protocol) && protocol != "" {
		k.Transport = protocol
	}
	return k
}

func cmpOr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return b
}
