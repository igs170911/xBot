package ops

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Schema 是由 Go struct 產生的 JSON Schema（draft 2020-12 的子集），MCP 工具、HTTP、CLI、前端型別共用。
//
// 支援的 struct 標籤：
//
//	json:"name,omitempty"  欄位名稱（json:"-" 略過）
//	desc:"說明"             description
//	required:"true"        必填；字串另外要求不能是空的
//	enum:"a,b,c"           只能是這幾個值（陣列則套用到每個元素）；型別有 EnumValues() []string 時自動帶入
//	min:"1" max:"64"       字串長度、數字大小、陣列個數的上下限
//	secret:"true"          密鑰：稽核時遮蔽，介面用密碼框（x-secret）
//	resource:"bot"         這個欄位是某個資源的 id（bot／team／host），用來檢查資源範圍（x-resource）
type Schema struct {
	Type                 string             `json:"type,omitempty"`
	Description          string             `json:"description,omitempty"`
	Format               string             `json:"format,omitempty"`
	Enum                 []any              `json:"enum,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	AdditionalProperties any                `json:"additionalProperties,omitempty"` // false 或 *Schema
	Items                *Schema            `json:"items,omitempty"`
	Minimum              *float64           `json:"minimum,omitempty"`
	Maximum              *float64           `json:"maximum,omitempty"`
	MinLength            *int               `json:"minLength,omitempty"`
	MaxLength            *int               `json:"maxLength,omitempty"`
	MinItems             *int               `json:"minItems,omitempty"`
	MaxItems             *int               `json:"maxItems,omitempty"`
	Secret               bool               `json:"x-secret,omitempty"`
	Resource             string             `json:"x-resource,omitempty"`

	// Order 是 Properties 依 struct 欄位宣告的順序（CLI 旗標、前端型別用）。
	Order []string `json:"-"`
	// OmitEmpty 是有 omitempty／omitzero 標籤的欄位：輸出時可能不出現（前端型別用）。
	OmitEmpty []string `json:"-"`
}

// Property 依名稱取出一個屬性；不是物件或沒有這個屬性時回傳 nil。
func (s *Schema) Property(name string) *Schema {
	if s == nil {
		return nil
	}
	return s.Properties[name]
}

// IsRequired 回答 name 是不是必填欄位。
func (s *Schema) IsRequired(name string) bool {
	return slices.Contains(s.Required, name)
}

// IsOmitEmpty 回答 name 欄位在輸出時是不是可能省略（omitempty／omitzero）。
func (s *Schema) IsOmitEmpty(name string) bool {
	return slices.Contains(s.OmitEmpty, name)
}

// SchemaOf 產生型別 T 的 JSON Schema。
func SchemaOf[T any]() (*Schema, error) {
	return schemaFor(reflect.TypeFor[T]())
}

func schemaFor(t reflect.Type) (*Schema, error) {
	g := &schemaGen{seen: map[reflect.Type]bool{}}
	return g.typ(t)
}

type schemaGen struct {
	seen map[reflect.Type]bool // 偵測遞迴型別（不支援，會讓 MCP 用戶端看不懂）
}

type enumer interface{ EnumValues() []string }

var (
	rawMessageType = reflect.TypeFor[json.RawMessage]()
	timeType       = reflect.TypeFor[time.Time]()
	enumerType     = reflect.TypeFor[enumer]()
)

func (g *schemaGen) typ(t reflect.Type) (*Schema, error) {
	switch t {
	case rawMessageType:
		return &Schema{}, nil // 任意 JSON
	case timeType:
		return &Schema{Type: "string", Format: "date-time"}, nil
	}
	var s *Schema
	switch t.Kind() {
	case reflect.Pointer:
		return g.typ(t.Elem())
	case reflect.String:
		s = &Schema{Type: "string"}
	case reflect.Bool:
		s = &Schema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s = &Schema{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		s = &Schema{Type: "number"}
	case reflect.Interface:
		return &Schema{}, nil
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return &Schema{Type: "string", Format: "byte"}, nil // encoding/json 用 base64
		}
		items, err := g.typ(t.Elem())
		if err != nil {
			return nil, err
		}
		s = &Schema{Type: "array", Items: items}
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("%s：map 的 key 只能是字串", t)
		}
		val, err := g.typ(t.Elem())
		if err != nil {
			return nil, err
		}
		s = &Schema{Type: "object", AdditionalProperties: val}
	case reflect.Struct:
		var err error
		if s, err = g.object(t); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%s：不支援的型別 %s", t, t.Kind())
	}
	if t.Implements(enumerType) {
		for _, v := range reflect.Zero(t).Interface().(enumer).EnumValues() {
			s.Enum = append(s.Enum, v)
		}
	}
	return s, nil
}

func (g *schemaGen) object(t reflect.Type) (*Schema, error) {
	if g.seen[t] {
		return nil, fmt.Errorf("%s：不支援遞迴的型別", t)
	}
	g.seen[t] = true
	defer delete(g.seen, t)
	s := &Schema{Type: "object", Properties: map[string]*Schema{}, AdditionalProperties: false}
	if err := g.fields(t, s); err != nil {
		return nil, err
	}
	return s, nil
}

func (g *schemaGen) fields(t reflect.Type, s *Schema) error {
	for f := range t.Fields() {
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, tagOpts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				if err := g.fields(ft, s); err != nil { // 內嵌 struct 的欄位攤平
					return err
				}
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		p, err := g.typ(f.Type)
		if err != nil {
			return fmt.Errorf("%s.%s：%w", t.Name(), f.Name, err)
		}
		if err := applyTags(p, f); err != nil {
			return fmt.Errorf("%s.%s：%w", t.Name(), f.Name, err)
		}
		if _, dup := s.Properties[name]; !dup {
			s.Order = append(s.Order, name)
			if slices.ContainsFunc(strings.Split(tagOpts, ","), func(o string) bool { return o == "omitempty" || o == "omitzero" }) {
				s.OmitEmpty = append(s.OmitEmpty, name)
			}
		}
		s.Properties[name] = p
		if f.Tag.Get("required") == "true" {
			s.Required = append(s.Required, name)
			if p.Type == "string" && p.MinLength == nil {
				p.MinLength = ptr(1)
			}
		}
	}
	return nil
}

func applyTags(p *Schema, f reflect.StructField) error {
	p.Description = f.Tag.Get("desc")
	p.Secret = f.Tag.Get("secret") == "true"
	p.Resource = f.Tag.Get("resource")
	switch p.Resource {
	case "", ResourceBot, ResourceTeam, ResourceHost:
	default:
		return fmt.Errorf("resource 標籤只能是 bot、team、host：%q", p.Resource)
	}
	target := p // enum 套用到陣列的元素
	if p.Type == "array" {
		target = p.Items
	}
	if p.Resource != "" && target.Type != "string" {
		return fmt.Errorf("resource 欄位要是字串或字串陣列")
	}
	if e := f.Tag.Get("enum"); e != "" {
		target.Enum = nil
		for _, v := range strings.Split(e, ",") {
			v = strings.TrimSpace(v)
			switch target.Type {
			case "string":
				target.Enum = append(target.Enum, v)
			case "integer", "number":
				n, err := strconv.ParseFloat(v, 64)
				if err != nil {
					return fmt.Errorf("enum 值 %q 不是數字", v)
				}
				target.Enum = append(target.Enum, n)
			default:
				return fmt.Errorf("enum 只能用在字串或數字")
			}
		}
	}
	for _, b := range []struct {
		tag string
		min bool
	}{{"min", true}, {"max", false}} {
		v := f.Tag.Get(b.tag)
		if v == "" {
			continue
		}
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("%s 標籤 %q 不是數字", b.tag, v)
		}
		switch p.Type {
		case "string":
			setBound(&p.MinLength, &p.MaxLength, b.min, int(n))
		case "array":
			setBound(&p.MinItems, &p.MaxItems, b.min, int(n))
		case "integer", "number":
			setBound(&p.Minimum, &p.Maximum, b.min, n)
		default:
			return fmt.Errorf("%s 標籤只能用在字串、數字或陣列", b.tag)
		}
	}
	return nil
}

func setBound[T any](lo, hi **T, isMin bool, v T) {
	if isMin {
		*lo = &v
	} else {
		*hi = &v
	}
}

func ptr[T any](v T) *T { return &v }

// ── 驗證 ──────────────────────────────────────────────

// Validate 檢查 raw（JSON）是否符合 schema，回傳所有有問題的欄位（沒有問題回 nil）。
// null 視同沒有填。
func (s *Schema) Validate(raw json.RawMessage) []FieldError {
	v, err := decodeJSON(raw)
	if err != nil {
		return []FieldError{{Message: "不是正確的 JSON"}}
	}
	var errs []FieldError
	s.validate(v, "", &errs)
	return errs
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func (s *Schema) validate(v any, path string, errs *[]FieldError) {
	bad := func(format string, args ...any) {
		*errs = append(*errs, FieldError{Field: path, Message: fmt.Sprintf(format, args...)})
	}
	switch s.Type {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			if path == "" {
				bad("輸入要是 JSON 物件")
			} else {
				bad("要是物件")
			}
			return
		}
		for _, name := range s.Required {
			if obj[name] == nil {
				*errs = append(*errs, FieldError{Field: join(path, name), Message: "必填"})
			}
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			val := obj[k]
			if val == nil {
				continue
			}
			if p := s.Properties[k]; p != nil {
				p.validate(val, join(path, k), errs)
			} else if extra, ok := s.AdditionalProperties.(*Schema); ok {
				extra.validate(val, join(path, k), errs)
			} else if s.AdditionalProperties == false {
				*errs = append(*errs, FieldError{Field: join(path, k), Message: "不認得這個欄位"})
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			bad("要是陣列")
			return
		}
		if s.MinItems != nil && len(arr) < *s.MinItems {
			bad("至少要 %d 個", *s.MinItems)
		}
		if s.MaxItems != nil && len(arr) > *s.MaxItems {
			bad("最多 %d 個", *s.MaxItems)
		}
		if s.Items != nil {
			for i, item := range arr {
				p := fmt.Sprintf("%s[%d]", path, i)
				if item == nil {
					*errs = append(*errs, FieldError{Field: p, Message: "不能是空值"})
					continue
				}
				s.Items.validate(item, p, errs)
			}
		}
	case "string":
		str, ok := v.(string)
		if !ok {
			bad("要是文字")
			return
		}
		n := len([]rune(str))
		if s.MinLength != nil && n < *s.MinLength {
			if *s.MinLength == 1 {
				bad("不能是空的")
			} else {
				bad("至少要 %d 個字", *s.MinLength)
			}
		}
		if s.MaxLength != nil && n > *s.MaxLength {
			bad("最多 %d 個字", *s.MaxLength)
		}
		s.checkEnum(str, bad)
	case "integer", "number":
		num, ok := v.(json.Number)
		if !ok {
			bad("要是數字")
			return
		}
		f, err := num.Float64()
		if err != nil {
			bad("要是數字")
			return
		}
		if s.Type == "integer" {
			if _, err := num.Int64(); err != nil {
				bad("要是整數")
				return
			}
		}
		if s.Minimum != nil && f < *s.Minimum {
			bad("不能小於 %v", *s.Minimum)
		}
		if s.Maximum != nil && f > *s.Maximum {
			bad("不能大於 %v", *s.Maximum)
		}
		s.checkEnum(f, bad)
	case "boolean":
		if _, ok := v.(bool); !ok {
			bad("要是 true 或 false")
		}
	}
}

func (s *Schema) checkEnum(v any, bad func(string, ...any)) {
	if len(s.Enum) == 0 {
		return
	}
	names := make([]string, len(s.Enum))
	for i, e := range s.Enum {
		if e == v {
			return
		}
		names[i] = fmt.Sprint(e)
	}
	bad("只能是 %s", strings.Join(names, "、"))
}
