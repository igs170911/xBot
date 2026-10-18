package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"

	"xbot/internal/limits"
	"xbot/internal/ops"
	"xbot/internal/store"
)

// HTTP 出口（api.Options.Streams）：檔案內容不經過 JSON。身分由 api 驗證後傳進來；
// 權限、資源範圍、稽核、事件都經操作登錄表（xbot.files.upload／xbot.files.download），和其他出口一樣。

// sandboxed 是 Bot 做出來的內容在瀏覽器直接打開時的限制：不執行程式、不碰這個網站的登入與儲存（LazyBoy SANDBOXED）。
const sandboxed = "sandbox; default-src 'none'; img-src data: blob:; style-src 'unsafe-inline'; font-src data:; media-src 'self'"

// UploadHandler：POST /api/v1/files.upload?bot_id=…&dir=…，multipart/form-data，每個檔案一個 part（欄位名不限）。
// 每個檔案邊收邊寫進暫存檔（超過上限就停），再經 xbot.files.upload 串流寫進 Bot 的電腦。回 {"files":[Entry…]}。
func (s *Service) UploadHandler(reg *ops.Registry) func(w http.ResponseWriter, r *http.Request, id ops.Identity) error {
	return func(w http.ResponseWriter, r *http.Request, id ops.Identity) error {
		mt, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mt != "multipart/form-data" || params["boundary"] == "" {
			return ops.InvalidInput(ops.FieldError{Message: "上傳要用 multipart/form-data"})
		}
		q := r.URL.Query()
		botID, dir := q.Get("bot_id"), q.Get("dir")
		// 合計上限外加一點 multipart 的標頭。
		body := http.MaxBytesReader(w, r.Body, limits.UploadTotalBytes+(1<<20))
		mr := multipart.NewReader(body, params["boundary"])
		var out []Entry
		var total int64
		count := 0
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return uploadErr(err)
			}
			if part.FileName() == "" {
				// 一般欄位：bot_id、dir 也可以放在表單裡。
				v, _ := io.ReadAll(io.LimitReader(part, 2048))
				switch part.FormName() {
				case "bot_id":
					botID = strings.TrimSpace(string(v))
				case "dir":
					dir = strings.TrimSpace(string(v))
				}
				continue
			}
			count++
			if count > limits.UploadFilesMax {
				return ops.Limit(fmt.Sprintf("一次最多上傳 %d 個檔案", limits.UploadFilesMax))
			}
			e, n, err := s.uploadPart(r, reg, id, part, botID, dir, limits.UploadTotalBytes-total)
			if err != nil {
				return err
			}
			total += n
			out = append(out, e)
		}
		if count == 0 {
			return ops.InvalidInput(ops.FieldError{Field: "file", Message: "沒有收到檔案"})
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		return json.NewEncoder(w).Encode(map[string]any{"files": out})
	}
}

func uploadErr(err error) error {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		return ops.Limit(fmt.Sprintf("一次上傳合計最多 %d MB", limits.UploadTotalBytes>>20))
	}
	return ops.InvalidInput(ops.FieldError{Message: "上傳的內容不完整，請再試一次"})
}

func (s *Service) uploadPart(r *http.Request, reg *ops.Registry, id ops.Identity, part *multipart.Part, botID, dir string, left int64) (Entry, int64, error) {
	name := Filename(part.FileName())
	limit := min(SizeLimit(name), left)
	tmp, err := os.CreateTemp(s.TempDir, "xbot-upload-*")
	if err != nil {
		return Entry{}, 0, ops.Internal(err)
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	n, err := io.Copy(tmp, io.LimitReader(part, limit+1))
	if err != nil {
		return Entry{}, 0, uploadErr(err)
	}
	if n > limit {
		if limit < SizeLimit(name) {
			return Entry{}, 0, ops.Limit(fmt.Sprintf("一次上傳合計最多 %d MB", limits.UploadTotalBytes>>20))
		}
		return Entry{}, 0, TooBig(name)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return Entry{}, 0, ops.Internal(err)
	}
	raw, _ := json.Marshal(UploadIn{BotID: botID, Dir: dir, Name: name, Size: n})
	res, err := reg.Invoke(WithStream(r.Context(), Stream{R: tmp, Size: n}), id, "xbot.files.upload", raw, ops.Options{RequestID: store.NewID()})
	if err != nil {
		return Entry{}, 0, err
	}
	var e Entry
	if err := json.Unmarshal(res.Output, &e); err != nil {
		return Entry{}, 0, ops.Internal(err)
	}
	return e, n, nil
}

// DownloadHandler：GET /api/v1/files.download?bot_id=…&path=…[&inline=1][&thumb=1]。
// 先經 xbot.files.download 檢查權限與路徑（不能離開 /workspace、不經過連結、要是一般檔案），再串流檔案內容。
// 預設以附件下載；inline=1 只對圖片、PDF、音訊、影片、純文字有效（HTML 這類一律下載），並加上 sandbox。
func (s *Service) DownloadHandler(reg *ops.Registry) func(w http.ResponseWriter, r *http.Request, id ops.Identity) error {
	return func(w http.ResponseWriter, r *http.Request, id ops.Identity) error {
		q := r.URL.Query()
		in := PathIn{BotID: q.Get("bot_id"), Path: q.Get("path")}
		raw, _ := json.Marshal(in)
		res, err := reg.Invoke(r.Context(), id, "xbot.files.download", raw, ops.Options{})
		if err != nil {
			return err
		}
		var meta DownloadOut
		if err := json.Unmarshal(res.Output, &meta); err != nil {
			return ops.Internal(err)
		}
		rel, _ := Rel(meta.Path)
		ws, err := s.locate(r.Context(), in.BotID, false)
		if err != nil {
			return err
		}
		rc, info, err := ws.Open(r.Context(), rel)
		if err != nil {
			return opsErr(err)
		}
		defer rc.Close()
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if q.Get("thumb") == "1" {
			if !Thumbable(meta.Name, info.Size) {
				return ops.NotFound("這個檔案沒有縮圖")
			}
			data, _, err := ReadHead(rc, limits.UploadImageBytes)
			if err != nil {
				return ops.Internal(err)
			}
			img, ct, err := Thumbnail(data, limits.ThumbSize)
			if err != nil {
				return ops.NotFound("這個檔案沒有縮圖")
			}
			h.Set("Content-Type", ct)
			h.Set("Cache-Control", "private, max-age=300")
			h.Set("Content-Security-Policy", sandboxed)
			h.Set("Content-Length", strconv.Itoa(len(img)))
			_, err = w.Write(img)
			return ignoreWriteErr(err)
		}
		inline := q.Get("inline") == "1" && Inlinable(meta.Name)
		ct := MimeOf(meta.Name)
		switch KindOf(meta.Name) {
		case KindText, KindMarkdown, KindCSV, KindCode:
			if inline {
				ct = "text/plain; charset=utf-8" // 永遠不當成網頁
			}
		}
		disp := "attachment"
		if inline {
			disp = "inline"
		}
		h.Set("Content-Type", ct)
		if cd := mime.FormatMediaType(disp, map[string]string{"filename": meta.Name}); cd != "" {
			h.Set("Content-Disposition", cd)
		} else {
			h.Set("Content-Disposition", disp)
		}
		if KindOf(meta.Name) != KindPDF {
			// 瀏覽器內建的 PDF 檢視器在 sandbox 裡不能用；PDF 的程式在檢視器裡跑，碰不到這個網站。
			h.Set("Content-Security-Policy", sandboxed)
		}
		h.Set("Cache-Control", "private, no-store")
		h.Set("Content-Length", strconv.FormatInt(info.Size, 10))
		_, err = io.Copy(w, rc)
		return ignoreWriteErr(err)
	}
}

// 已經開始送內容以後的錯誤（多半是對方斷線）寫不出錯誤訊息了。
func ignoreWriteErr(error) error { return nil }
