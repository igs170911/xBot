package files

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"xbot/internal/limits"
)

// XLSX 讀 Excel 活頁簿（.xlsx、.xlsm）的一個工作表的前幾列（sheet 空白時讀第一個）。
// 只用標準函式庫讀 zip 與 XML：不算公式、不套數字格式（日期會是 Excel 的序號）；解壓縮後的大小有上限，擋壓縮炸彈。
func XLSX(data []byte, sheet string) (*Table, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("這個 Excel 檔已損毀或不是 .xlsx 格式")
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[strings.TrimPrefix(f.Name, "/")] = f
	}
	budget := limits.FileSheetXMLBytes
	open := func(name string) (io.ReadCloser, error) {
		f := files[name]
		if f == nil {
			return nil, fmt.Errorf("缺少 %s", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		return struct {
			io.Reader
			io.Closer
		}{&budgetReader{r: rc, n: &budget}, rc}, nil
	}
	names, targets, err := workbook(open)
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, errors.New("這個 Excel 檔沒有工作表")
	}
	idx := 0
	if sheet != "" {
		idx = -1
		for i, n := range names {
			if n == sheet {
				idx = i
			}
		}
		if idx < 0 {
			return nil, fmt.Errorf("沒有「%s」這個工作表", sheet)
		}
	}
	var shared []string
	if files["xl/sharedStrings.xml"] != nil {
		if shared, err = sharedStrings(open); err != nil {
			return nil, err
		}
	}
	t, err := readSheet(open, targets[idx], shared)
	if err != nil {
		return nil, err
	}
	t.Sheets, t.Sheet = names, names[idx]
	return t, nil
}

type budgetReader struct {
	r io.Reader
	n *int64
}

func (b *budgetReader) Read(p []byte) (int, error) {
	if *b.n <= 0 {
		return 0, errors.New("這個 Excel 檔解壓縮後太大，請下載後開啟")
	}
	if int64(len(p)) > *b.n {
		p = p[:*b.n]
	}
	n, err := b.r.Read(p)
	*b.n -= int64(n)
	return n, err
}

// workbook 讀工作表名稱與它們的檔案位置（依活頁簿裡的順序）。
func workbook(open func(string) (io.ReadCloser, error)) ([]string, []string, error) {
	var wb struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			RID  string `xml:"http://schemas.openxmlformats.org/officeDocument/2006/relationships id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := decodeXML(open, "xl/workbook.xml", &wb); err != nil {
		return nil, nil, err
	}
	var rels struct {
		Rel []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := decodeXML(open, "xl/_rels/workbook.xml.rels", &rels); err != nil {
		return nil, nil, err
	}
	target := map[string]string{}
	for _, r := range rels.Rel {
		t := r.Target
		if strings.HasPrefix(t, "/") {
			t = strings.TrimPrefix(t, "/")
		} else {
			t = path.Join("xl", t)
		}
		target[r.ID] = t
	}
	var names, targets []string
	for i, s := range wb.Sheets {
		t := target[s.RID]
		if t == "" {
			t = fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1)
		}
		names, targets = append(names, s.Name), append(targets, t)
	}
	return names, targets, nil
}

func decodeXML(open func(string) (io.ReadCloser, error), name string, v any) error {
	rc, err := open(name)
	if err != nil {
		return errors.New("這個 Excel 檔已損毀或不是 .xlsx 格式")
	}
	defer rc.Close()
	if err := xml.NewDecoder(rc).Decode(v); err != nil {
		return fmt.Errorf("讀不懂這個 Excel 檔：%w", err)
	}
	return nil
}

// sharedStrings 讀共用字串表：每個 <si> 裡所有 <t> 接起來（跳過注音 <rPh>）。
func sharedStrings(open func(string) (io.ReadCloser, error)) ([]string, error) {
	rc, err := open("xl/sharedStrings.xml")
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	d := xml.NewDecoder(rc)
	var out []string
	var cur strings.Builder
	inSI, inT, inPh := false, false, 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("讀不懂這個 Excel 檔：%w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "t":
				inT = true
			case "rPh":
				inPh++
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "si":
				inSI = false
				out = append(out, cur.String())
			case "t":
				inT = false
			case "rPh":
				inPh--
			}
		case xml.CharData:
			if inSI && inT && inPh == 0 {
				cur.Write(t)
			}
		}
	}
}

// colIndex 把 "C12" 變成 2（從 0 開始）；讀不懂回 -1。
func colIndex(ref string) int {
	n := 0
	i := 0
	for ; i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z'; i++ {
		n = n*26 + int(ref[i]-'A'+1)
	}
	if i == 0 {
		return -1
	}
	return n - 1
}

func readSheet(open func(string) (io.ReadCloser, error), name string, shared []string) (*Table, error) {
	rc, err := open(name)
	if err != nil {
		return nil, errors.New("這個 Excel 檔已損毀或不是 .xlsx 格式")
	}
	defer rc.Close()
	d := xml.NewDecoder(rc)
	t := &Table{Rows: [][]string{}, TotalRows: -1}
	var row []string
	var cell struct {
		typ, ref string
		val      strings.Builder
		inV      bool
	}
	rows := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("讀不懂這個 Excel 檔：%w", err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "dimension":
				for _, a := range e.Attr {
					if a.Name.Local == "ref" {
						if _, end, ok := strings.Cut(a.Value, ":"); ok {
							if n, err := strconv.Atoi(strings.TrimLeft(end, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")); err == nil && n > 0 {
								t.TotalRows = n - 1
							}
						}
					}
				}
			case "row":
				row = []string{}
			case "c":
				cell.typ, cell.ref = "", ""
				cell.val.Reset()
				for _, a := range e.Attr {
					switch a.Name.Local {
					case "t":
						cell.typ = a.Value
					case "r":
						cell.ref = a.Value
					}
				}
			case "v", "t":
				cell.inV = true
			}
		case xml.CharData:
			if cell.inV {
				cell.val.Write(e)
			}
		case xml.EndElement:
			switch e.Name.Local {
			case "v", "t":
				cell.inV = false
			case "c":
				v := cell.val.String()
				switch cell.typ {
				case "s":
					if i, err := strconv.Atoi(v); err == nil && i >= 0 && i < len(shared) {
						v = shared[i]
					}
				case "b":
					if v == "1" {
						v = "TRUE"
					} else {
						v = "FALSE"
					}
				}
				i := colIndex(cell.ref)
				if i < 0 {
					i = len(row)
				}
				if i >= limits.FilePreviewColumns {
					t.Truncated = true
					continue
				}
				for len(row) < i {
					row = append(row, "")
				}
				row = append(row, v)
			case "row":
				rows++
				if rows == 1 {
					t.Columns = row
					continue
				}
				if len(t.Rows) >= limits.FilePreviewRows {
					t.Truncated = true
					if t.TotalRows < 0 {
						t.TotalRows = -1
					}
					return finish(t), nil
				}
				t.Rows = append(t.Rows, row)
			}
		}
	}
	if !t.Truncated {
		t.TotalRows = len(t.Rows)
	}
	return finish(t), nil
}

func finish(t *Table) *Table {
	if t.Columns == nil {
		t.Columns = []string{}
	}
	return t
}
