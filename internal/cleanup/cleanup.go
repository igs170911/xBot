// Package cleanup 是例行清理（BX-14）：讓每個 Bot 的電腦不會被會自己長回來的東西塞滿。照 LazyBoy server/cleanup.py。
//
// 工作成果（/workspace）永遠不碰，只清：
//   - 引擎的暫存（/hermes/cache/scratch）超過 limits.CleanupScratchAge（3 天）沒動的檔案；
//   - XBot 新增：/workspace/tmp（Bot 放下載的原始網頁、中間資料的地方）裡同樣超過 3 天沒動的檔案；
//   - 你傳的附件（/workspace/uploads 底下每一項）放超過 limits.CleanupUploadAge（30 天）的；
//   - 瀏覽器快取超過 limits.CleanupBrowserCacheBytes（200 MB）時整個清掉。登入（cookies、local storage、IndexedDB）保留。
//
// LazyBoy 還會清「檔案預覽」（Office 轉成的 PDF，7 天）；XBot 的檔案預覽不轉檔、也沒有預覽快取，所以沒有這一項。
//
// 指令都經 box.Shell 在電腦裡（或沒開機時的輔助容器裡）以 Bot 使用者執行；這裡決定刪什麼、算清出多少。
package cleanup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"xbot/internal/box"
	"xbot/internal/limits"
)

// 各個資料夾（相對於 volume）。
const (
	scratchDir = "cache/scratch" // /hermes 底下：Hermes 的暫存（HERMES_HOME/cache/scratch）
	toolsDir   = "tools"         // /hermes 底下：Bot 安裝的工具
	uploadsDir = "uploads"       // /workspace 底下：你傳的附件
	tmpDir     = "tmp"           // /workspace 底下：Bot 的中間檔（XBot 新增）
)

// profiles 是瀏覽器設定檔的位置（相對於 /home/box），依序找第一個是真的資料夾的：
// box/box-chrome 用 chrome-profile；更早的版本在 .config/chromium，那時 chrome-profile 是連到它的連結。
var profiles = []string{"chrome-profile", ".config/chromium"}

// BrowserCaches 是瀏覽器設定檔裡 Chromium 會自己重建的快取（相對於設定檔）。出自 server/cleanup.py BROWSER_CACHES。
var BrowserCaches = []string{
	"Default/Cache", "Default/Code Cache", "Default/GPUCache", "Default/Service Worker/CacheStorage",
	"Default/DawnGraphiteCache", "Default/DawnWebGPUCache", "GPUPersistentCache", "GrShaderCache", "ShaderCache",
	"component_crx_cache", "extensions_crx_cache",
}

// Usage 是一個 Bot 的電腦空間花在哪裡（位元組）。出自 server/cleanup.py usage()。
type Usage struct {
	Workspace int64 `json:"workspace" desc:"工作成果（/workspace，不含附件與 tmp），不會自動刪除"`
	Uploads   int64 `json:"uploads" desc:"你傳的附件（/workspace/uploads），30 天後自動刪除"`
	Caches    int64 `json:"caches" desc:"快取與暫存（引擎暫存、/workspace/tmp、瀏覽器快取），每天自動清理"`
	Browser   int64 `json:"browser" desc:"瀏覽器登入資料與設定（不含快取）"`
	Tools     int64 `json:"tools" desc:"Bot 安裝的工具"`
	Other     int64 `json:"other" desc:"其他（引擎紀錄、家目錄的其他檔案）"`
	Total     int64 `json:"total" desc:"全部"`
}

// Result 是一次清理清出的空間（位元組）。
type Result struct {
	Scratch int64 `json:"scratch" desc:"引擎暫存與 /workspace/tmp"`
	Uploads int64 `json:"uploads" desc:"過期的附件"`
	Browser int64 `json:"browser" desc:"瀏覽器快取"`
	Freed   int64 `json:"freed" desc:"合計"`
}

// 在電腦裡執行的小程式（sh -c）。路徑都以參數傳入，不會被當成指令。
// nolink：從 $1 往上到 $2（不含）每一段都不能是連結；Bot 在暫存或附件的路徑上放了連結時不跟著走。
// （sh 的變數都是全域的，所以用 nl_ 開頭的名稱，不會蓋掉呼叫端的變數。）
const shNoLink = `nolink() { nl_p=$1; while [ "$nl_p" != "$2" ] && [ "$nl_p" != / ] && [ "$nl_p" != . ]; do [ -L "$nl_p" ] && return 1; nl_p=$(dirname -- "$nl_p"); done; return 0; }; `

const (
	// shSizes：參數是一組組「volume 根目錄 路徑」；每組印一行：路徑底下所有不是資料夾的項目大小合計（不跟著連結、不跨裝置）。
	// 不存在、或路徑上有連結時印 0。同 server/cleanup.py size()。
	shSizes = shNoLink + `while [ $# -ge 2 ]; do r=$1; p=$2; shift 2; ` +
		`if [ -d "$p" ] && nolink "$p" "$r"; then find "$p" -xdev ! -type d -printf '%s\n' 2>/dev/null | awk '{s+=$1} END {printf "%.0f\n", s}'; ` +
		`else echo 0; fi; done`

	// shProfile：印出瀏覽器設定檔是哪一個（$2… 依序，相對於 $1），沒有時印空行。
	shProfile = `r=$1; shift; for p in "$@"; do if [ -d "$r/$p" ] && [ ! -L "$r/$p" ]; then echo "$p"; exit 0; fi; done; echo`

	// shRemoveOld：$1 根目錄、$2 資料夾、$3 截止時間（秒）。刪掉比截止時間舊的檔案與連結（印出合計大小），
	// 再刪掉這次刪空的資料夾（剛刪掉的檔案所在的那一層，從深到淺；$2 本身不刪）、以及空了很久的資料夾；
	// 剛建好的空資料夾（例如正在用的）留著。同 server/cleanup.py _remove_old()。
	shRemoveOld = shNoLink + `r=$1; d=$2; cut=$3; [ -d "$d" ] && nolink "$d" "$r" || { echo 0; exit 0; }; ` +
		`emptied=$(mktemp) || exit 1; ` +
		`find "$d" -xdev -mindepth 1 \( -type f -o -type l \) ! -newermt "@$cut" -delete -printf '%s\n' -fprintf "$emptied" '%h\0' 2>/dev/null ` +
		`| awk '{s+=$1} END {printf "%.0f\n", s}'; ` +
		`sort -zur "$emptied" | grep -zvxF -- "$d" | xargs -0r rmdir --ignore-fail-on-non-empty -- 2>/dev/null; ` +
		`find "$d" -xdev -mindepth 1 -depth -type d -empty ! -newermt "@$cut" -delete 2>/dev/null; ` +
		`rm -f -- "$emptied"; exit 0`

	// shList：$1 根目錄、$2 資料夾、$3 最多幾項。列出底下每一項：種類、大小、修改時間、相對路徑（NUL 分隔）。
	shList = shNoLink + `[ -d "$2" ] && nolink "$2" "$1" || exit 0; ` +
		`find "$2" -xdev -mindepth 1 -printf '%y\t%s\t%T@\t%P\0' 2>/dev/null | head -z -n "$3"`

	// shRemoveNames：$1 根目錄、$2 資料夾，之後是要刪的項目名稱（只能是一層的名稱）。
	shRemoveNames = shNoLink + `r=$1; d=$2; shift 2; [ -d "$d" ] && nolink "$d" "$r" || exit 0; ` +
		`for n in "$@"; do case $n in */*|.|..|'') continue;; esac; rm -rf -- "$d/$n"; done`

	// shRemovePaths：$1 根目錄，之後是要整個刪掉的路徑（路徑上有連結的跳過）。
	shRemovePaths = shNoLink + `r=$1; shift; for t in "$@"; do { [ -e "$t" ] || [ -L "$t" ]; } || continue; ` +
		`nolink "$t" "$r" || continue; rm -rf -- "$t"; done`
)

// clearLiveCache 在開著的電腦裡請瀏覽器自己丟掉 HTTP 快取，而不是刪掉它正在用的檔案。出自 server/cleanup.py CLEAR_LIVE_CACHE。
const clearLiveCache = `
import { chromium } from '/opt/xbot/playwright/node_modules/playwright/index.mjs';
setTimeout(() => process.exit(1), 15000);
const browser = await chromium.connectOverCDP('http://127.0.0.1:9222', { timeout: 3000 });
const page = browser.contexts()[0]?.pages()[0];
if (!page) process.exit(2);
const session = await browser.contexts()[0].newCDPSession(page);
await session.send('Network.clearBrowserCache');
await session.detach();
process.exit(0);
`

// livePoll 是等瀏覽器在背景清快取時每次檢查的間隔（測試會改短）。
var livePoll = limits.CleanupLiveCachePoll

const sizesOutput = 64 << 10

func sh(script string, args ...string) []string {
	return append([]string{"sh", "-c", script, "sh"}, args...)
}

// sizes 量每個路徑底下的大小；pairs 是「根目錄、路徑」一組組。
func sizes(ctx context.Context, s box.Shell, pairs ...string) ([]int64, error) {
	out, err := s.Run(ctx, sizesOutput, sh(shSizes, pairs...)...)
	if err != nil {
		return nil, fmt.Errorf("量用量失敗：%w", err)
	}
	lines := strings.Fields(string(out))
	if len(lines) != len(pairs)/2 {
		return nil, fmt.Errorf("量用量失敗：輸出不對（%q）", out)
	}
	res := make([]int64, len(lines))
	for i, l := range lines {
		n, err := strconv.ParseInt(l, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("量用量失敗：輸出不對（%q）", l)
		}
		res[i] = n
	}
	return res, nil
}

// profile 回傳瀏覽器設定檔的完整路徑；沒有時空白。
func profile(ctx context.Context, s box.Shell) (string, error) {
	out, err := s.Run(ctx, 4096, sh(shProfile, append([]string{s.Home}, profiles...)...)...)
	if err != nil {
		return "", fmt.Errorf("找不到瀏覽器設定檔：%w", err)
	}
	p := strings.TrimSpace(string(out))
	if !slices.Contains(profiles, p) {
		return "", nil
	}
	return path.Join(s.Home, p), nil
}

// cachePairs 是瀏覽器快取的「根目錄、路徑」組。
func cachePairs(s box.Shell, prof string) []string {
	var pairs []string
	for _, c := range BrowserCaches {
		pairs = append(pairs, s.Home, path.Join(prof, c))
	}
	return pairs
}

func cacheSize(ctx context.Context, s box.Shell, prof string) (int64, error) {
	if prof == "" {
		return 0, nil
	}
	n, err := sizes(ctx, s, cachePairs(s, prof)...)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, v := range n {
		total += v
	}
	return total, nil
}

// Measure 量一個 Bot 的電腦空間花在哪裡。出自 server/cleanup.py usage()；XBot 的 /workspace/tmp 算在快取與暫存。
func Measure(ctx context.Context, s box.Shell) (Usage, error) {
	prof, err := profile(ctx, s)
	if err != nil {
		return Usage{}, err
	}
	pairs := []string{
		s.Hermes, s.Hermes, s.Hermes, path.Join(s.Hermes, scratchDir), s.Hermes, path.Join(s.Hermes, toolsDir),
		s.Home, s.Home,
		s.Workspace, s.Workspace, s.Workspace, path.Join(s.Workspace, uploadsDir), s.Workspace, path.Join(s.Workspace, tmpDir),
	}
	if prof != "" {
		pairs = append(pairs, s.Home, prof)
		pairs = append(pairs, cachePairs(s, prof)...)
	}
	n, err := sizes(ctx, s, pairs...)
	if err != nil {
		return Usage{}, err
	}
	hermes, scratch, tools, home, workspace, uploads, tmp := n[0], n[1], n[2], n[3], n[4], n[5], n[6]
	var browser, browserCache int64
	if prof != "" {
		browser = n[7]
		for _, v := range n[8:] {
			browserCache += v
		}
	}
	u := Usage{
		Workspace: workspace - uploads - tmp,
		Uploads:   uploads,
		Caches:    scratch + tmp + browserCache,
		Browser:   browser - browserCache,
		Tools:     tools,
		Other:     (hermes - scratch - tools) + (home - browser),
	}
	u.Total = u.Workspace + u.Uploads + u.Caches + u.Browser + u.Tools + u.Other
	return u, nil
}

// Options 決定一次清理做哪些事。
type Options struct {
	// Uploads：也刪過期的附件。每天的例行清理會；你按的「清理」不會（只清會自己長回來的快取與暫存）。
	Uploads bool
}

// Clean 清一個 Bot 的電腦（見檔案開頭）。呼叫端要確認 Bot 沒有在工作。某一項失敗時其他照做，錯誤一起回傳。
// 出自 server/cleanup.py clean_bot()。
func Clean(ctx context.Context, s box.Shell, now time.Time, opt Options) (Result, error) {
	var res Result
	var errs []error
	scratchCut := now.Add(-limits.CleanupScratchAge)
	for _, d := range [][2]string{{s.Hermes, scratchDir}, {s.Workspace, tmpDir}} {
		n, err := removeOld(ctx, s, d[0], path.Join(d[0], d[1]), scratchCut)
		res.Scratch += n
		errs = append(errs, err)
	}
	if opt.Uploads {
		n, err := ExpireUploads(ctx, s, now)
		res.Uploads = n
		errs = append(errs, err)
	}
	n, err := ClearBrowserCache(ctx, s)
	res.Browser = n
	errs = append(errs, err)
	res.Freed = res.Scratch + res.Uploads + res.Browser
	return res, errors.Join(errs...)
}

// removeOld 刪掉 dir 底下比 cutoff 舊的檔案，以及它們留下的空資料夾；回傳清出多少。
func removeOld(ctx context.Context, s box.Shell, root, dir string, cutoff time.Time) (int64, error) {
	out, err := s.Run(ctx, sizesOutput, sh(shRemoveOld, root, dir, strconv.FormatInt(cutoff.Unix(), 10))...)
	if err != nil {
		return 0, fmt.Errorf("清理 %s 失敗：%w", dir, err)
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	return n, nil
}

// entry 是 /workspace/uploads 底下的一項（檔案或資料夾）。
type entry struct {
	name   string
	newest time.Time // 資料夾：裡面最新的一項（空的時候是它自己）；檔案：它自己
	own    time.Time
	size   int64
	inside bool // 資料夾裡面有東西
}

// ExpireUploads 刪掉放超過 limits.CleanupUploadAge 的附件：/workspace/uploads 底下的每一項（檔案，或整個資料夾看裡面最新的），
// 回傳清出多少。舊訊息裡的附件之後打開會是「找不到檔案」。出自 server/cleanup.py expire_uploads()。
// XBot 的附件直接放在 uploads 底下（LazyBoy 每次上傳一個資料夾），也沒有另外的上傳紀錄要刪。
func ExpireUploads(ctx context.Context, s box.Shell, now time.Time) (int64, error) {
	dir := path.Join(s.Workspace, uploadsDir)
	out, err := s.Run(ctx, limits.FilesListOutputBytes, sh(shList, s.Workspace, dir, strconv.Itoa(limits.FilesWalkScanMax))...)
	if err != nil {
		return 0, fmt.Errorf("讀附件清單失敗：%w", err)
	}
	cutoff := now.Add(-limits.CleanupUploadAge)
	var names []string
	var freed int64
	for _, e := range expired(out, limits.FilesWalkScanMax, cutoff) {
		names = append(names, e.name)
		freed += e.size
	}
	for chunk := range slices.Chunk(names, 100) {
		if _, err := s.Run(ctx, sizesOutput, sh(shRemoveNames, append([]string{s.Workspace, dir}, chunk...)...)...); err != nil {
			return 0, fmt.Errorf("刪除過期的附件失敗：%w", err)
		}
	}
	return freed, nil
}

// expired 讀 shList 的輸出（max 是最多幾項），挑出最新一項比 cutoff 舊的附件。
// 清單被截斷時，最後一項可能沒列完（裡面也許有新的檔案），不算它。
func expired(out []byte, max int, cutoff time.Time) []entry {
	var order []string
	byName := map[string]*entry{}
	count := 0
	for rec := range bytes.SplitSeq(out, []byte{0}) {
		f := strings.SplitN(string(rec), "\t", 4)
		if len(f) != 4 || f[3] == "" {
			continue
		}
		count++
		size, _ := strconv.ParseInt(f[1], 10, 64)
		var mt time.Time
		if sec, err := strconv.ParseFloat(f[2], 64); err == nil {
			mt = time.UnixMilli(int64(sec * 1000))
		}
		top, _, nested := strings.Cut(f[3], "/")
		e := byName[top]
		if e == nil {
			e = &entry{name: top}
			byName[top] = e
			order = append(order, top)
		}
		if f[0] != "d" {
			e.size += size
		}
		if !nested {
			e.own = mt
			continue
		}
		if !e.inside || mt.After(e.newest) {
			e.newest = mt
		}
		e.inside = true
	}
	if count >= max && len(order) > 0 {
		order = order[:len(order)-1]
	}
	var res []entry
	for _, name := range order {
		e := *byName[name]
		if !e.inside {
			e.newest = e.own
		}
		if !e.newest.IsZero() && e.newest.Before(cutoff) {
			res = append(res, e)
		}
	}
	return res
}

// ClearBrowserCache 在瀏覽器快取超過 limits.CleanupBrowserCacheBytes 時清掉：電腦開著時請開著的瀏覽器自己清
// （等它在背景清掉兩成以上，最多 limits.CleanupLiveCacheChecks 次），沒開機時直接刪資料夾。回傳清出多少。
// 出自 server/cleanup.py clear_browser_cache()；開著的瀏覽器沒回應時和 LazyBoy 一樣不硬刪，等下一次。
func ClearBrowserCache(ctx context.Context, s box.Shell) (int64, error) {
	prof, err := profile(ctx, s)
	if err != nil || prof == "" {
		return 0, err
	}
	before, err := cacheSize(ctx, s, prof)
	if err != nil {
		return 0, err
	}
	if before < limits.CleanupBrowserCacheBytes {
		return 0, nil
	}
	if s.Live {
		cctx, cancel := context.WithTimeout(ctx, limits.CleanupLiveCacheTimeout)
		_, err := s.Run(cctx, 4096, "node", "--input-type=module", "-e", clearLiveCache)
		cancel()
		if err != nil {
			slog.Info("開著的瀏覽器沒有清快取，等下一次", "err", err)
		} else {
			// 瀏覽器回覆後才在背景清空快取。
			for range limits.CleanupLiveCacheChecks {
				select {
				case <-ctx.Done():
					return 0, ctx.Err()
				case <-time.After(livePoll):
				}
				if n, err := cacheSize(ctx, s, prof); err == nil && n*5 < before*4 {
					break
				}
			}
		}
	} else {
		var paths []string
		for i, p := range cachePairs(s, prof) {
			if i%2 == 1 {
				paths = append(paths, p)
			}
		}
		if _, err := s.Run(ctx, sizesOutput, sh(shRemovePaths, append([]string{s.Home}, paths...)...)...); err != nil {
			return 0, fmt.Errorf("刪除瀏覽器快取失敗：%w", err)
		}
	}
	after, err := cacheSize(ctx, s, prof)
	if err != nil {
		return 0, err
	}
	return max(0, before-after), nil
}
