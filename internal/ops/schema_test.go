package ops

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type schemaMember struct {
	BotID string `json:"bot_id" required:"true" resource:"bot" desc:"成員的 Bot id"`
	Role  string `json:"role,omitempty" enum:"lead,member"`
}

type schemaBase struct {
	Note string `json:"note,omitempty"`
}

type schemaIn struct {
	schemaBase
	Name     string            `json:"name" required:"true" desc:"名稱" max:"64"`
	Count    int               `json:"count,omitempty" min:"1" max:"10"`
	Ratio    float64           `json:"ratio,omitempty"`
	Level    int               `json:"level,omitempty" enum:"1,2,3"`
	Scope    Scope             `json:"scope,omitempty"`
	Scopes   []Scope           `json:"scopes,omitempty" min:"1"`
	Tags     []string          `json:"tags,omitempty" enum:"a,b" max:"3"`
	Members  []schemaMember    `json:"members"`
	Meta     map[string]int    `json:"meta,omitempty"`
	Raw      json.RawMessage   `json:"raw,omitempty"`
	When     time.Time         `json:"when,omitzero"`
	Ptr      *schemaMember     `json:"ptr,omitempty"`
	APIKey   string            `json:"api_key,omitempty" secret:"true"`
	Labels   map[string]string `json:"labels,omitempty"`
	Hidden   string            `json:"-"`
	private  string            //lint:ignore U1000 測試：不輸出的欄位要略過
	Untagged bool
}

func TestSchemaGeneration(t *testing.T) {
	s, err := SchemaOf[schemaIn]()
	if err != nil {
		t.Fatal(err)
	}
	if s.Type != "object" || s.AdditionalProperties != false {
		t.Fatalf("根要是不收額外欄位的物件：%+v", s)
	}
	wantOrder := "note,name,count,ratio,level,scope,scopes,tags,members,meta,raw,when,ptr,api_key,labels,Untagged"
	if got := strings.Join(s.Order, ","); got != wantOrder {
		t.Fatalf("欄位順序：%s", got)
	}
	if strings.Join(s.Required, ",") != "name" {
		t.Fatalf("必填：%v", s.Required)
	}
	name := s.Property("name")
	if name.Type != "string" || name.Description != "名稱" || *name.MinLength != 1 || *name.MaxLength != 64 {
		t.Fatalf("name：%+v", name)
	}
	if c := s.Property("count"); c.Type != "integer" || *c.Minimum != 1 || *c.Maximum != 10 {
		t.Fatalf("count：%+v", c)
	}
	if s.Property("ratio").Type != "number" || s.Property("Untagged").Type != "boolean" {
		t.Fatal("數字或布林型別不對")
	}
	if l := s.Property("level"); l.Type != "integer" || len(l.Enum) != 3 || l.Enum[2] != 3.0 {
		t.Fatalf("數字 enum：%+v", l.Enum)
	}
	if sc := s.Property("scope"); len(sc.Enum) != 4 || sc.Enum[0] != "read" {
		t.Fatalf("EnumValues 型別：%+v", sc.Enum)
	}
	if scs := s.Property("scopes"); scs.Type != "array" || len(scs.Items.Enum) != 4 || *scs.MinItems != 1 {
		t.Fatalf("enum 陣列：%+v", scs)
	}
	if tags := s.Property("tags"); len(tags.Items.Enum) != 2 || *tags.MaxItems != 3 {
		t.Fatalf("enum 標籤套到陣列元素：%+v", tags)
	}
	m := s.Property("members")
	if m.Type != "array" || m.Items.Type != "object" || strings.Join(m.Items.Required, ",") != "bot_id" ||
		m.Items.Property("bot_id").Resource != "bot" || len(m.Items.Property("role").Enum) != 2 {
		t.Fatalf("巢狀 struct：%+v", m.Items)
	}
	if meta := s.Property("meta"); meta.Type != "object" || meta.AdditionalProperties.(*Schema).Type != "integer" {
		t.Fatalf("map：%+v", meta)
	}
	if raw := s.Property("raw"); raw.Type != "" {
		t.Fatalf("RawMessage 是任意 JSON：%+v", raw)
	}
	if w := s.Property("when"); w.Type != "string" || w.Format != "date-time" {
		t.Fatalf("time：%+v", w)
	}
	if p := s.Property("ptr"); p.Type != "object" || p.Property("bot_id") == nil {
		t.Fatalf("指標：%+v", p)
	}
	if !s.Property("api_key").Secret || s.Property("Hidden") != nil || s.Property("private") != nil {
		t.Fatal("secret 或略過的欄位不對")
	}

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	for _, want := range []string{`"additionalProperties":false`, `"x-secret":true`, `"x-resource":"bot"`, `"required":["name"]`,
		`"enum":["read","operate","manage","admin"]`, `"description":"名稱"`} {
		if !strings.Contains(js, want) {
			t.Errorf("JSON Schema 少了 %s：%s", want, js)
		}
	}
	if strings.Contains(js, "Order") {
		t.Error("Order 不輸出到 JSON")
	}
}

type recursive struct {
	Children []recursive `json:"children"`
}

type badEnum struct {
	On bool `json:"on" enum:"yes"`
}

type badResource struct {
	ID string `json:"id" resource:"planet"`
}

type badResourceType struct {
	N int `json:"n" resource:"bot"`
}

type badMin struct {
	B bool `json:"b" min:"1"`
}

type badMap struct {
	M map[int]string `json:"m"`
}

type badChan struct {
	C chan int `json:"c"`
}

func TestSchemaErrors(t *testing.T) {
	for name, f := range map[string]func() (*Schema, error){
		"遞迴":         SchemaOf[recursive],
		"布林 enum":    SchemaOf[badEnum],
		"不認得的資源":     SchemaOf[badResource],
		"資源不是字串":     SchemaOf[badResourceType],
		"min 用在布林":   SchemaOf[badMin],
		"map key 數字": SchemaOf[badMap],
		"channel":    SchemaOf[badChan],
	} {
		if _, err := f(); err == nil {
			t.Errorf("%s：應該報錯", name)
		}
	}
	// 同一個型別出現兩次（不是遞迴）可以。
	type twice struct {
		A schemaMember `json:"a"`
		B schemaMember `json:"b"`
	}
	if _, err := SchemaOf[twice](); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaValidate(t *testing.T) {
	s, err := SchemaOf[schemaIn]()
	if err != nil {
		t.Fatal(err)
	}
	ok := `{"name":"x","count":3,"ratio":0.5,"level":2,"scope":"read","scopes":["admin"],"tags":["a"],
		"members":[{"bot_id":"b1","role":"lead"}],"meta":{"k":1},"raw":{"any":[1]},"when":"2026-01-01T00:00:00Z",
		"ptr":null,"note":"n","Untagged":true}`
	if errs := s.Validate(json.RawMessage(ok)); errs != nil {
		t.Fatalf("合法輸入不該有錯：%v", errs)
	}
	cases := map[string]string{
		`{"members":[]}`:                            "name=必填",
		`{"name":"x","count":0}`:                    "count=不能小於 1",
		`{"name":"x","count":1.5}`:                  "count=要是整數",
		`{"name":"x","count":"1"}`:                  "count=要是數字",
		`{"name":"x","level":4}`:                    "level=只能是 1、2、3",
		`{"name":"x","scope":"root"}`:               "scope=只能是 read、operate、manage、admin",
		`{"name":"x","scopes":[]}`:                  "scopes=至少要 1 個",
		`{"name":"x","tags":["a","a","a","a"]}`:     "tags=最多 3 個",
		`{"name":"x","members":[{"role":"lead"}]}`:  "members[0].bot_id=必填",
		`{"name":"x","members":[null]}`:             "members[0]=不能是空值",
		`{"name":"x","members":{}}`:                 "members=要是陣列",
		`{"name":"x","meta":{"k":"v"}}`:             "meta.k=要是數字",
		`{"name":"x","ptr":{"bot_id":"b","zzz":1}}`: "ptr.zzz=不認得這個欄位",
		`{"name":"x","Untagged":"yes"}`:             "Untagged=要是 true 或 false",
		`{"name":"x","ptr":"b"}`:                    "ptr=要是物件",
		`{"name":"x","labels":{"a":1}}`:             "labels.a=要是文字",
		`"x"`:                                       "=輸入要是 JSON 物件",
		`{"name":"x"} {}`:                           "=不是正確的 JSON",
	}
	for in, want := range cases {
		errs := s.Validate(json.RawMessage(in))
		var got []string
		for _, e := range errs {
			got = append(got, e.Field+"="+e.Message)
		}
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s：想要 [%s]，得到 %v", in, want, got)
		}
	}
	// 空輸入當成 {}。
	if errs := s.Validate(nil); len(errs) != 1 || errs[0].Field != "name" {
		t.Fatalf("空輸入：%v", errs)
	}
}
