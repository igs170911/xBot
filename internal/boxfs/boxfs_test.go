package boxfs

import (
	"archive/tar"
	"bytes"
	"testing"
)

func TestTarRoundTrip(t *testing.T) {
	buf, err := Tar([]File{{Name: "a/b/SKILL.md", Data: []byte("x")}, {Name: "a/c.txt", Data: []byte("yy"), Mode: 0o600}}, 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(buf.Bytes()))
	var names []string
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		if h.Uid != 1000 || h.Gid != 1000 {
			t.Fatalf("擁有者：%+v", h)
		}
		names = append(names, h.Name)
	}
	if len(names) != 4 || names[0] != "a/" || names[1] != "a/b/" {
		t.Fatalf("中間的資料夾也要有：%v", names)
	}
	// docker cp /hermes/skills 的 tar 以 skills/ 開頭，讀出來去掉這一層。
	src, _ := Tar([]File{{Name: "skills/a/b/SKILL.md", Data: []byte("x")}, {Name: "skills/.usage.json", Data: []byte("{}")}}, 0, 0)
	raw := src.Bytes()
	files, err := untar(bytes.NewReader(raw), 0)
	if err != nil || len(files) != 2 || files[0].Name != "a/b/SKILL.md" || files[1].Name != ".usage.json" {
		t.Fatalf("%+v %v", files, err)
	}
	if _, err := untar(bytes.NewReader(raw), 1); err == nil {
		t.Fatal("超過大小要回錯誤")
	}
	for _, bad := range []string{"../x", "/abs", "a/../../x"} {
		if _, err := Tar([]File{{Name: bad}}, 0, 0); err == nil {
			t.Errorf("%s 要擋", bad)
		}
	}
	if _, err := Clean("/hermes/../etc"); err == nil {
		t.Error("不能有 ..")
	}
}
