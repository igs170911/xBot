package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// EnvPaths 是設定要找哪些資料夾的環境變數（用 : 分隔）；設成 off 表示不自動找（測試用）。
const EnvPaths = "XBOT_IMPORT_PATHS"

// DefaultRoots 是預設會找的資料夾：LazyBoy 與 OpenMausBot 程式裡的預設位置，加上常見的放法。
func DefaultRoots() []string {
	if v, ok := os.LookupEnv(EnvPaths); ok {
		if strings.TrimSpace(v) == "off" {
			return nil
		}
		var out []string
		for _, p := range filepath.SplitList(v) {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	var out []string
	// LazyBoy：server/config.py LAZYHERMES_DATA_DIR，預設 <專案>/.data。
	if v := os.Getenv("LAZYHERMES_DATA_DIR"); v != "" {
		out = append(out, v)
	}
	// OpenMausBot：server/config.ts OMB_DATA_DIR，預設 ~/.openmausbot（更舊的版本是 ~/.opengrokbot）。
	if v := os.Getenv("OMB_DATA_DIR"); v != "" {
		out = append(out, v)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return out
	}
	out = append(out, filepath.Join(home, ".openmausbot"), filepath.Join(home, ".opengrokbot"))
	// 桌面版的其他工作環境：~/.openmausbot-<名稱>
	if more, err := filepath.Glob(filepath.Join(home, ".openmausbot-*")); err == nil {
		out = append(out, more...)
	}
	for _, repo := range []string{"LazyBoyHermes", "LazyBoy", "lazyboy", "lazyhermes"} {
		for _, parent := range []string{"", "Projects", "projects", "src", "code", "dev"} {
			out = append(out, filepath.Join(home, parent, repo, ".data"))
		}
	}
	return out
}

// Found 是找到的一份舊資料（xbot.import.detect）。
type Found struct {
	ID       string `json:"id" desc:"這份舊資料的代號（plan、run 用）"`
	Kind     string `json:"kind" desc:"lazyboy 或 openmausbot"`
	Label    string `json:"label" desc:"LazyBoy 或 OpenMausBot"`
	Path     string `json:"path"`
	Bots     int    `json:"bots"`
	Skills   int    `json:"skills" desc:"Bot 自己的技能（不含內建的）"`
	Memory   int    `json:"memory" desc:"有記憶的 Bot"`
	Routines int    `json:"routines"`
	Teams    int    `json:"teams"`
	Rooms    int    `json:"rooms"`
	Keys     int    `json:"keys" desc:"模型金鑰（要你勾選才帶）"`
	Logins   int    `json:"logins" desc:"有瀏覽器登入的 Bot（要你勾選才帶）"`
	Imported int    `json:"imported" desc:"已經帶進來的 Bot"`
	Summary  string `json:"summary" desc:"一句話，例：找到 LazyBoy 的 3 個 Bot、2 把金鑰和 4 個定時任務"`
}

// SourceID 是一份舊資料的代號：種類＋路徑的雜湊。
func SourceID(kind, path string) string {
	sum := sha256.Sum256([]byte(path))
	return kind + "-" + hex.EncodeToString(sum[:5])
}

// KindOf 回答資料夾是哪一種舊系統；都不是時回空白。
func KindOf(dir string) string {
	switch {
	case IsLazyBoy(dir):
		return KindLazyBoy
	case IsOpenMausBot(dir):
		return KindOpenMausBot
	}
	return ""
}

// ErrNotLegacy 表示指定的資料夾不是 LazyBoy 或 OpenMausBot 的資料目錄。
var ErrNotLegacy = errors.New("這個資料夾不是 LazyBoy 或 OpenMausBot 的資料目錄（LazyBoy 要有 product.sqlite3，OpenMausBot 要有 bots.json）")

// candidates 回傳要找的資料夾（絕對路徑、去掉重複）。extra 不是空白時只找它。
func (s *Service) candidates(extra string) []string {
	roots := s.Roots
	if extra != "" {
		roots = []string{extra}
	} else if roots == nil {
		roots = DefaultRoots()
	}
	var out []string
	for _, r := range roots {
		if strings.HasPrefix(r, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				r = filepath.Join(home, r[2:])
			}
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		if !slices.Contains(out, abs) {
			out = append(out, abs)
		}
	}
	return out
}

// stamp 是判斷舊資料有沒有變的時間（主要檔案的修改時間）。
func stamp(kind, dir string) time.Time {
	var files []string
	if kind == KindLazyBoy {
		files = []string{lbDB, lbDB + "-wal", "settings.json"}
	} else {
		files = []string{ombBots, "groups.json", "routines.json", "config.json"}
	}
	var t time.Time
	for _, f := range files {
		if st, err := os.Stat(filepath.Join(dir, f)); err == nil && st.ModTime().After(t) {
			t = st.ModTime()
		}
	}
	return t
}

type cached struct {
	stamp time.Time
	at    time.Time
	src   *Source
}

// read 讀一份舊資料；短時間內、檔案沒變時用上次讀的結果。
func (s *Service) read(ctx context.Context, kind, dir string) (*Source, error) {
	st := stamp(kind, dir)
	s.cacheMu.Lock()
	if c, ok := s.cache[dir]; ok && c.stamp.Equal(st) && time.Since(c.at) < cacheFor {
		s.cacheMu.Unlock()
		return c.src, nil
	}
	s.cacheMu.Unlock()
	var src *Source
	var err error
	switch kind {
	case KindLazyBoy:
		tmp, terr := os.MkdirTemp(s.TempDir, "xbot-import-")
		if terr != nil {
			return nil, terr
		}
		src, err = ReadLazyBoy(ctx, dir, tmp)
		_ = os.RemoveAll(tmp)
	case KindOpenMausBot:
		src, err = ReadOpenMausBot(dir)
	default:
		return nil, ErrNotLegacy
	}
	if err != nil {
		return nil, err
	}
	s.cacheMu.Lock()
	if s.cache == nil {
		s.cache = map[string]cached{}
	}
	s.cache[dir] = cached{stamp: st, at: time.Now(), src: src}
	s.cacheMu.Unlock()
	return src, nil
}

// cacheFor 是讀過的舊資料可以沿用多久（首頁與預演常常連著問）。
const cacheFor = 30 * time.Second

// Detect 找這台電腦上的舊資料。extra 不是空白時只看這個資料夾（不是舊資料時回 ErrNotLegacy）。
func (s *Service) Detect(ctx context.Context, extra string) ([]Found, error) {
	out := []Found{}
	for _, dir := range s.candidates(extra) {
		kind := KindOf(dir)
		if kind == "" {
			if extra != "" {
				return nil, ErrNotLegacy
			}
			continue
		}
		src, err := s.read(ctx, kind, dir)
		if err != nil {
			if extra != "" {
				return nil, err
			}
			s.log().Warn("讀舊資料失敗", "path", dir, "err", err)
			continue
		}
		f := s.summarize(ctx, src)
		if f.Bots == 0 && f.Keys == 0 && extra == "" {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

func (s *Service) summarize(ctx context.Context, src *Source) Found {
	f := Found{ID: SourceID(src.Kind, src.Path), Kind: src.Kind, Label: Labels[src.Kind], Path: src.Path, Bots: len(src.Bots),
		Routines: len(src.Routines), Teams: len(src.Teams), Rooms: len(src.Rooms), Keys: len(src.Keys)}
	for _, b := range src.Bots {
		f.Skills += len(b.Skills)
		if b.HasMemory() {
			f.Memory++
		}
		if b.Login != nil {
			f.Logins++
		}
	}
	if s.DB != nil {
		if m, err := loadMap(ctx, s.DB, src.Kind); err == nil {
			for _, b := range src.Bots {
				if _, ok := m.bot(ctx, s.DB, b.ID); ok {
					f.Imported++
				}
			}
		}
	}
	parts := []string{fmt.Sprintf("%d 個 Bot", f.Bots)}
	if f.Keys > 0 {
		parts = append(parts, fmt.Sprintf("%d 把金鑰", f.Keys))
	}
	if f.Routines > 0 {
		parts = append(parts, fmt.Sprintf("%d 個定時任務", f.Routines))
	}
	last := parts[len(parts)-1]
	text := last
	if len(parts) > 1 {
		text = strings.Join(parts[:len(parts)-1], "、") + "和 " + last
	}
	f.Summary = "在這台電腦找到 " + f.Label + " 的 " + text + "。"
	if f.Imported > 0 && f.Imported == f.Bots {
		f.Summary = f.Label + " 的 Bot 都帶進來了。"
	}
	return f
}

// resolve 依代號或路徑找到一份舊資料並讀出來。
func (s *Service) resolve(ctx context.Context, id, path string) (*Source, Found, error) {
	if path != "" {
		list, err := s.Detect(ctx, path)
		if err != nil {
			return nil, Found{}, err
		}
		if len(list) == 0 {
			return nil, Found{}, ErrNotLegacy
		}
		src, err := s.read(ctx, list[0].Kind, list[0].Path)
		return src, list[0], err
	}
	for _, dir := range s.candidates("") {
		kind := KindOf(dir)
		if kind == "" || SourceID(kind, dir) != id {
			continue
		}
		src, err := s.read(ctx, kind, dir)
		if err != nil {
			return nil, Found{}, err
		}
		return src, s.summarize(ctx, src), nil
	}
	return nil, Found{}, errNoSource
}

var errNoSource = errors.New("找不到這份舊資料；它可能被移走了，請重新偵測")
