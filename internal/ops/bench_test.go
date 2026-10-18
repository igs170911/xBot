package ops

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"xbot/internal/store"
)

// 效能基準（v1.0，結果記在 docs/perf.md）：make bench，或 go test -run '^$' -bench . ./internal/ops/
// 中介層本身的成本：讀的操作（使用者本人）、會改東西的操作（寫稽核＋事件），以及多個讀同時進來。

type benchIn struct {
	Name string `json:"name" max:"20"`
}
type benchOut struct {
	Name string `json:"name"`
}

func benchRegistry(b *testing.B) *Registry {
	b.Helper()
	db, err := store.Open(context.Background(), filepath.Join(b.TempDir(), store.FileName))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	r := New(Config{DB: db})
	handle := func(_ context.Context, _ *Call, in benchIn) (benchOut, error) { return benchOut(in), nil }
	Register(r, Op[benchIn, benchOut]{Name: "xbot.bench.read", Summary: "讀", Scope: ScopeRead, Risk: RiskSafe, Handle: handle})
	Register(r, Op[benchIn, benchOut]{Name: "xbot.bench.write", Summary: "寫", Scope: ScopeManage, Risk: RiskSafe, Handle: handle})
	return r
}

var benchRaw = json.RawMessage(`{"name":"小幫手"}`)

func BenchmarkInvokeRead(b *testing.B) {
	r := benchRegistry(b)
	id := Owner("bench")
	for b.Loop() {
		if _, err := r.Invoke(context.Background(), id, "xbot.bench.read", benchRaw, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}

// 多個讀同時進來（例：手機和電腦同時開著）：讀不該排在單一寫入連線後面。
func BenchmarkInvokeReadParallel(b *testing.B) {
	r := benchRegistry(b)
	id := Owner("bench")
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := r.Invoke(context.Background(), id, "xbot.bench.read", benchRaw, Options{}); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkInvokeWrite(b *testing.B) {
	r := benchRegistry(b)
	id := Owner("bench")
	for b.Loop() {
		if _, err := r.Invoke(context.Background(), id, "xbot.bench.write", benchRaw, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
