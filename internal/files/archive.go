package files

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"

	"xbot/internal/limits"
)

// ArchiveEntry 是壓縮檔裡的一項。
type ArchiveEntry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Dir  bool   `json:"dir"`
}

// Archive 是壓縮檔的內容清單（不解壓縮；LazyBoy previews.archive_entries）。
type Archive struct {
	Entries   []ArchiveEntry `json:"entries"`
	Total     int            `json:"total" desc:"總項數（數到上限為止）"`
	Truncated bool           `json:"truncated"`
}

// ErrArchive 表示讀不了這個壓縮檔。
var ErrArchive = errors.New("壓縮檔已損毀或無法讀取")

// ListArchive 列出 zip 或 tar（含 .tar.gz、.tgz）裡面有什麼，最多 ArchiveEntries 項，數到 ArchiveCountLimit 為止。
func ListArchive(data []byte) (*Archive, error) {
	out := &Archive{Entries: []ArchiveEntry{}}
	if zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data))); err == nil {
		out.Total = len(zr.File)
		for _, f := range zr.File {
			if len(out.Entries) >= limits.ArchiveEntries {
				break
			}
			out.Entries = append(out.Entries, ArchiveEntry{Name: f.Name, Size: int64(f.UncompressedSize64), Dir: strings.HasSuffix(f.Name, "/")})
		}
		out.Truncated = out.Total > len(out.Entries)
		return out, nil
	} else if bytes.HasPrefix(data, []byte("PK")) {
		return nil, ErrArchive
	}
	var r io.Reader = bytes.NewReader(data)
	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, ErrArchive
		}
		defer gz.Close()
		budget := limits.FileSheetXMLBytes // 解壓縮後最多讀這麼多（擋壓縮炸彈）
		r = &budgetReader{r: gz, n: &budget}
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if out.Total == 0 {
				return nil, ErrArchive
			}
			out.Truncated = true
			break
		}
		out.Total++
		if len(out.Entries) < limits.ArchiveEntries {
			out.Entries = append(out.Entries, ArchiveEntry{Name: h.Name, Size: h.Size, Dir: h.Typeflag == tar.TypeDir})
		}
		if out.Total >= limits.ArchiveCountLimit {
			out.Truncated = true
			break
		}
	}
	if out.Total > len(out.Entries) {
		out.Truncated = true
	}
	return out, nil
}
