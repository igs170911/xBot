// Package web 提供打包後的前端檔案。
//
// 只有用 -tags webembed 建置（make build）時才會把 web/dist 嵌入執行檔；
// 平常的 go test、make dev 不需要先建前端。
package web

import "io/fs"

// Dist 回傳前端檔案；沒有嵌入時回傳 nil。
func Dist() fs.FS { return dist() }
