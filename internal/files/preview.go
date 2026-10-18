package files

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/traditionalchinese"

	"xbot/internal/limits"
)

// Table 是 CSV、Excel 的表格預覽。
type Table struct {
	Columns []string   `json:"columns" desc:"第一列（欄名）"`
	Rows    [][]string `json:"rows" desc:"之後的列，最多 1,000 列"`
	// TotalRows 是資料列總數（不含欄名）；-1 表示檔案太大，沒有數完。
	TotalRows int      `json:"total_rows" desc:"資料列總數（不含欄名）；-1 表示檔案太大沒有數完"`
	Truncated bool     `json:"truncated" desc:"只顯示前面幾列或幾欄"`
	Sheets    []string `json:"sheets,omitempty" desc:"Excel 的工作表名稱"`
	Sheet     string   `json:"sheet,omitempty" desc:"這是哪一個工作表"`
}

// Text 是文字預覽的結果。
type Text struct {
	Text      string
	Truncated bool
	Encoding  string // utf-8、big5
	Binary    bool
}

// DecodeText 把讀到的內容變成文字：UTF-8（去掉 BOM），不是的話試 Big5（台灣常見的 CSV）；看起來是二進位檔就回 Binary。
// truncated 表示 data 是從檔案前面截下來的（最後一個字可能被切斷）。
func DecodeText(data []byte, truncated bool) Text {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	head := data[:min(len(data), 8192)]
	if bytes.IndexByte(head, 0) >= 0 {
		return Text{Binary: true}
	}
	if truncated {
		// 切在一個字的中間：去掉不完整的尾巴。
		for i := 0; i < utf8.UTFMax && len(data) > 0 && !utf8.Valid(data); i++ {
			r, _ := utf8.DecodeLastRune(data)
			if r != utf8.RuneError {
				break
			}
			data = data[:len(data)-1]
		}
	}
	if utf8.Valid(data) {
		return Text{Text: string(data), Truncated: truncated, Encoding: "utf-8"}
	}
	dec, err := traditionalchinese.Big5.NewDecoder().Bytes(data)
	if err == nil && utf8.Valid(dec) && bytes.Count(dec, []byte("�"))*100 <= len([]rune(string(dec))) {
		return Text{Text: string(dec), Truncated: truncated, Encoding: "big5"}
	}
	return Text{Binary: true}
}

// ReadHead 讀 r 的前 max 個位元組；回傳是否還有更多。
func ReadHead(r io.Reader, max int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > max {
		return data[:max], true, nil
	}
	return data, false, nil
}

// CSV 把文字當成 CSV（.tsv 或看起來是 Tab 分隔時用 Tab）讀成表格。truncated 表示文字只是檔案的前面一段：最後一列可能不完整，丟掉，也不知道總列數。
func CSV(text, name string, truncated bool) (*Table, error) {
	if truncated {
		if i := strings.LastIndexByte(text, '\n'); i >= 0 {
			text = text[:i+1]
		}
	}
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = delimiter(text, name)
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	r.ReuseRecord = false
	t := &Table{Rows: [][]string{}, TotalRows: 0}
	first := true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) && len(t.Rows) > 0 {
				t.Truncated = true
				break
			}
			return nil, err
		}
		if first {
			t.Columns = clip(rec, t)
			first = false
			continue
		}
		t.TotalRows++
		if len(t.Rows) < limits.FilePreviewRows {
			t.Rows = append(t.Rows, clip(rec, t))
		} else {
			t.Truncated = true
		}
	}
	if t.Columns == nil {
		t.Columns = []string{}
	}
	if truncated {
		t.TotalRows, t.Truncated = -1, true
	}
	return t, nil
}

func clip(rec []string, t *Table) []string {
	if len(rec) > limits.FilePreviewColumns {
		t.Truncated = true
		rec = rec[:limits.FilePreviewColumns]
	}
	out := make([]string, len(rec))
	for i, s := range rec {
		if len(s) > 2000 {
			s = s[:2000] + "…"
			s = strings.ToValidUTF8(s, "")
		}
		out[i] = s
	}
	return out
}

func delimiter(text, name string) rune {
	if strings.EqualFold(path.Ext(name), ".tsv") {
		return '\t'
	}
	line, _, _ := strings.Cut(text, "\n")
	best, n := ',', strings.Count(line, ",")
	for _, c := range []rune{'\t', ';'} {
		if k := strings.Count(line, string(c)); k > n {
			best, n = c, k
		}
	}
	return best
}

// Language 是程式碼的語言（給語法上色用）：副檔名去掉點。
func Language(name string) string {
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	switch ext {
	case "yml":
		return "yaml"
	case "htm":
		return "html"
	case "mjs", "cjs":
		return "js"
	case "":
		return strings.ToLower(name)
	}
	return ext
}
