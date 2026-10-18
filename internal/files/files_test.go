package files

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/text/encoding/traditionalchinese"

	"xbot/internal/limits"
)

var ctx = context.Background()

// 路徑：只能在 /workspace 裡面（LazyBoy files.contained）。
func TestRelStaysInWorkspace(t *testing.T) {
	ok := map[string]string{
		"":                      "",
		"/workspace":            "",
		"/workspace/":           "",
		"報告.md":                 "報告.md",
		"/workspace/a/b.csv":    "a/b.csv",
		"a//b/./c":              "a/b/c",
		"/workspace/a/":         "a",
		"%2e%2e/x":              "%2e%2e/x", // 只是名稱，不會被解碼
		"from/阿哲/價格.csv":        "from/阿哲/價格.csv",
		"  /workspace/空白.txt  ": "空白.txt",
	}
	for in, want := range ok {
		got, err := Rel(in)
		if err != nil || got != want {
			t.Errorf("Rel(%q) = %q, %v；想要 %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"..", "../etc/passwd", "a/../../x", "a/..", "/workspace/../etc", "/etc/passwd", "/home/box/.hermes",
		"/workspacex/a", "a\x00b", "a\nb", "\x7f", strings.Repeat("x", 256), strings.Repeat("a/", limits.FilePathDepth) + "x",
		strings.Repeat("x", limits.FilePathMax+1), "\xff\xfe"} {
		if got, err := Rel(bad); err == nil {
			t.Errorf("Rel(%q) 要拒絕，得到 %q", bad, got)
		}
	}
	if _, err := Rel("../x"); !errors.Is(err, ErrOutside) {
		t.Errorf("跑出 /workspace 要回 ErrOutside：%v", err)
	}
	if Abs("") != "/workspace" || Abs("a/b") != "/workspace/a/b" {
		t.Error("Abs")
	}
	for in, want := range map[string]string{"a/b.txt": "b.txt", `C:\Users\me\報告.pdf`: "報告.pdf", "..": "attachment", "x\x01y": "x_y", " .hidden ": "hidden"} {
		if got := Filename(in); got != want {
			t.Errorf("Filename(%q) = %q，想要 %q", in, got, want)
		}
	}
}

// 翻自 LazyBoy tests/test_important_files.py。
func TestResultsAreImportantAndBuildNoiseIsNot(t *testing.T) {
	for _, rel := range []string{"report.md", "projects/site/index.html", "charts/sales.png", "projects/app/src/main.py"} {
		if !Important(rel) {
			t.Errorf("%s 是成果", rel)
		}
	}
	for _, rel := range []string{"projects/app/dist/bundle.js", "package-lock.json", "run.log", "projects/app/cache/x.json", "a/b.pyc",
		"lessons/x/shots/1.png", "lessons/x/raw.jsonl"} {
		if Important(rel) {
			t.Errorf("%s 不是成果", rel)
		}
	}
	if !Important("lessons/x/steps.json") {
		t.Error("示範教學的 steps.json 是給人看的")
	}
}

func TestDeliverablesRankBeforeDeepSource(t *testing.T) {
	if !(Rank("report.md") < Rank("projects/app/src/main.py")) || !(Rank("report.md") < Rank("notes/report.md")) {
		t.Fatal("上層的成果檔要排在前面")
	}
}

func TestWalkSkipsDependencyTreesAndHidden(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"node_modules/pkg/index.js", ".git/HEAD", "out.md", "Venv/lib.py", "sub/.env", "sub/ok.txt"} {
		write(t, root, p, "x")
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	infos, err := Dir{Path: root}.Walk(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, i := range infos {
		names = append(names, i.Rel)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"out.md", "sub/ok.txt"}) {
		t.Fatalf("%v", names)
	}
}

func TestGroupsAndKinds(t *testing.T) {
	cases := map[string]Group{"報告.md": GroupResult, "uploads/a.pdf": GroupUpload, "lessons/x/steps.json": GroupLesson,
		"from/阿哲/價格.csv": GroupPeer, "from/x.csv": GroupResult}
	for rel, want := range cases {
		if g, _ := GroupOf(rel); g != want {
			t.Errorf("%s：%s，想要 %s", rel, g, want)
		}
	}
	if _, from := GroupOf("from/阿哲/a/b.csv"); from != "阿哲" {
		t.Errorf("隊友名稱：%q", from)
	}
	for name, want := range map[string]Kind{"a.PNG": KindImage, "b.pdf": KindPDF, "c.tsv": KindCSV, "d.xlsx": KindSheet, "e.md": KindMarkdown,
		"f.go": KindCode, "g.txt": KindText, "h.docx": KindOffice, "i.zip": KindArchive, "j.bin": KindOther, "Makefile": KindText} {
		if got := KindOf(name); got != want {
			t.Errorf("%s：%s，想要 %s", name, got, want)
		}
	}
	if MimeOf("a.md") != "text/markdown; charset=utf-8" || !strings.HasPrefix(MimeOf("a.png"), "image/png") || MimeOf("x.unknownext") != "application/octet-stream" {
		t.Error("MimeOf")
	}
	if Inlinable("a.html") || Inlinable("a.svg") == false || !Inlinable("a.pdf") || Inlinable("a.docx") {
		t.Error("HTML 一律下載；圖片、PDF 可以直接顯示")
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeText(t *testing.T) {
	if got := DecodeText([]byte("\xef\xbb\xbf你好"), false); got.Text != "你好" || got.Encoding != "utf-8" || got.Binary {
		t.Fatalf("BOM：%+v", got)
	}
	big5, _ := traditionalchinese.Big5.NewEncoder().Bytes([]byte("商品,價格\n帆布包,1280\n"))
	if got := DecodeText(big5, false); got.Encoding != "big5" || !strings.Contains(got.Text, "帆布包") {
		t.Fatalf("Big5：%+v", got)
	}
	if got := DecodeText([]byte("abc\x00def"), false); !got.Binary {
		t.Fatal("有 NUL 是二進位檔")
	}
	// 截在一個字的中間：去掉不完整的尾巴。
	cut := []byte("價格表")[:7]
	if got := DecodeText(cut, true); got.Binary || got.Text != "價格" || !got.Truncated {
		t.Fatalf("截斷：%+v", got)
	}
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0x0d}
	if !DecodeText(png, false).Binary {
		t.Fatal("PNG 是二進位檔")
	}
}

func TestCSV(t *testing.T) {
	tab, err := CSV("商品,我們,好物町\n帆布托特包,1280,1190\n\"防水,托特\",1580,1390\n", "價格.csv", false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(tab.Columns, []string{"商品", "我們", "好物町"}) || len(tab.Rows) != 2 || tab.Rows[1][0] != "防水,托特" || tab.TotalRows != 2 || tab.Truncated {
		t.Fatalf("%+v", tab)
	}
	// Tab、分號分隔。
	if tab, _ := CSV("a\tb\n1\t2\n", "x.tsv", false); len(tab.Columns) != 2 {
		t.Fatalf("tsv：%+v", tab)
	}
	if tab, _ := CSV("a;b;c\n1;2;3\n", "x.csv", false); len(tab.Columns) != 3 {
		t.Fatalf("分號：%+v", tab)
	}
	// 超過 1,000 列：只回前面，但數得出總數。
	var b strings.Builder
	b.WriteString("n\n")
	for i := range 1500 {
		fmt.Fprintf(&b, "%d\n", i)
	}
	tab, _ = CSV(b.String(), "big.csv", false)
	if len(tab.Rows) != limits.FilePreviewRows || tab.TotalRows != 1500 || !tab.Truncated {
		t.Fatalf("大檔：%d %d %v", len(tab.Rows), tab.TotalRows, tab.Truncated)
	}
	// 只讀了檔案的前面：最後一列不完整，丟掉；不知道總列數。
	tab, _ = CSV("a,b\n1,2\n3,4\n5,", "x.csv", true)
	if len(tab.Rows) != 2 || tab.TotalRows != -1 || !tab.Truncated {
		t.Fatalf("截斷：%+v", tab)
	}
}

// xlsx 做一個最小的 .xlsx。
func xlsx(t *testing.T, sheets map[string]string, shared []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range sheets {
			if !yield(k) {
				return
			}
		}
	})
	var wb, rels strings.Builder
	wb.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	rels.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i, n := range names {
		fmt.Fprintf(&wb, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, n, i+1, i+1)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Target="worksheets/sheet%d.xml"/>`, i+1, i+1)
		add(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`+sheets[n]+`</sheetData></worksheet>`)
	}
	wb.WriteString(`</sheets></workbook>`)
	rels.WriteString(`</Relationships>`)
	add("xl/workbook.xml", wb.String())
	add("xl/_rels/workbook.xml.rels", rels.String())
	if shared != nil {
		var ss strings.Builder
		ss.WriteString(`<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
		for _, s := range shared {
			ss.WriteString(`<si><t>` + s + `</t></si>`)
		}
		ss.WriteString(`<si><r><t>富</t></r><r><t>文字</t></r><rPh><t>ふ</t></rPh></si></sst>`)
		add("xl/sharedStrings.xml", ss.String())
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestXLSX(t *testing.T) {
	data := xlsx(t, map[string]string{
		"A訂單": `<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c><c r="D1" t="inlineStr"><is><t>備註</t></is></c></row>` +
			`<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2"><v>1280</v></c><c r="C2" t="b"><v>1</v></c></row>`,
		"B其他": `<row r="1"><c r="A1"><v>1</v></c></row>`,
	}, []string{"商品", "價格"})
	tab, err := XLSX(data, "")
	if err != nil {
		t.Fatal(err)
	}
	if tab.Sheet != "A訂單" || !slices.Equal(tab.Sheets, []string{"A訂單", "B其他"}) {
		t.Fatalf("工作表：%+v", tab)
	}
	if !slices.Equal(tab.Columns, []string{"商品", "價格", "", "備註"}) || len(tab.Rows) != 1 || !slices.Equal(tab.Rows[0], []string{"富文字", "1280", "TRUE"}) ||
		tab.TotalRows != 1 {
		t.Fatalf("內容：%+v", tab)
	}
	if tab, err := XLSX(data, "B其他"); err != nil || tab.Sheet != "B其他" || tab.Columns[0] != "1" {
		t.Fatalf("第二個工作表：%+v %v", tab, err)
	}
	if _, err := XLSX(data, "沒有"); err == nil {
		t.Fatal("沒有這個工作表")
	}
	if _, err := XLSX([]byte("PK\x03\x04 not really"), ""); err == nil {
		t.Fatal("壞掉的檔案要回錯誤")
	}
}

// 翻自 LazyBoy tests/test_previews.py test_archives_are_listed_without_extracting。
func TestArchivesAreListedWithoutExtracting(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("report/summary.md")
	w.Write([]byte("hi"))
	zw.Create("report/")
	zw.Close()
	a, err := ListArchive(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if a.Total != 2 || !slices.Contains(a.Entries, ArchiveEntry{Name: "report/summary.md", Size: 2}) {
		t.Fatalf("%+v", a)
	}
	if _, err := ListArchive([]byte("PK\x03\x04 not really")); err == nil {
		t.Fatal("壞掉的 zip 要回錯誤")
	}
	// .tar.gz
	var tb bytes.Buffer
	gz := gzip.NewWriter(&tb)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "a/", Typeflag: tar.TypeDir, Mode: 0o755})
	tw.WriteHeader(&tar.Header{Name: "a/b.txt", Typeflag: tar.TypeReg, Size: 3, Mode: 0o644})
	tw.Write([]byte("abc"))
	tw.Close()
	gz.Close()
	a, err = ListArchive(tb.Bytes())
	if err != nil || a.Total != 2 || !a.Entries[0].Dir || a.Entries[1].Size != 3 {
		t.Fatalf("tar.gz：%+v %v", a, err)
	}
	if _, err := ListArchive([]byte("just text")); err == nil {
		t.Fatal("不是壓縮檔")
	}
}

func TestThumbnail(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1000, 500))
	for y := range 500 {
		for x := range 1000 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 100, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	out, ct, err := Thumbnail(buf.Bytes(), 320)
	if err != nil || ct != "image/jpeg" {
		t.Fatal(ct, err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != 320 || cfg.Height != 160 {
		t.Fatalf("%+v %v", cfg, err)
	}
	// 有透明：PNG。
	clear := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	buf.Reset()
	png.Encode(&buf, clear)
	if _, ct, err := Thumbnail(buf.Bytes(), 320); err != nil || ct != "image/png" {
		t.Fatal(ct, err)
	}
	// 解壓縮炸彈：宣稱很大的尺寸，不解碼。
	huge := image.NewGray(image.Rect(0, 0, 1, 1))
	buf.Reset()
	png.Encode(&buf, huge)
	b := buf.Bytes()
	// IHDR 的寬高在第 16～24 位元組。
	copy(b[16:24], []byte{0, 0x01, 0, 0, 0, 0x01, 0, 0})
	if _, _, err := Thumbnail(b, 320); !errors.Is(err, ErrNoThumb) {
		t.Fatalf("太大的圖不做縮圖：%v", err)
	}
	if _, _, err := Thumbnail([]byte("not an image"), 320); !errors.Is(err, ErrNoThumb) {
		t.Fatal(err)
	}
}
