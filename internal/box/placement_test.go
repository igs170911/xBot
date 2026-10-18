package box

import (
	"testing"

	"xbot/internal/hosts"
	"xbot/internal/store"
)

// B12（CL-1）：還沒有電腦的 Bot 建在 bot.host_id 指定的主機；之後一律跟著電腦所在的主機。
func TestCreateOnBotHost(t *testing.T) {
	e := setup(t)
	row, err := e.db.CreateHost(ctx, store.Host{Name: "cloud-01", Kind: "ssh", Address: "cloud-01", Port: 22})
	if err != nil {
		t.Fatal(err)
	}
	remote := &fakeHost{id: row.ID, dir: t.TempDir(), docker: newFakeDocker()}
	reg := hosts.NewRegistry(e.host)
	reg.Add(remote)
	m := New(e.db, reg, Options{})
	if err := e.db.SetBotHost(ctx, e.bot, row.ID); err != nil {
		t.Fatal(err)
	}
	s := ok[Status](t)(m.Create(ctx, e.bot))
	// 電腦和那台主機的網路防護（xbot-netguard）都在 cloud-01。
	if s.HostID != row.ID || len(remote.docker.containers) != 2 || remote.docker.containers[GuardName] == nil || len(e.docker.containers) != 0 {
		t.Fatalf("電腦應該建在 cloud-01：%+v（本機 %d 台、cloud-01 %d 台）", s, len(e.docker.containers), len(remote.docker.containers))
	}
	// Bot 改回自動擺放後，電腦還是在 cloud-01（換主機要搬家）。
	if err := e.db.SetBotHost(ctx, e.bot, ""); err != nil {
		t.Fatal(err)
	}
	if s := ok[Status](t)(m.Status(ctx, e.bot)); s.HostID != row.ID {
		t.Fatalf("電腦所在主機：%q", s.HostID)
	}
}
