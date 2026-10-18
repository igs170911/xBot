package hosts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"xbot/internal/store"
)

func TestEnsureLocal(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	// 指向不存在的 socket：測試不依賴這台機器有沒有 Docker，主機會記成離線。
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(dir, "no-docker.sock"))
	db, err := store.Open(ctx, filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	h, err := EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	again, err := EnsureLocal(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.ID() != h.ID() {
		t.Fatalf("第二次啟動應該沿用同一列：%s vs %s", again.ID(), h.ID())
	}
	all, err := db.ListHosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Kind != "local" || all[0].Name != LocalName || all[0].Status != "offline" {
		t.Fatalf("主機表：%+v", all)
	}
	if h.Kind() != "local" || h.Addr(1234) != "127.0.0.1:1234" {
		t.Fatalf("本機：%s %s", h.Kind(), h.Addr(1234))
	}

	ipc, err := h.PrepareIPC("b1")
	if err != nil {
		t.Fatal(err)
	}
	if ipc != filepath.Join(dir, "bots", "b1", "ipc") {
		t.Fatalf("ipc 路徑：%s", ipc)
	}
	if st, err := os.Stat(ipc); err != nil || st.Mode().Perm() != 0o711 {
		t.Fatalf("ipc 權限：%v %v", st.Mode(), err)
	}

	r := NewRegistry(h)
	if got, err := r.Get(""); err != nil || got != Host(h) {
		t.Fatalf("預設主機：%v %v", got, err)
	}
	if _, err := r.Get("other"); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("未知主機：%v", err)
	}
}
