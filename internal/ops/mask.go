package ops

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Masked 是遮蔽後的密鑰值。
const Masked = "***"

// 欄位名稱拆成單字後，含有這些字就當成密鑰（例：api_key、accessToken、password）。
var secretWords = map[string]bool{
	"key": true, "apikey": true, "token": true, "password": true, "passwd": true, "passphrase": true,
	"secret": true, "credential": true, "credentials": true, "authorization": true, "cookie": true, "otp": true,
}

// IsSecretName 回答這個欄位名稱看起來是不是密鑰。
func IsSecretName(name string) bool {
	for _, w := range splitWords(name) {
		if secretWords[w] {
			return true
		}
	}
	return false
}

// splitWords 把 snake_case、kebab-case、camelCase 拆成小寫單字。
func splitWords(name string) []string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	rs := []rune(name)
	for i, r := range rs {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
			continue
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]))):
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

// Mask 回傳遮蔽密鑰後的 JSON（稽核、待我處理卡片用）：schema 標了 secret 的欄位，
// 以及名稱像密鑰的欄位（key、token、password、secret…），值換成 "***"。
// raw 不是正確的 JSON 時回傳 {}，避免把讀不懂的內容原樣寫進紀錄。
func Mask(raw json.RawMessage, s *Schema) json.RawMessage {
	v, err := decodeJSON(raw)
	if err != nil {
		return json.RawMessage("{}")
	}
	out, err := json.Marshal(maskValue(v, s))
	if err != nil {
		return json.RawMessage("{}")
	}
	return out
}

func maskValue(v any, s *Schema) any {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			var cs *Schema
			if s != nil {
				cs = s.Properties[k]
				if cs == nil {
					cs, _ = s.AdditionalProperties.(*Schema)
				}
			}
			if child != nil && ((cs != nil && cs.Secret) || IsSecretName(k)) {
				val[k] = Masked
				continue
			}
			val[k] = maskValue(child, cs)
		}
	case []any:
		var is *Schema
		if s != nil {
			is = s.Items
		}
		for i, child := range val {
			val[i] = maskValue(child, is)
		}
	}
	return v
}
