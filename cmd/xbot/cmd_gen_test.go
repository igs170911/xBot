package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"xbot/internal/api"
)

// web/src/api/generated.ts 要和登錄表一致；不一致時執行 make gen。
func TestGeneratedTSUpToDate(t *testing.T) {
	cur, err := os.ReadFile(filepath.Join("..", "..", api.GeneratedTSPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(cur, api.GenerateTS(cliRegistry().List())) {
		t.Fatalf("%s 過期了，請執行 make gen 並提交", api.GeneratedTSPath)
	}
}
