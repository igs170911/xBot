// Package version 記錄建置版本；make build 會用 -ldflags 寫入。
package version

// Version 是 git describe 的結果；開發中是 "dev"。
var Version = "dev"
