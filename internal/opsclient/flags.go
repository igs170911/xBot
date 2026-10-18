package opsclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"xbot/internal/ops"
)

// ErrHelp 表示使用者要看說明（-h／--help）。
var ErrHelp = errors.New("help")

// Parsed 是解析指令列旗標的結果。
type Parsed struct {
	Input json.RawMessage
	Opts  ops.Options
}

// ParseArgs 依操作的輸入 schema 解析旗標，產生操作的輸入 JSON：
//
//	--name 值 或 --name=值          欄位（底線也可以寫成 -）
//	--a.b 值                        巢狀物件的欄位
//	--tags x --tags y              陣列：重複旗標逐一加入，或直接給 JSON：--tags '["x","y"]'
//	--obj '{"k":1}'                物件：給 JSON
//	--flag / --flag=false          布林
//	--json '{...}'（或 --json -）   整份輸入的 JSON（- 表示從 stdin 讀）；個別旗標會覆蓋它
//	--dry-run、--request-id 值      MCP-4
func ParseArgs(info ops.Info, args []string, stdin io.Reader) (Parsed, error) {
	var p Parsed
	root := map[string]any{}
	set := map[string]any{} // 旗標設的值（path → 值），最後蓋在 --json 上
	var order []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-h" || arg == "--help" {
			return p, ErrHelp
		}
		if !strings.HasPrefix(arg, "--") || arg == "--" {
			return p, fmt.Errorf("看不懂 %q：欄位要寫成 --欄位 值（xbot %s %s --help 看說明）", arg, info.Domain, info.Action)
		}
		name, value, hasValue := strings.Cut(arg[2:], "=")
		next := func() (string, error) {
			if hasValue {
				return value, nil
			}
			if i+1 >= len(args) {
				return "", fmt.Errorf("--%s 後面要接一個值", name)
			}
			i++
			return args[i], nil
		}
		switch name {
		case "dry-run", "dry_run":
			b, err := parseBool(name, value, hasValue)
			if err != nil {
				return p, err
			}
			p.Opts.DryRun = b
			continue
		case "request-id", "request_id":
			v, err := next()
			if err != nil {
				return p, err
			}
			p.Opts.RequestID = v
			continue
		case "json":
			v, err := next()
			if err != nil {
				return p, err
			}
			if v == "-" {
				b, err := io.ReadAll(stdin)
				if err != nil {
					return p, err
				}
				v = string(b)
			}
			var m map[string]any
			if err := decode(v, &m); err != nil || m == nil {
				return p, fmt.Errorf("--json 要是一個 JSON 物件")
			}
			root = m
			continue
		}
		path, s, err := resolve(info.Input, name)
		if err != nil {
			return p, err
		}
		key := strings.Join(path, ".")
		if s.Type == "boolean" {
			// --flag、--flag=false；下一個參數剛好是 true/false 也接受。
			if !hasValue && i+1 < len(args) && (args[i+1] == "true" || args[i+1] == "false") {
				i++
				value, hasValue = args[i], true
			}
			b, err := parseBool(name, value, hasValue)
			if err != nil {
				return p, err
			}
			setOnce(set, &order, key, b)
			continue
		}
		v, err := next()
		if err != nil {
			return p, err
		}
		if s.Type == "array" {
			if strings.HasPrefix(strings.TrimSpace(v), "[") {
				var arr []any
				if err := decode(v, &arr); err != nil {
					return p, fmt.Errorf("--%s 的 JSON 陣列格式不對：%v", name, err)
				}
				setOnce(set, &order, key, arr)
				continue
			}
			item, err := convert(name, s.Items, v)
			if err != nil {
				return p, err
			}
			prev, _ := set[key].([]any)
			setOnce(set, &order, key, append(prev, item))
			continue
		}
		val, err := convert(name, s, v)
		if err != nil {
			return p, err
		}
		setOnce(set, &order, key, val)
	}
	for _, key := range order {
		if err := setPath(root, strings.Split(key, "."), set[key]); err != nil {
			return p, err
		}
	}
	b, err := json.Marshal(root)
	if err != nil {
		return p, err
	}
	p.Input = b
	return p, nil
}

func setOnce(set map[string]any, order *[]string, key string, v any) {
	if _, ok := set[key]; !ok {
		*order = append(*order, key)
	}
	set[key] = v
}

func parseBool(name, value string, hasValue bool) (bool, error) {
	if !hasValue {
		return true, nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("--%s 只能是 true 或 false", name)
	}
	return b, nil
}

func decode(s string, v any) error {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	return dec.Decode(v)
}

// resolve 找出旗標名稱（可含 . 表示巢狀）對應的欄位路徑與 schema。
func resolve(root *ops.Schema, name string) ([]string, *ops.Schema, error) {
	s := root
	var path []string
	for _, part := range strings.Split(name, ".") {
		if s == nil || s.Type != "object" || s.Properties == nil {
			return nil, nil, fmt.Errorf("不認得 --%s", name)
		}
		key := part
		if s.Properties[key] == nil {
			key = strings.ReplaceAll(part, "-", "_")
		}
		next := s.Properties[key]
		if next == nil {
			return nil, nil, fmt.Errorf("不認得 --%s（可用：%s）", name, strings.Join(flagNames(s), "、"))
		}
		path = append(path, key)
		s = next
	}
	return path, s, nil
}

func flagNames(s *ops.Schema) []string {
	out := make([]string, 0, len(s.Order))
	for _, n := range s.Order {
		out = append(out, "--"+n)
	}
	return out
}

// convert 把文字轉成 schema 要的型別。
func convert(name string, s *ops.Schema, v string) (any, error) {
	if s == nil {
		return v, nil
	}
	switch s.Type {
	case "string":
		return v, nil
	case "integer":
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			return nil, fmt.Errorf("--%s 要是整數：%q", name, v)
		}
		return json.Number(v), nil
	case "number":
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			return nil, fmt.Errorf("--%s 要是數字：%q", name, v)
		}
		return json.Number(v), nil
	case "boolean":
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("--%s 只能是 true 或 false", name)
		}
		return b, nil
	case "object", "array":
		var x any
		if err := decode(v, &x); err != nil {
			return nil, fmt.Errorf("--%s 要給 JSON：%v", name, err)
		}
		return x, nil
	default: // 任意 JSON：看得懂就當 JSON，否則當文字
		var x any
		if decode(v, &x) == nil {
			return x, nil
		}
		return v, nil
	}
}

func setPath(root map[string]any, path []string, v any) error {
	m := root
	for i, k := range path[:len(path)-1] {
		child, ok := m[k].(map[string]any)
		if !ok {
			if m[k] != nil {
				return fmt.Errorf("--%s 不是物件，不能再往下設定", strings.Join(path[:i+1], "."))
			}
			child = map[string]any{}
			m[k] = child
		}
		m = child
	}
	m[path[len(path)-1]] = v
	return nil
}

// Usage 產生一個操作的指令列說明。
func Usage(info ops.Info) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "用法：xbot %s %s [旗標]\n\n%s\n", info.Domain, info.Action, info.Summary)
	if info.Description != "" {
		fmt.Fprintf(&b, "%s\n", info.Description)
	}
	fmt.Fprintf(&b, "\n操作：%s　權限：%s　風險：%s\n", info.Name, info.Scope, info.Risk)
	if info.Input != nil && len(info.Input.Order) > 0 {
		b.WriteString("\n欄位：\n")
		writeFlags(&b, info.Input, "")
	}
	b.WriteString("\n共用：\n")
	b.WriteString("  --json <JSON|->        整份輸入（- 從 stdin 讀）；個別旗標會覆蓋它\n")
	if info.Mutates {
		b.WriteString("  --dry-run              只回傳會發生什麼，不真的執行\n")
		b.WriteString("  --request-id <文字>    同一個 request_id 重送只會做一次\n")
	}
	return b.String()
}

func writeFlags(b *bytes.Buffer, s *ops.Schema, prefix string) {
	for _, name := range s.Order {
		p := s.Properties[name]
		flag := prefix + name
		var notes []string
		if s.IsRequired(name) {
			notes = append(notes, "必填")
		}
		if len(p.Enum) > 0 {
			vals := make([]string, len(p.Enum))
			for i, e := range p.Enum {
				vals[i] = fmt.Sprint(e)
			}
			notes = append(notes, "可用："+strings.Join(vals, "、"))
		}
		if p.Type == "array" && p.Items != nil && len(p.Items.Enum) > 0 {
			vals := make([]string, len(p.Items.Enum))
			for i, e := range p.Items.Enum {
				vals[i] = fmt.Sprint(e)
			}
			notes = append(notes, "可用："+strings.Join(vals, "、"))
		}
		desc := p.Description
		if len(notes) > 0 {
			desc = strings.TrimSpace(desc + "（" + strings.Join(notes, "；") + "）")
		}
		fmt.Fprintf(b, "  --%-20s %s\n", flag+" "+typeHint(p), desc)
		if p.Type == "object" && len(p.Order) > 0 {
			writeFlags(b, p, flag+".")
		}
	}
}

func typeHint(s *ops.Schema) string {
	switch s.Type {
	case "string":
		return "<文字>"
	case "integer":
		return "<整數>"
	case "number":
		return "<數字>"
	case "boolean":
		return ""
	case "array":
		return "<值…|JSON>"
	case "object":
		return "<JSON>"
	}
	return "<JSON>"
}
