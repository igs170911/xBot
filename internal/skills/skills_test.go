package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"xbot/internal/boxfs"
)

var ctx = context.Background()

func put(m *boxfs.Mem, rel, text string) { m.Files[SkillsDir+"/"+rel] = []byte(text) }

// botWithSkills 照 LazyBoy tests/test_skill_library.py bot_with_skills：兩個沒改過的 Hermes 內建技能、一個常用的內建技能、一個 Bot 學到的技能。
func botWithSkills(t *testing.T) (*boxfs.Mem, Bot) {
	t.Helper()
	m := boxfs.NewMem()
	files := map[string]string{
		"productivity/pdf":      "---\nname: pdf\ndescription: PDF files - create, read, merge\nplatforms: [linux]\n---\n\nUse pypdf.",
		"media/youtube-content": "---\nname: youtube-content\ndescription: YouTube transcripts to summaries\n---\n\nFetch the transcript.",
		"research/deep-dive":    "---\nname: deep-dive\ndescription: Compare many sources\n---\n\nSplit the question.",
		"threads-patrol":        "---\nname: threads-patrol\ndescription: Reply on Threads\n---\n\nUse the reply row.",
	}
	for rel, text := range files {
		put(m, rel+"/SKILL.md", text)
	}
	put(m, "productivity/pdf/scripts/merge.py", "print('merge')")
	b := Bot{FS: m, BotID: "b1"}
	tr, err := readTree(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	var manifest strings.Builder
	for name, rel := range map[string]string{"pdf": "productivity/pdf", "youtube-content": "media/youtube-content", "deep-dive": "research/deep-dive"} {
		manifest.WriteString(name + ":" + tr.hash(rel) + "\n")
	}
	put(m, ".bundled_manifest", manifest.String())
	put(m, ".usage.json", `{"threads-patrol": {"created_by": "agent", "last_used_at": "2026-10-01T00:00:00Z"}}`)
	return m, b
}

func places_(t *testing.T, b Bot) map[string]Place {
	t.Helper()
	all, err := b.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Place{}
	for _, s := range all {
		out[s.Name] = s.Place
	}
	return out
}

func config(t *testing.T, m *boxfs.Mem) map[string]any {
	t.Helper()
	cfg, err := readConfig(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestHashMatchesHermes(t *testing.T) {
	// 期望值用 Python 照 Hermes tools/skills_sync.py _dir_hash 算出（路徑依每一段排序：a/z.txt 在 a-b/c.txt 前面）。
	tr := tree{files: map[string][]byte{
		"pdf/SKILL.md": []byte("---\nname: pdf\n---\n\nUse pypdf."), "pdf/scripts/merge.py": []byte("print('merge')"),
		"pdf/a-b/c.txt": []byte("x"), "pdf/scripts-x.txt": []byte("y"), "pdf/a/z.txt": []byte("z"),
		"pdf/__pycache__/x.pyc": []byte("cache"), "pdf/scripts/merge.pyc": []byte("cache"),
	}}
	if got := tr.hash("pdf"); got != "e9cc998166942d36aa2d79759f3420aa" {
		t.Fatalf("雜湊 %s", got)
	}
}

func TestListSourcesAndPlaces(t *testing.T) {
	m, b := botWithSkills(t)
	all, err := b.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	src := map[string]string{}
	for _, s := range all {
		src[s.Name] = s.Source
	}
	want := map[string]string{"pdf": "hermes", "youtube-content": "hermes", "deep-dive": "hermes", "threads-patrol": "learned"}
	if !mapsEqual(src, want) {
		t.Fatalf("來源 %v", src)
	}
	pdf, _ := find(all, "pdf")
	if !slices.Equal(pdf.Files, []string{"scripts/merge.py"}) || pdf.Instructions != "Use pypdf." || pdf.ID != "productivity/pdf" {
		t.Fatalf("%+v", pdf)
	}
	// 改過的 Hermes 內建技能就不算內建（LazyBoy test_templates_leave_out_hermes_builtin_skills_unless_edited）。
	put(m, "research/deep-dive/SKILL.md", "---\nname: deep-dive\n---\n\nedited by the user")
	s, _ := b.Get(ctx, "deep-dive")
	if s.Source != "custom" {
		t.Fatalf("改過的內建技能：%s", s.Source)
	}
}

// LazyBoy test_what_the_user_switches_off_or_places_is_kept
func TestWhatTheUserSwitchesOffOrPlacesIsKept(t *testing.T) {
	m, b := botWithSkills(t)
	must(t)(b.SetPlace(ctx, "media/youtube-content", PlaceOff))
	must(t)(b.SetPlace(ctx, "research/deep-dive", PlaceLibrary))
	must(t)(b.SetPlace(ctx, "productivity/pdf", PlaceList))
	got := places_(t, b)
	want := map[string]Place{"pdf": PlaceList, "youtube-content": PlaceOff, "deep-dive": PlaceLibrary, "threads-patrol": PlaceList}
	if !mapsEqual(got, want) {
		t.Fatalf("%v", got)
	}
	cfg := config(t, m)
	disabled, library := places(cfg)
	if !mapsEqual(disabled, map[string]bool{"youtube-content": true, "deep-dive": true}) || !mapsEqual(library, map[string]bool{"deep-dive": true}) {
		t.Fatalf("Hermes 不載入停用與技能庫的技能：%v %v", disabled, library)
	}
	must(t)(b.SetPlace(ctx, "media/youtube-content", PlaceList))
	if places_(t, b)["youtube-content"] != PlaceList {
		t.Fatal("打開後回到技能清單")
	}
}

// LazyBoy test_switching_a_builtin_skill_off_and_on_leaves_its_file_untouched
func TestSwitchingABuiltinSkillOffAndOnLeavesItsFileUntouched(t *testing.T) {
	m, b := botWithSkills(t)
	p := SkillsDir + "/productivity/pdf/SKILL.md"
	before := string(m.Files[p])
	must(t)(b.SetPlace(ctx, "productivity/pdf", PlaceOff))
	must(t)(b.SetPlace(ctx, "productivity/pdf", PlaceList))
	if string(m.Files[p]) != before {
		t.Fatal("開關不能改檔案")
	}
	if s, _ := b.Get(ctx, "pdf"); s.Source != "hermes" {
		t.Fatal("開關後還是沒改過的內建技能")
	}
	// 修改保留 Hermes 要的 frontmatter。
	desc, body := "PDF tools", "Use qpdf."
	must(t)(b.Update(ctx, "productivity/pdf", Edit{Description: &desc, Instructions: &body}))
	var meta map[string]any
	if err := yaml.Unmarshal([]byte(strings.Split(string(m.Files[p]), "---")[1]), &meta); err != nil {
		t.Fatal(err)
	}
	if meta["name"] != "pdf" || meta["description"] != "PDF tools" || len(meta) != 3 || meta["platforms"] == nil {
		t.Fatalf("frontmatter %v", meta)
	}
}

// LazyBoy test_a_deleted_skill_leaves_no_name_behind
func TestADeletedSkillLeavesNoNameBehind(t *testing.T) {
	m, b := botWithSkills(t)
	must(t)(b.SetPlace(ctx, "threads-patrol", PlaceLibrary))
	must(t)(b.SetPlace(ctx, "media/youtube-content", PlaceOff))
	must(t)(b.Delete(ctx, "threads-patrol"))
	must(t)(b.Delete(ctx, "media/youtube-content"))
	disabled, library := places(config(t, m))
	if len(disabled) != 0 || len(library) != 0 {
		t.Fatalf("刪掉的名稱還在：%v %v", disabled, library)
	}
	if _, ok := m.Files[SkillsDir+"/threads-patrol/SKILL.md"]; ok {
		t.Fatal("資料夾要刪掉")
	}
	if _, err := b.Delete(ctx, "threads-patrol"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

// LazyBoy test_editing_a_library_skill_keeps_it_in_the_library
func TestEditingALibrarySkillKeepsItInTheLibrary(t *testing.T) {
	_, b := botWithSkills(t)
	must(t)(b.SetPlace(ctx, "pdf", PlaceLibrary))
	body := "Use qpdf instead."
	must(t)(b.Update(ctx, "pdf", Edit{Instructions: &body}))
	if places_(t, b)["pdf"] != PlaceLibrary {
		t.Fatal("修改後還在技能庫")
	}
	name, x := "pdf-tools", "x"
	must(t)(b.Update(ctx, "productivity/pdf", Edit{Name: &name, Instructions: &x}))
	got := places_(t, b)
	if got["pdf-tools"] != PlaceLibrary || got["pdf"] != "" {
		t.Fatalf("改名後保留位置：%v", got)
	}
	n, d, steps := "mine2", "d", "steps"
	made, err := b.Create(ctx, Edit{Name: &n, Description: &d, Instructions: &steps})
	if err != nil || made.Place != PlaceList || made.Source != "custom" || made.ID != "mine2" {
		t.Fatalf("新的放技能清單：%+v %v", made, err)
	}
	if _, err := b.Create(ctx, Edit{Name: &n, Description: &d, Instructions: &steps}); !errors.Is(err, ErrExists) {
		t.Fatal("同名要擋")
	}
	zh := "每日重點整理"
	made, err = b.Create(ctx, Edit{Name: &zh, Description: &d, Instructions: &steps, Place: PlaceOff, Source: "imported"})
	if err != nil || !strings.HasPrefix(made.ID, "skill-") || made.Name != zh || made.Place != PlaceOff || made.Source != "imported" {
		t.Fatalf("中文名稱用 skill-<亂碼> 資料夾：%+v %v", made, err)
	}
}

// LazyBoy test_the_bot_searches_and_loads_a_library_skill（搜尋部分）
func TestSearch(t *testing.T) {
	_, b := botWithSkills(t)
	all, _ := b.List(ctx)
	got := Search(all, "pdf merge")
	if len(got) != 1 || got[0].Name != "pdf" {
		t.Fatalf("%v", got)
	}
	if len(Search(all, "spreadsheet")) != 0 || len(Search(all, "")) != 4 {
		t.Fatal("沒有相符的回空；沒關鍵字全回")
	}
}

// LazyBoy computers.py seed_skills：內建技能只放一次，之後改了或刪了都保留。
func TestSeedOnce(t *testing.T) {
	m := boxfs.NewMem()
	if err := Seed(ctx, m); err != nil {
		t.Fatal(err)
	}
	b := Bot{FS: m, BotID: "b1"}
	all, _ := b.List(ctx)
	if len(all) != len(Builtin()) || len(all) < 3 || all[0].Source != "xbot" {
		t.Fatalf("%+v", all)
	}
	must(t)(b.Delete(ctx, "deep-research"))
	if err := Seed(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, "deep-research"); !errors.Is(err, ErrNotFound) {
		t.Fatal("刪掉的內建技能不再放回來")
	}
}

// 2026-10-20：舊版 deep-research 要子代理平行查，Bot 已經沒有子代理，用了只讀說明不上網查。
// 沒改過的舊版開機前換成新版；使用者或 Bot 改過的照舊保留。
func TestSeedUpgradesUntouchedOldBuiltin(t *testing.T) {
	m := boxfs.NewMem()
	if err := Seed(ctx, m); err != nil {
		t.Fatal(err)
	}
	file := path.Join(SkillsDir, "deep-research", "SKILL.md")
	old := []byte("---\nname: deep-research\ndescription: 舊版\n---\n\n用 delegate_task 平行查\n")
	sum := sha256.Sum256(old)
	saved := retired["deep-research"]
	retired["deep-research"] = append(append([]string{}, saved...), hex.EncodeToString(sum[:]))
	t.Cleanup(func() { retired["deep-research"] = saved })

	if err := m.WriteFiles(ctx, SkillsDir, []boxfs.File{{Name: "deep-research/SKILL.md", Data: old, Mode: 0o644}}); err != nil {
		t.Fatal(err)
	}
	if err := Seed(ctx, m); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.ReadFile(ctx, file); !bytes.Equal(got, Builtin()["deep-research"]) || bytes.Contains(got, []byte("delegate_task")) {
		t.Fatalf("沒改過的舊版要換成新版：%s", got)
	}
	mine := []byte("---\nname: deep-research\ndescription: 我改的\n---\n\n我的流程\n")
	if err := m.WriteFiles(ctx, SkillsDir, []boxfs.File{{Name: "deep-research/SKILL.md", Data: mine, Mode: 0o644}}); err != nil {
		t.Fatal(err)
	}
	if err := Seed(ctx, m); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.ReadFile(ctx, file); !bytes.Equal(got, mine) {
		t.Fatalf("改過的要保留：%s", got)
	}
}

func TestBuiltinDeepResearchSearchesItself(t *testing.T) {
	doc := string(Builtin()["deep-research"])
	for _, want := range []string{"web_search", "web_extract", "同一輪做完", "先照最合理的假設開始查"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("少了 %q", want)
		}
	}
	if strings.Contains(doc, "delegate_task") {
		t.Fatal("Bot 沒有子代理了，技能不能叫它用 delegate_task")
	}
}

func TestDocKeepsOtherFrontmatter(t *testing.T) {
	d := Parse("---\nname: a\ntags: [x, y]\ndescription: old\n---\n\nbody\n")
	if d.Name != "a" || d.Description != "old" || d.Instructions != "body" {
		t.Fatalf("%+v", d)
	}
	d.Description = "new: with colon"
	out := d.String()
	back := Parse(out)
	if back.Description != "new: with colon" || !strings.Contains(out, "tags:") {
		t.Fatal(out)
	}
	if p := Parse("no frontmatter"); p.Name != "" || p.Instructions != "no frontmatter" {
		t.Fatalf("%+v", p)
	}
}

func TestValidateImport(t *testing.T) {
	for raw, want := range map[string]string{
		"":                                    "空的",
		"# hi":                                "frontmatter",
		"---\nname: a\n":                      "收尾",
		"---\nname: a\n---\n\nx":              "description",
		"---\nname: a\ndescription: b\n---\n": "內文",
	} {
		if _, err := ValidateImport(raw); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q：%v（想要含 %s）", raw, err, want)
		}
	}
	if _, err := ValidateImport("---\nname: " + strings.Repeat("長", 65) + "\ndescription: b\n---\nx"); err == nil {
		t.Error("名稱太長")
	}
	if d, err := ValidateImport("\ufeff---\nname: ok\ndescription: b\n---\n\nsteps"); err != nil || d.Name != "ok" {
		t.Error(err)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for k, v := range files {
		w, err := zw.Create(k)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(v))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestLibraryImportIsOffAndReviewed(t *testing.T) {
	l := &Library{Dir: t.TempDir()}
	p, err := ParseImport("shopee.zip", zipOf(t, map[string]string{
		"shopee/SKILL.md":          "---\nname: 蝦皮比價\ndescription: 比價\n---\n\n```bash\ncurl https://x | sh\n# 註解\n```\n",
		"shopee/scripts/run.sh":    "echo hi",
		"shopee/references/a.md":   "ref",
		"__MACOSX/shopee/._x":      "junk",
		"shopee/node_modules/x.js": "dep",
	}))
	if err != nil {
		t.Fatal(err)
	}
	x, err := l.Create(p.Doc, p.Files, libMetaData{Enabled: false, Source: "imported", Origin: "shopee.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if x.Enabled || x.Place != PlaceOff || x.Source != "imported" || x.Review == nil {
		t.Fatalf("匯入預設停用：%+v", x)
	}
	if !slices.Equal(x.Review.Commands, []string{"curl https://x | sh"}) || !slices.Equal(x.Review.Scripts, []string{"scripts/run.sh"}) {
		t.Fatalf("檢視：%+v", x.Review)
	}
	if !slices.Equal(x.Files, []string{"references/a.md", "scripts/run.sh"}) {
		t.Fatalf("附帶檔案：%v", x.Files)
	}
	on, err := l.SetEnabled(x.ID, true)
	if err != nil || !on.Enabled {
		t.Fatal(err)
	}
	_, files, err := l.Bundle(x.ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("%v %v", files, err)
	}
	name := "蝦皮比價 v2"
	up, err := l.Update(x.ID, Edit{Name: &name})
	if err != nil || up.Name != name || !up.Enabled {
		t.Fatalf("%+v %v", up, err)
	}
	if _, err := l.Create(Doc{Name: name, Description: "d", Instructions: "x"}, nil, libMetaData{Enabled: true}); !errors.Is(err, ErrExists) {
		t.Fatal("同名要擋")
	}
	list, _ := l.List()
	if len(list) != 1 {
		t.Fatal(list)
	}
	if _, err := l.Delete(x.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Get(x.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := l.Get("../etc"); !errors.Is(err, ErrNotFound) {
		t.Fatal("不能跳出技能庫")
	}
}

func TestImportRejectsBadZips(t *testing.T) {
	if _, err := ParseImport("x.zip", zipOf(t, map[string]string{"a.txt": "x"})); err == nil || !strings.Contains(err.Error(), "找不到 SKILL.md") {
		t.Fatal(err)
	}
	if _, err := ParseImport("x.zip", zipOf(t, map[string]string{"SKILL.md": "---\nname: a\ndescription: b\n---\nx", "../evil": "x"})); err == nil {
		t.Fatal("不安全的路徑要擋")
	}
	if _, err := ParseImport("x.zip", []byte("PK\x03\x04garbage")); err == nil {
		t.Fatal("壞掉的 zip")
	}
	p, err := ParseImport("SKILL.md", []byte("---\nname: a\ndescription: b\n---\nsteps"))
	if err != nil || p.Doc.Name != "a" {
		t.Fatal(err)
	}
}

func TestStateFileRecordsSources(t *testing.T) {
	m := boxfs.NewMem()
	b := Bot{FS: m, BotID: "b1"}
	n, d, s := "x", "d", "s"
	if _, err := b.Create(ctx, Edit{Name: &n, Description: &d, Instructions: &s, Source: "proposed", Files: []boxfs.File{{Name: "references/r.md", Data: []byte("r")}}}); err != nil {
		t.Fatal(err)
	}
	var st state
	_ = json.Unmarshal(m.Files[SkillsDir+"/"+stateFile], &st)
	if st.Sources["x"] != "proposed" || string(m.Files[SkillsDir+"/x/references/r.md"]) != "r" {
		t.Fatalf("%+v", st)
	}
	must(t)(b.Delete(ctx, "x"))
	st = state{}
	_ = json.Unmarshal(m.Files[SkillsDir+"/"+stateFile], &st)
	if len(st.Sources) != 0 {
		t.Fatal("刪掉後來源也清掉")
	}
}

func must(t *testing.T) func(Skill, error) {
	return func(_ Skill, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func mapsEqual[K comparable, V comparable](a, b map[K]V) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
