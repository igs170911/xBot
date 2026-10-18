package files

import (
	"mime"
	"path"
	"strings"
)

// Kind 是檔案的種類（決定怎麼預覽、縮圖畫什麼）。
type Kind string

// 檔案種類。
const (
	KindFolder   Kind = "folder"
	KindImage    Kind = "image"
	KindPDF      Kind = "pdf"
	KindCSV      Kind = "csv"
	KindSheet    Kind = "sheet"    // Excel（.xlsx 可以預覽前幾列）
	KindMarkdown Kind = "markdown" // Markdown
	KindCode     Kind = "code"
	KindText     Kind = "text"
	KindOffice   Kind = "office" // Word、PowerPoint 等（下載後開啟）
	KindArchive  Kind = "archive"
	KindAudio    Kind = "audio"
	KindVideo    Kind = "video"
	KindOther    Kind = "other"
)

// EnumValues 讓 JSON Schema 列出可用值。
func (Kind) EnumValues() []string {
	return []string{"folder", "image", "pdf", "csv", "sheet", "markdown", "code", "text", "office", "archive", "audio", "video", "other"}
}

var kinds = map[string]Kind{}

func init() {
	add := func(k Kind, exts ...string) {
		for _, e := range exts {
			kinds[e] = k
		}
	}
	add(KindImage, ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp", ".avif", ".ico")
	add(KindPDF, ".pdf")
	add(KindCSV, ".csv", ".tsv")
	add(KindSheet, ".xlsx", ".xlsm", ".xls", ".ods", ".numbers")
	add(KindMarkdown, ".md", ".markdown", ".mdx")
	add(KindCode, ".go", ".py", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".json", ".jsonl", ".yaml", ".yml", ".toml", ".sh", ".bash",
		".zsh", ".rb", ".rs", ".java", ".kt", ".swift", ".c", ".h", ".cc", ".cpp", ".hpp", ".cs", ".php", ".css", ".scss", ".html", ".htm",
		".xml", ".sql", ".ini", ".cfg", ".conf", ".env", ".lua", ".r", ".pl", ".vue", ".svelte", ".dockerfile", ".makefile", ".ipynb")
	add(KindText, ".txt", ".log", ".text", ".rst", ".srt", ".vtt")
	add(KindOffice, ".doc", ".docx", ".docm", ".dot", ".dotx", ".odt", ".ott", ".rtf", ".pages", ".ppt", ".pptx", ".pptm", ".pps",
		".ppsx", ".odp", ".key")
	add(KindArchive, ".zip", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar")
	add(KindAudio, ".mp3", ".wav", ".m4a", ".ogg", ".flac", ".aac", ".opus")
	add(KindVideo, ".mp4", ".webm", ".mov", ".mkv", ".avi", ".m4v")
}

// KindOf 依副檔名決定種類。
func KindOf(name string) Kind {
	ext := strings.ToLower(path.Ext(name))
	if k, ok := kinds[ext]; ok {
		return k
	}
	switch strings.ToLower(name) {
	case "dockerfile", "makefile", "readme", "license":
		return KindText
	}
	return KindOther
}

// MimeOf 是檔名對應的 MIME 類型；不知道時是 application/octet-stream。
func MimeOf(name string) string {
	ext := strings.ToLower(path.Ext(name))
	switch KindOf(name) {
	case KindMarkdown:
		return "text/markdown; charset=utf-8"
	case KindCSV:
		if ext == ".tsv" {
			return "text/tab-separated-values; charset=utf-8"
		}
		return "text/csv; charset=utf-8"
	case KindText:
		return "text/plain; charset=utf-8"
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

// 以下照 LazyBoy server/files.py：哪些是 Bot 做出的成果、哪些是建置產物或工具狀態。

// pruneDirs 是不列出的套件資料夾：放進清單會先把上限塞滿。
var pruneDirs = set("node_modules", "__pycache__", "venv", "env", "site-packages", "bower_components", "target")

var noiseDirs = union(pruneDirs, set("dist", "build", "out", "coverage", "tmp", "temp", "cache", "logs", "vendor"))

var noiseSuffixes = []string{".pyc", ".pyo", ".o", ".so", ".a", ".class", ".log", ".tmp", ".swp", ".lock", ".map",
	".d.ts", ".tsbuildinfo", ".sqlite3-wal", ".sqlite3-shm", ".pid", ".bak"}

var noiseNames = set("package-lock.json", "yarn.lock", "pnpm-lock.yaml", "uv.lock", "poetry.lock", "cargo.lock",
	"go.sum", "thumbs.db", "desktop.ini", "license", "license.txt", "license.md")

var deliverables = set(".md", ".pdf", ".docx", ".xlsx", ".pptx", ".csv", ".html", ".txt", ".json",
	".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".mp4", ".webm", ".mp3", ".wav", ".zip")

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func union(a, b map[string]bool) map[string]bool {
	m := make(map[string]bool, len(a)+len(b))
	for k := range a {
		m[k] = true
	}
	for k := range b {
		m[k] = true
	}
	return m
}

// Important 回答 /workspace 底下的這個檔案是不是值得預設顯示的成果（LazyBoy files.important）。
func Important(rel string) bool {
	parts := split(rel)
	if len(parts) == 0 {
		return false
	}
	name := strings.ToLower(parts[len(parts)-1])
	for _, p := range parts[:len(parts)-1] {
		if noiseDirs[strings.ToLower(p)] {
			return false
		}
	}
	if noiseNames[name] {
		return false
	}
	for _, s := range noiseSuffixes {
		if strings.HasSuffix(name, s) {
			return false
		}
	}
	// 示範教學的原始錄影與截圖；給人看的是 steps.json。
	if parts[0] == "lessons" && (contains(parts, "shots") || strings.HasSuffix(name, ".jsonl")) {
		return false
	}
	return true
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Rank 越小越前面：放在上層的成果檔排在深處的原始碼前面（LazyBoy files.rank）。
func Rank(rel string) int {
	parts := split(rel)
	r := 10
	if deliverables[strings.ToLower(path.Ext(rel))] {
		r = 0
	}
	return r + min(len(parts)-1, 9)
}

// Group 是「全部檔案」的分組（E07、L19）。
type Group string

// 分組。
const (
	GroupResult Group = "result" // 成果
	GroupUpload Group = "upload" // 你上傳的
	GroupLesson Group = "lesson" // 示範教學
	GroupPeer   Group = "peer"   // 從隊友來的
)

// EnumValues 讓 JSON Schema 列出可用值。
func (Group) EnumValues() []string { return []string{"result", "upload", "lesson", "peer"} }

// GroupOf 依位置分組：uploads/ 是你上傳的、from/<隊友>/ 是隊友帶來的（X02）、lessons/ 是示範教學，其他是成果。
// 隊友帶來的檔案同時回傳隊友的名稱。
func GroupOf(rel string) (Group, string) {
	parts := split(rel)
	if len(parts) < 2 {
		return GroupResult, ""
	}
	switch parts[0] {
	case "uploads":
		return GroupUpload, ""
	case "lessons":
		return GroupLesson, ""
	case "from":
		if len(parts) >= 3 {
			return GroupPeer, parts[1]
		}
	}
	return GroupResult, ""
}
