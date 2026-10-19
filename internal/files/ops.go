package files

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"xbot/internal/boxfs"
	"xbot/internal/collab"
	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// Service 是檔案面板（C33，FL-1）：瀏覽、預覽、上傳下載、整理 Bot 電腦裡的 /workspace。
type Service struct {
	// Locate 找 Bot 的 /workspace（DockerLocator，測試用 DirLocator）。
	Locate Locator
	// Carry 是 Bot 之間帶檔（X02）讀寫兩邊 /workspace 用的；nil 時 move_between_bots 回 internal。
	Carry collab.Files
	// TempDir 是上傳時暫存檔案的地方；空白用系統暫存資料夾。
	TempDir string
}

// ── 輸入輸出 ──────────────────────────────────────────

// Entry 是一個檔案或資料夾。
type Entry struct {
	Name      string `json:"name"`
	Path      string `json:"path" desc:"完整路徑，/workspace/…"`
	Dir       bool   `json:"dir"`
	Size      int64  `json:"size" desc:"位元組；資料夾是 0 或檔案系統的值"`
	Modified  int64  `json:"modified" desc:"最後修改時間（Unix 毫秒）"`
	Kind      Kind   `json:"kind"`
	Mime      string `json:"mime,omitempty"`
	Group     Group  `json:"group,omitempty" desc:"all=true 時：result 成果、upload 你上傳的、lesson 示範教學、peer 從隊友來的"`
	From      string `json:"from,omitempty" desc:"group=peer 時：哪個隊友帶來的"`
	Important bool   `json:"important" desc:"是 Bot 做出的成果（不是建置產物、快取、鎖定檔）"`
}

// ListIn 是 xbot.files.list 的輸入。
type ListIn struct {
	BotID  string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"Bot id；Bot 看自己的電腦時不用填"`
	Path   string `json:"path,omitempty" max:"1024" desc:"要看的資料夾，例：/workspace/報告；不填是 /workspace"`
	All    bool   `json:"all,omitempty" desc:"true：列出所有檔案（略過隱藏檔與套件資料夾，最新的在前，最多 500 個），忽略 path"`
	Hidden bool   `json:"hidden,omitempty" desc:"資料夾裡以 . 開頭的也列出"`
	Offset int    `json:"offset,omitempty" min:"0" max:"100000" desc:"從第幾項開始（分頁）"`
	Limit  int    `json:"limit,omitempty" min:"1" max:"500" desc:"最多幾項；不填 300"`
	Wake   bool   `json:"wake,omitempty" desc:"電腦沒開機時先開機（列檔案要在電腦裡執行指令）"`
}

// ListOut 是 xbot.files.list 的輸出。
type ListOut struct {
	BotID string `json:"bot_id"`
	Path  string `json:"path" desc:"這個資料夾的完整路徑"`
	// State：ready 列好了；off 電腦沒開機（帶 wake=true 再呼叫一次）；none 這個 Bot 還沒有電腦。
	State     string  `json:"state" enum:"ready,off,none" desc:"ready：列好了；off：電腦沒開機（wake=true 再試）；none：Bot 還沒有電腦"`
	Entries   []Entry `json:"entries" desc:"資料夾在前，再依名稱排序；all=true 時成果在前、再依修改時間新到舊"`
	Total     int     `json:"total" desc:"總項數（分頁用）"`
	Offset    int     `json:"offset"`
	Truncated bool    `json:"truncated" desc:"項目太多，只讀了前面一部分"`
}

// ReadIn 是 xbot.files.read 的輸入。
type ReadIn struct {
	BotID    string `json:"bot_id,omitempty" resource:"bot" max:"64" desc:"Bot id；Bot 看自己的電腦時不用填"`
	Path     string `json:"path" required:"true" max:"1024" desc:"檔案的完整路徑，例：/workspace/報告.md"`
	MaxBytes int64  `json:"max_bytes,omitempty" min:"1" max:"1048576" desc:"文字最多讀幾個位元組；不填 256 KB"`
	Sheet    string `json:"sheet,omitempty" max:"200" desc:"Excel：要看哪個工作表；不填第一個"`
}

// ReadOut 是 xbot.files.read 的輸出。
type ReadOut struct {
	Entry
	Text      string   `json:"text,omitempty" desc:"文字內容（文字、Markdown、程式碼、CSV 原始內容）"`
	Truncated bool     `json:"truncated" desc:"檔案比較大，只讀了前面"`
	Encoding  string   `json:"encoding,omitempty" desc:"utf-8 或 big5"`
	Binary    bool     `json:"binary" desc:"不是文字檔：只有資訊，請下載"`
	Language  string   `json:"language,omitempty" desc:"程式碼的語言（副檔名）"`
	Table     *Table   `json:"table,omitempty" desc:"CSV、Excel 的表格"`
	Archive   *Archive `json:"archive,omitempty" desc:"壓縮檔裡有什麼"`
	Note      string   `json:"note,omitempty" desc:"沒有預覽的原因，例：檔案太大"`
	Download  string   `json:"download_url" desc:"下載網址（GET）"`
	Inline    string   `json:"inline_url,omitempty" desc:"在瀏覽器直接顯示的網址（圖片、PDF、音訊、影片）"`
	Thumb     string   `json:"thumb_url,omitempty" desc:"縮圖網址（PNG、JPEG、GIF）"`
}

// PathIn 指定一個 Bot 的一個路徑。
type PathIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Path  string `json:"path" required:"true" max:"1024" desc:"完整路徑，例：/workspace/報告"`
}

// WriteIn 是 xbot.files.write 的輸入。
type WriteIn struct {
	BotID     string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Path      string `json:"path" required:"true" max:"1024" desc:"檔案的完整路徑，例：/workspace/筆記.md；缺的資料夾會一起建"`
	Content   string `json:"content" max:"524288" desc:"文字內容（UTF-8）"`
	Overwrite bool   `json:"overwrite,omitempty" desc:"已經有這個檔案時換掉（要你核准）；不帶時遇到同名回 conflict"`
}

// MoveIn 是 xbot.files.move 的輸入。
type MoveIn struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	From  string `json:"from" required:"true" max:"1024" desc:"原本的完整路徑"`
	To    string `json:"to" required:"true" max:"1024" desc:"新的完整路徑（改名或搬到別的資料夾）；已經存在時回 conflict"`
}

// DeleteOut 是 xbot.files.delete 的輸出。
type DeleteOut struct {
	Path    string `json:"path"`
	Deleted bool   `json:"deleted"`
}

// UploadIn 是 xbot.files.upload 的輸入。網頁與 HTTP 用 POST /api/v1/files.upload（multipart/form-data），內容不經過 JSON。
type UploadIn struct {
	BotID   string `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"Bot id"`
	Dir     string `json:"dir,omitempty" max:"1024" desc:"放到哪個資料夾；不填是 /workspace/uploads"`
	Name    string `json:"name" required:"true" max:"255" desc:"檔名；同名時自動加上 (2)、(3)…"`
	Content string `json:"content_base64,omitempty" secret:"true" max:"700000" desc:"檔案內容（base64）；大檔請用 HTTP multipart：POST /api/v1/files.upload?bot_id=…&dir=…"`
	Size    int64  `json:"size,omitempty" min:"0" desc:"multipart 上傳時由服務填入"`
}

// DownloadOut 是 xbot.files.download 的輸出。
type DownloadOut struct {
	Entry
	URL string `json:"url" desc:"下載網址（GET，同一個登入）"`
}

// CopyIn 是 xbot.files.move_between_bots 的輸入。
type CopyIn struct {
	BotID   string   `json:"bot_id" required:"true" resource:"bot" max:"64" desc:"檔案在哪個 Bot 的電腦"`
	ToBotID string   `json:"to_bot_id" required:"true" resource:"bot" max:"64" desc:"傳給哪個 Bot"`
	Paths   []string `json:"paths" required:"true" min:"1" max:"10" desc:"要傳的檔案（完整路徑，最多 10 個）"`
}

// Skipped 是沒有傳過去的檔案。
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// CopyOut 是 xbot.files.move_between_bots 的輸出。
type CopyOut struct {
	Files   []collab.Carried `json:"files" desc:"傳過去的檔案：在對方電腦的 /workspace/from/<寄件者>/…"`
	Skipped []Skipped        `json:"skipped"`
}

// ── 登錄 ──────────────────────────────────────────────

// Register 登錄 xbot.files.* 操作。s 是 nil 時只登錄定義。
func Register(reg *ops.Registry, s *Service) {
	need := func() error {
		if s == nil || s.Locate == nil {
			return ops.Internal(errors.New("檔案服務沒有啟動"))
		}
		return nil
	}
	ops.Register(reg, ops.Op[ListIn, ListOut]{
		Name: "xbot.files.list", Summary: "列出 Bot 電腦裡的檔案",
		Description: "看 /workspace 底下一個資料夾（資料夾在前、依名稱排序、分頁），或用 all=true 列出所有檔案（成果在前、新的在前）。" +
			"電腦沒開機時回 state=off，帶 wake=true 會先開機。" + teamReadNote,
		Scope: ops.ScopeRead, Risk: ops.RiskSafe, ForBot: true, TeamRead: true,
		Handle: func(ctx context.Context, call *ops.Call, in ListIn) (ListOut, error) {
			if err := need(); err != nil {
				return ListOut{}, err
			}
			id, err := self(call, in.BotID)
			if err != nil {
				return ListOut{}, err
			}
			return s.List(ctx, id, in, call.DryRun)
		},
	})
	ops.Register(reg, ops.Op[ReadIn, ReadOut]{
		Name: "xbot.files.read", Summary: "預覽 Bot 電腦裡的一個檔案",
		Description: "文字、Markdown、程式碼回傳內容（大檔只讀前面）；CSV、Excel 回傳前 1,000 列的表格；壓縮檔列出裡面有什麼；" +
			"圖片、PDF 與其他檔案回傳資訊與下載網址。經過連結（symlink）的路徑不開啟。" + teamReadNote,
		Scope: ops.ScopeRead, Risk: ops.RiskSafe, ForBot: true, TeamRead: true,
		Handle: func(ctx context.Context, call *ops.Call, in ReadIn) (ReadOut, error) {
			if err := need(); err != nil {
				return ReadOut{}, err
			}
			id, err := self(call, in.BotID)
			if err != nil {
				return ReadOut{}, err
			}
			return s.Read(ctx, id, in)
		},
	})
	ops.Register(reg, ops.Op[PathIn, DownloadOut]{
		Name: "xbot.files.download", Summary: "取得 Bot 電腦裡一個檔案的下載網址",
		Description: "回傳檔案資訊與下載網址（GET /api/v1/files.download?bot_id=…&path=…，用同一個登入或權杖）。",
		Scope:       ops.ScopeRead, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, _ *ops.Call, in PathIn) (DownloadOut, error) {
			if err := need(); err != nil {
				return DownloadOut{}, err
			}
			return s.Download(ctx, in)
		},
	})
	ops.Register(reg, ops.Op[WriteIn, Entry]{
		Name: "xbot.files.write", Summary: "在 Bot 電腦裡寫一個文字檔",
		Description: "建立文字檔（缺的資料夾一起建）。已經有同名檔案時回 conflict；帶 overwrite=true 換掉舊的，要你核准。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		RiskOf: func(in WriteIn) ops.Risk {
			if in.Overwrite {
				return ops.RiskConfirm
			}
			return ops.RiskSafe
		},
		Handle: func(ctx context.Context, call *ops.Call, in WriteIn) (Entry, error) {
			if err := need(); err != nil {
				return Entry{}, err
			}
			return s.Write(ctx, in, call.DryRun)
		},
	})
	ops.Register(reg, ops.Op[PathIn, Entry]{
		Name: "xbot.files.mkdir", Summary: "在 Bot 電腦裡建資料夾",
		Description: "可以一次建多層；已經有同名資料夾不算錯。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in PathIn) (Entry, error) {
			if err := need(); err != nil {
				return Entry{}, err
			}
			return s.Mkdir(ctx, in, call.DryRun)
		},
	})
	ops.Register(reg, ops.Op[MoveIn, Entry]{
		Name: "xbot.files.move", Summary: "搬移或改名 Bot 電腦裡的檔案",
		Description: "在同一台 Bot 電腦的 /workspace 裡搬移或改名（檔案或資料夾）。目的地已經存在時回 conflict，不會蓋掉。電腦沒開機時會先開機。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in MoveIn) (Entry, error) {
			if err := need(); err != nil {
				return Entry{}, err
			}
			return s.Move(ctx, in, call.DryRun)
		},
	})
	ops.Register(reg, ops.Op[PathIn, DeleteOut]{
		Name: "xbot.files.delete", Summary: "刪除 Bot 電腦裡的檔案",
		Description: "刪除一個檔案或整個資料夾，不能復原。代理或 Bot 呼叫時一律送到「待我處理」等你允許，不能代為核准。電腦沒開機時會先開機。",
		Scope:       ops.ScopeOperate, Risk: ops.RiskConfirm, NoDelegate: true,
		Handle: func(ctx context.Context, call *ops.Call, in PathIn) (DeleteOut, error) {
			if err := need(); err != nil {
				return DeleteOut{}, err
			}
			return s.Delete(ctx, in, call.DryRun)
		},
	})
	ops.Register(reg, ops.Op[UploadIn, Entry]{
		Name: "xbot.files.upload", Summary: "上傳檔案到 Bot 電腦",
		Description: fmt.Sprintf("放到 /workspace 的資料夾（預設 uploads）。一個檔案最多 %d MB（圖片 %d MB），一次最多 %d 個、合計 %d MB。"+
			"大檔請用 HTTP：POST /api/v1/files.upload?bot_id=…&dir=…（multipart/form-data，欄位 file）。",
			limits.UploadFileBytes>>20, limits.UploadImageBytes>>20, limits.UploadFilesMax, limits.UploadTotalBytes>>20),
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in UploadIn) (Entry, error) {
			if err := need(); err != nil {
				return Entry{}, err
			}
			return s.Upload(ctx, in, call.DryRun)
		},
	})
	ops.Register(reg, ops.Op[CopyIn, CopyOut]{
		Name: "xbot.files.move_between_bots", Summary: "把檔案傳給另一個 Bot",
		Description: fmt.Sprintf("複製到對方電腦的 /workspace/from/<寄件者>/（和 Bot 之間帶檔相同，X02）：對方已經有內容相同的檔案就不再複製。"+
			"原本的檔案留著。一次最多 %d 個、合計 %d MB；超過的略過並寫出原因。", limits.CarryFilesMax, limits.CarryBytesMax>>20),
		Scope: ops.ScopeOperate, Risk: ops.RiskSafe,
		Handle: func(ctx context.Context, call *ops.Call, in CopyIn) (CopyOut, error) {
			if err := need(); err != nil {
				return CopyOut{}, err
			}
			return s.CopyToBot(ctx, call.DB, in, call.DryRun)
		},
	})
}

// teamReadNote：主管 Bot 可以看下屬電腦裡的檔案（使用者回報 2026-10-19：主管列下屬的檔案被拒絕）。只能看，不能改。
const teamReadNote = "有團隊管理權限的 Bot 也能看向它報告的 Bot（直屬或間接）的電腦：bot_id 填對方的 id（xbot.teams.team_status 裡有），只能看、不能改。"

// self：Bot 只能看自己的電腦；有團隊管理權限的 Bot 也能看向它報告的 Bot（登錄表已經檢查過，見 ops.Op.TeamRead）。
func self(call *ops.Call, botID string) (string, error) {
	if call.Identity.Kind == ops.KindBot {
		if botID != "" && botID != call.Identity.ID {
			if call.Identity.ReadsReport(botID) {
				return botID, nil
			}
			return "", ops.Forbidden("只能看你自己電腦裡的檔案；有團隊管理權限時也能看向你報告的 Bot 的檔案")
		}
		return call.Identity.ID, nil
	}
	if botID == "" {
		return "", ops.InvalidInput(ops.FieldError{Field: "bot_id", Message: "要指定 Bot"})
	}
	return botID, nil
}

// opsErr 把檔案錯誤轉成操作的錯誤代碼。
func opsErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound), errors.Is(err, boxfs.ErrNotExist):
		return ops.NotFound(ErrNotFound.Error())
	case errors.Is(err, ErrLink), errors.Is(err, ErrOutside):
		return ops.Forbidden(err.Error())
	case errors.Is(err, ErrExists):
		return ops.Conflict(err.Error())
	case errors.Is(err, ErrIsDir), errors.Is(err, ErrNotDir):
		return ops.InvalidInput(ops.FieldError{Field: "path", Message: err.Error()})
	case errors.Is(err, ErrNoComputer):
		return ops.NotFound("這個 Bot 還沒有電腦：先跟它說句話，電腦開好後就有檔案了")
	case errors.Is(err, ErrNotRunning):
		return ops.Conflict("這個 Bot 的電腦沒有開機")
	}
	return err
}

func pathErr(field string, err error) error {
	if errors.Is(err, ErrOutside) {
		return ops.Forbidden(err.Error())
	}
	return ops.InvalidInput(ops.FieldError{Field: field, Message: err.Error()})
}

// entry 把 Info 變成輸出的 Entry。
func entry(i Info) Entry {
	e := Entry{Name: base(i.Rel), Path: Abs(i.Rel), Dir: i.Dir, Size: i.Size, Kind: KindFolder}
	if !i.Modified.IsZero() {
		e.Modified = i.Modified.UnixMilli()
	}
	if !i.Dir {
		e.Kind, e.Mime, e.Important = KindOf(e.Name), MimeOf(e.Name), Important(i.Rel)
	} else {
		e.Size = 0
	}
	return e
}

func (s *Service) locate(ctx context.Context, botID string, wake bool) (Workspace, error) {
	ws, err := s.Locate(ctx, botID, wake)
	return ws, opsErr(err)
}

// ── 列出 ──────────────────────────────────────────────

// List 列出一個資料夾或所有檔案。
func (s *Service) List(ctx context.Context, botID string, in ListIn, dryRun bool) (ListOut, error) {
	rel, err := Rel(in.Path)
	if err != nil {
		return ListOut{}, pathErr("path", err)
	}
	if in.All {
		rel = ""
	}
	out := ListOut{BotID: botID, Path: Abs(rel), State: "ready", Entries: []Entry{}, Offset: in.Offset}
	ws, err := s.Locate(ctx, botID, in.Wake && !dryRun)
	if errors.Is(err, ErrNoComputer) {
		out.State = "none"
		return out, nil
	}
	if errors.Is(err, ErrNotRunning) {
		out.State = "off"
		return out, nil
	}
	if err != nil {
		return ListOut{}, opsErr(err)
	}
	var infos []Info
	if in.All {
		infos, err = ws.Walk(ctx)
	} else {
		infos, err = ws.List(ctx, rel)
	}
	if errors.Is(err, ErrNotRunning) {
		out.State = "off"
		return out, nil
	}
	if err != nil {
		return ListOut{}, opsErr(err)
	}
	entries := make([]Entry, 0, len(infos))
	for _, i := range infos {
		if !in.All && !in.Hidden && strings.HasPrefix(base(i.Rel), ".") {
			continue
		}
		e := entry(i)
		if in.All {
			e.Group, e.From = GroupOf(i.Rel)
		}
		entries = append(entries, e)
	}
	if in.All {
		out.Truncated = len(infos) >= limits.FilesWalkScanMax
		slices.SortStableFunc(entries, func(a, b Entry) int {
			if a.Important != b.Important {
				if a.Important {
					return -1
				}
				return 1
			}
			if a.Modified != b.Modified {
				if a.Modified > b.Modified {
					return -1
				}
				return 1
			}
			return strings.Compare(a.Path, b.Path)
		})
		if len(entries) > limits.FilesScanMax {
			entries, out.Truncated = entries[:limits.FilesScanMax], true
		}
	} else {
		out.Truncated = len(infos) >= limits.FilesFolderScanMax
		slices.SortStableFunc(entries, func(a, b Entry) int {
			if a.Dir != b.Dir {
				if a.Dir {
					return -1
				}
				return 1
			}
			if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
				return c
			}
			return strings.Compare(a.Name, b.Name)
		})
	}
	out.Total = len(entries)
	limit := in.Limit
	if limit == 0 {
		limit = limits.FolderEntries
	}
	if in.Offset < len(entries) {
		out.Entries = entries[in.Offset:min(len(entries), in.Offset+limit)]
	}
	return out, nil
}

// ── 預覽與下載 ────────────────────────────────────────

// URL 是下載網址；inline 在瀏覽器裡直接顯示，thumb 是縮圖。
func URL(botID, p string, inline, thumb bool) string {
	q := url.Values{"bot_id": {botID}, "path": {p}}
	if inline {
		q.Set("inline", "1")
	}
	if thumb {
		q.Set("thumb", "1")
	}
	return "/api/v1/files.download?" + q.Encode()
}

// Thumbable 回答這個檔案能不能做縮圖。
func Thumbable(name string, size int64) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif":
		return size <= limits.UploadImageBytes
	}
	return false
}

// Inlinable 回答這個檔案能不能在瀏覽器裡直接顯示（HTML 這類會執行程式的一律下載）。
func Inlinable(name string) bool {
	switch KindOf(name) {
	case KindImage, KindPDF, KindAudio, KindVideo, KindText, KindMarkdown, KindCSV:
		return true
	}
	return false
}

// Read 預覽一個檔案。
func (s *Service) Read(ctx context.Context, botID string, in ReadIn) (ReadOut, error) {
	rel, err := Rel(in.Path)
	if err != nil {
		return ReadOut{}, pathErr("path", err)
	}
	ws, err := s.locate(ctx, botID, false)
	if err != nil {
		return ReadOut{}, err
	}
	rc, info, err := ws.Open(ctx, rel)
	if err != nil {
		return ReadOut{}, opsErr(err)
	}
	defer rc.Close()
	e := entry(info)
	out := ReadOut{Entry: e, Download: URL(botID, e.Path, false, false)}
	if Inlinable(e.Name) {
		out.Inline = URL(botID, e.Path, true, false)
	}
	if Thumbable(e.Name, e.Size) {
		out.Thumb = URL(botID, e.Path, false, true)
	}
	want := in.MaxBytes
	if want <= 0 {
		want = limits.FilePreviewBytes
	}
	switch e.Kind {
	case KindImage, KindPDF, KindAudio, KindVideo, KindOffice:
		out.Binary = true
		if e.Kind == KindOffice {
			out.Note = "這種檔案請下載後開啟"
		}
		return out, nil
	case KindSheet:
		ext := strings.ToLower(path.Ext(e.Name))
		if ext != ".xlsx" && ext != ".xlsm" {
			out.Binary, out.Note = true, "這種試算表請下載後開啟"
			return out, nil
		}
		if e.Size > limits.FileSheetBytes {
			out.Binary, out.Note = true, fmt.Sprintf("檔案超過 %d MB，不預覽，請下載", limits.FileSheetBytes>>20)
			return out, nil
		}
		data, _, err := ReadHead(rc, limits.FileSheetBytes)
		if err != nil {
			return ReadOut{}, err
		}
		out.Binary = true
		t, err := XLSX(data, in.Sheet)
		if err != nil {
			out.Note = err.Error()
			return out, nil
		}
		out.Table, out.Truncated = t, t.Truncated
		return out, nil
	case KindArchive:
		out.Binary = true
		if e.Size > limits.FileSheetBytes {
			out.Note = fmt.Sprintf("壓縮檔超過 %d MB，不列出內容，請下載", limits.FileSheetBytes>>20)
			return out, nil
		}
		data, _, err := ReadHead(rc, limits.FileSheetBytes)
		if err != nil {
			return ReadOut{}, err
		}
		a, err := ListArchive(data)
		if err != nil {
			out.Note = err.Error()
			return out, nil
		}
		out.Archive = a
		return out, nil
	}
	if e.Kind == KindCSV {
		want = limits.FilePreviewBytesMax
	}
	data, more, err := ReadHead(rc, want)
	if err != nil {
		return ReadOut{}, err
	}
	t := DecodeText(data, more)
	if t.Binary {
		out.Binary, out.Note = true, "不是文字檔，請下載"
		return out, nil
	}
	out.Text, out.Truncated, out.Encoding = t.Text, t.Truncated, t.Encoding
	switch e.Kind {
	case KindCSV:
		if tab, err := CSV(t.Text, e.Name, more); err == nil {
			out.Table = tab
		}
		// 原始內容只回前面一段（表格已經有了）。
		if int64(len(out.Text)) > limits.FilePreviewBytes {
			out.Text = strings.ToValidUTF8(out.Text[:limits.FilePreviewBytes], "")
			out.Truncated = true
		}
	case KindCode:
		out.Language = Language(e.Name)
	case KindOther:
		out.Kind = KindText // 看起來是文字的未知檔案
	}
	return out, nil
}

// Download 檢查檔案存在、是一般檔案，回傳資訊與下載網址。
func (s *Service) Download(ctx context.Context, in PathIn) (DownloadOut, error) {
	rel, err := Rel(in.Path)
	if err != nil {
		return DownloadOut{}, pathErr("path", err)
	}
	ws, err := s.locate(ctx, in.BotID, false)
	if err != nil {
		return DownloadOut{}, err
	}
	info, err := ws.Stat(ctx, rel)
	if err != nil {
		return DownloadOut{}, opsErr(err)
	}
	if info.Dir {
		return DownloadOut{}, opsErr(ErrIsDir)
	}
	e := entry(info)
	return DownloadOut{Entry: e, URL: URL(in.BotID, e.Path, false, false)}, nil
}

// ── 寫入與整理 ────────────────────────────────────────

// Write 寫一個文字檔。
func (s *Service) Write(ctx context.Context, in WriteIn, dryRun bool) (Entry, error) {
	rel, err := Rel(in.Path)
	if err != nil || rel == "" {
		if err == nil {
			err = ErrIsDir
		}
		return Entry{}, pathErr("path", err)
	}
	if !utf8.ValidString(in.Content) {
		return Entry{}, ops.InvalidInput(ops.FieldError{Field: "content", Message: "要是 UTF-8 文字"})
	}
	ws, err := s.locate(ctx, in.BotID, false)
	if err != nil {
		return Entry{}, err
	}
	cur, err := ws.Stat(ctx, rel)
	switch {
	case err == nil && cur.Dir:
		return Entry{}, opsErr(ErrIsDir)
	case err == nil && !in.Overwrite:
		return Entry{}, ops.Conflict("已經有這個檔案；要換掉請帶 overwrite=true")
	case err != nil && !errors.Is(err, ErrNotFound):
		return Entry{}, opsErr(err)
	}
	if dryRun {
		return entry(Info{Rel: rel, Size: int64(len(in.Content)), Modified: time.Now()}), nil
	}
	if err := ws.Write(ctx, rel, int64(len(in.Content)), strings.NewReader(in.Content)); err != nil {
		return Entry{}, opsErr(err)
	}
	return s.stat(ctx, ws, rel)
}

func (s *Service) stat(ctx context.Context, ws Workspace, rel string) (Entry, error) {
	info, err := ws.Stat(ctx, rel)
	if err != nil {
		return Entry{}, opsErr(err)
	}
	return entry(info), nil
}

// Mkdir 建資料夾。
func (s *Service) Mkdir(ctx context.Context, in PathIn, dryRun bool) (Entry, error) {
	rel, err := Rel(in.Path)
	if err != nil || rel == "" {
		if err == nil {
			err = ErrExists
		}
		return Entry{}, pathErr("path", err)
	}
	for _, part := range split(rel) {
		if Filename(part) != part {
			return Entry{}, ops.InvalidInput(ops.FieldError{Field: "path", Message: "資料夾名稱不能有控制字元、斜線，也不能以點或空白開頭結尾"})
		}
	}
	ws, err := s.locate(ctx, in.BotID, false)
	if err != nil {
		return Entry{}, err
	}
	if dryRun {
		return entry(Info{Rel: rel, Dir: true}), nil
	}
	if err := ws.Mkdir(ctx, rel); err != nil {
		return Entry{}, opsErr(err)
	}
	return s.stat(ctx, ws, rel)
}

// Move 搬移或改名。
func (s *Service) Move(ctx context.Context, in MoveIn, dryRun bool) (Entry, error) {
	from, err := Rel(in.From)
	if err != nil || from == "" {
		if err == nil {
			err = errors.New("不能搬移整個工作目錄")
		}
		return Entry{}, pathErr("from", err)
	}
	to, err := Rel(in.To)
	if err != nil || to == "" {
		if err == nil {
			err = ErrExists
		}
		return Entry{}, pathErr("to", err)
	}
	if Filename(base(to)) != base(to) {
		return Entry{}, ops.InvalidInput(ops.FieldError{Field: "to", Message: "名稱不能有控制字元、斜線，也不能以點或空白開頭結尾"})
	}
	if to == from || strings.HasPrefix(to, from+"/") {
		return Entry{}, ops.InvalidInput(ops.FieldError{Field: "to", Message: "不能搬到自己裡面"})
	}
	ws, err := s.locate(ctx, in.BotID, !dryRun)
	if err != nil {
		return Entry{}, err
	}
	info, err := ws.Stat(ctx, from)
	if err != nil {
		return Entry{}, opsErr(err)
	}
	if _, err := ws.Stat(ctx, to); err == nil {
		return Entry{}, opsErr(ErrExists)
	} else if !errors.Is(err, ErrNotFound) {
		return Entry{}, opsErr(err)
	}
	if dryRun {
		info.Rel = to
		return entry(info), nil
	}
	if err := ws.Move(ctx, from, to); err != nil {
		return Entry{}, opsErr(err)
	}
	return s.stat(ctx, ws, to)
}

// Delete 刪掉一個檔案或資料夾。
func (s *Service) Delete(ctx context.Context, in PathIn, dryRun bool) (DeleteOut, error) {
	rel, err := Rel(in.Path)
	if err != nil || rel == "" {
		if err == nil {
			err = errors.New("不能刪除整個工作目錄")
		}
		return DeleteOut{}, pathErr("path", err)
	}
	ws, err := s.locate(ctx, in.BotID, !dryRun)
	if err != nil {
		return DeleteOut{}, err
	}
	if _, err := ws.Stat(ctx, rel); err != nil && !errors.Is(err, ErrLink) {
		return DeleteOut{}, opsErr(err)
	}
	if dryRun {
		return DeleteOut{Path: Abs(rel)}, nil
	}
	if err := ws.Remove(ctx, rel); err != nil {
		return DeleteOut{}, opsErr(err)
	}
	return DeleteOut{Path: Abs(rel), Deleted: true}, nil
}

// ── 上傳 ──────────────────────────────────────────────

type uploadKey struct{}

// Stream 是 HTTP multipart 上傳的一個檔案內容（UploadHandler 放進 context，xbot.files.upload 讀出）。
type Stream struct {
	R    io.Reader
	Size int64
}

// WithStream 把上傳的內容放進 context。
func WithStream(ctx context.Context, st Stream) context.Context {
	return context.WithValue(ctx, uploadKey{}, st)
}

// SizeLimit 是一個上傳檔案的上限：圖片 20 MB（給模型看），其他 50 MB（LazyBoy UPLOAD_LIMITS）。
func SizeLimit(name string) int64 {
	if KindOf(name) == KindImage {
		return limits.UploadImageBytes
	}
	return limits.UploadFileBytes
}

// TooBig 是超過上傳上限的錯誤。
func TooBig(name string) error {
	if KindOf(name) == KindImage {
		return ops.Limit(fmt.Sprintf("圖片最多 %d MB：%s", limits.UploadImageBytes>>20, name))
	}
	return ops.Limit(fmt.Sprintf("一個檔案最多 %d MB：%s", limits.UploadFileBytes>>20, name))
}

// Upload 寫入一個上傳的檔案；同名時加上 (2)、(3)…。
func (s *Service) Upload(ctx context.Context, in UploadIn, dryRun bool) (Entry, error) {
	if strings.TrimSpace(in.Dir) == "" {
		in.Dir = Root + "/uploads"
	}
	dir, err := Rel(in.Dir)
	if err != nil {
		return Entry{}, pathErr("dir", err)
	}
	name := Filename(in.Name)
	var r io.Reader
	var size int64
	if st, ok := ctx.Value(uploadKey{}).(Stream); ok {
		r, size = st.R, st.Size
	} else {
		data, err := base64.StdEncoding.DecodeString(in.Content)
		if err != nil {
			return Entry{}, ops.InvalidInput(ops.FieldError{Field: "content_base64", Message: "不是有效的 base64"})
		}
		r, size = bytes.NewReader(data), int64(len(data))
	}
	if size > SizeLimit(name) {
		return Entry{}, TooBig(name)
	}
	ws, err := s.locate(ctx, in.BotID, false)
	if err != nil {
		return Entry{}, err
	}
	if dir != "" {
		if info, err := ws.Stat(ctx, dir); err == nil && !info.Dir {
			return Entry{}, opsErr(ErrNotDir)
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return Entry{}, opsErr(err)
		}
	}
	rel, err := s.free(ctx, ws, dir, name)
	if err != nil {
		return Entry{}, err
	}
	if dryRun {
		return entry(Info{Rel: rel, Size: size, Modified: time.Now()}), nil
	}
	if err := ws.Write(ctx, rel, size, r); err != nil {
		return Entry{}, opsErr(err)
	}
	return s.stat(ctx, ws, rel)
}

// free 找一個還沒用過的名稱：報告.pdf → 報告 (2).pdf → 報告 (3).pdf…
func (s *Service) free(ctx context.Context, ws Workspace, dir, name string) (string, error) {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i <= 100; i++ {
		n := name
		if i > 1 {
			n = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		rel := join(dir, n)
		_, err := ws.Stat(ctx, rel)
		if errors.Is(err, ErrNotFound) {
			return rel, nil
		}
		if err != nil {
			return "", opsErr(err)
		}
	}
	return "", ops.Conflict("同名的檔案太多了，請換個名稱")
}

// ── 傳給別的 Bot ──────────────────────────────────────

// CopyToBot 把檔案複製到另一個 Bot 的 /workspace/from/<寄件者>/（X02 帶檔）。
func (s *Service) CopyToBot(ctx context.Context, db *store.DB, in CopyIn, dryRun bool) (CopyOut, error) {
	if s.Carry == nil {
		return CopyOut{}, ops.Internal(errors.New("沒有設定 Bot 之間帶檔"))
	}
	if in.BotID == in.ToBotID {
		return CopyOut{}, ops.InvalidInput(ops.FieldError{Field: "to_bot_id", Message: "要傳給另一個 Bot"})
	}
	from, err := db.GetBot(ctx, in.BotID)
	if errors.Is(err, store.ErrNotFound) {
		return CopyOut{}, ops.NotFound("找不到這個 Bot")
	}
	if err != nil {
		return CopyOut{}, err
	}
	to, err := db.GetBot(ctx, in.ToBotID)
	if errors.Is(err, store.ErrNotFound) {
		return CopyOut{}, ops.NotFound("找不到要傳給的 Bot")
	}
	if err != nil {
		return CopyOut{}, err
	}
	out := CopyOut{Files: []collab.Carried{}, Skipped: []Skipped{}}
	var budget = limits.CarryBytesMax
	seen := map[string]bool{}
	for _, p := range in.Paths {
		rel, err := Rel(p)
		if err != nil || rel == "" {
			out.Skipped = append(out.Skipped, Skipped{Path: p, Reason: "路徑不在 /workspace 裡"})
			continue
		}
		if seen[rel] {
			continue
		}
		seen[rel] = true
		// 先用檔案面板的規則看一次：經過連結的不帶。
		ws, err := s.locate(ctx, in.BotID, false)
		if err != nil {
			return CopyOut{}, err
		}
		info, err := ws.Stat(ctx, rel)
		if err != nil || info.Dir {
			reason := "找不到這個檔案"
			switch {
			case err == nil:
				reason = "資料夾不能傳，請傳裡面的檔案"
			case errors.Is(err, ErrLink):
				reason = ErrLink.Error()
			}
			out.Skipped = append(out.Skipped, Skipped{Path: Abs(rel), Reason: reason})
			continue
		}
		if dryRun {
			out.Files = append(out.Files, collab.Carried{From: Abs(rel), To: Abs("from/" + collab.Filename(from.Name) + "/" + rel), Size: info.Size})
			continue
		}
		c, err := collab.CarryFile(ctx, s.Carry, from.ID, from.Name, to.ID, to.Name, rel, budget)
		if err != nil {
			reason := "複製失敗，請稍後再試"
			switch {
			case errors.Is(err, collab.ErrCarryLimit):
				reason = fmt.Sprintf("超過一次 %d MB 的上限", limits.CarryBytesMax>>20)
			case errors.Is(err, collab.ErrNoFile):
				reason = "找不到這個檔案（或對方還沒有電腦）"
			}
			out.Skipped = append(out.Skipped, Skipped{Path: Abs(rel), Reason: reason})
			continue
		}
		if !c.Reused {
			budget -= c.Size
		}
		out.Files = append(out.Files, c)
	}
	return out, nil
}
