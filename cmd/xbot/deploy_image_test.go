package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"

	"xbot/internal/hosts"
)

type fakeRemote struct {
	inspect string
	loaded  []byte
	cmds    []string
	loadErr error
}

func (f *fakeRemote) output(remote string) (string, error) {
	f.cmds = append(f.cmds, remote)
	return f.inspect, nil
}

func (f *fakeRemote) pipe(remote string, r io.Reader) error {
	f.cmds = append(f.cmds, remote)
	b, err := io.ReadAll(r)
	f.loaded = b
	if f.loadErr != nil {
		return f.loadErr
	}
	return err
}

type fakeSaver struct{ data string }

func (s fakeSaver) ImageSave(context.Context, []string, ...client.ImageSaveOption) (client.ImageSaveResult, error) {
	return io.NopCloser(strings.NewReader(s.data)), nil
}

func inspectJSON(t *testing.T, img image.InspectResponse) string {
	t.Helper()
	b, err := json.Marshal([]image.InspectResponse{img})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSyncImage(t *testing.T) {
	ctx := context.Background()
	ref := hosts.ImageRef{Name: "xbot-box:dev", ID: "sha256:aaa", Arch: "amd64", Size: 10}
	saver := fakeSaver{data: "IMAGE-TAR"}
	var out bytes.Buffer

	// 那台沒有映像：傳過去。
	r := &fakeRemote{inspect: "[]\nXBOT-NO-IMAGE"}
	res := syncImage(ctx, saver, ref, "amd64", r, &out)
	if res.State != "sent" || string(r.loaded) != "IMAGE-TAR" || r.cmds[len(r.cmds)-1] != "docker load -q" {
		t.Fatalf("沒有映像時應傳過去：%+v %v", res, r.cmds)
	}

	// 一樣的映像：不傳。
	r = &fakeRemote{inspect: inspectJSON(t, image.InspectResponse{ID: "sha256:aaa"})}
	if res := syncImage(ctx, saver, ref, "amd64", r, &out); res.State != "same" || r.loaded != nil {
		t.Fatalf("同一版不該再傳：%+v", res)
	}

	// 舊版的映像：傳。
	r = &fakeRemote{inspect: inspectJSON(t, image.InspectResponse{ID: "sha256:old"})}
	if res := syncImage(ctx, saver, ref, "amd64", r, &out); res.State != "sent" {
		t.Fatalf("過期的映像應更新：%+v", res)
	}

	// 架構不同、沒有 Docker、不能用 Docker、這台沒有映像、傳輸失敗：都不擋部署，說明怎麼補。
	cases := []struct {
		name    string
		ref     hosts.ImageRef
		arch    string
		remote  *fakeRemote
		contain string
	}{
		{"架構", ref, "arm64", &fakeRemote{}, "make box"},
		{"沒有 docker", ref, "amd64", &fakeRemote{inspect: "XBOT-NO-DOCKER"}, "沒有 Docker"},
		{"權限", ref, "amd64", &fakeRemote{inspect: "XBOT-NO-ACCESS permission denied while trying to connect"}, "docker 群組"},
		{"沒啟動", ref, "amd64", &fakeRemote{inspect: "XBOT-NO-ACCESS Cannot connect to the Docker daemon"}, "沒有在執行"},
		{"這台沒有", hosts.ImageRef{Name: "xbot-box:dev"}, "amd64", &fakeRemote{}, "make box"},
		{"傳輸失敗", ref, "amd64", &fakeRemote{inspect: "XBOT-NO-IMAGE", loadErr: errors.New("no space left on device")}, "no space"},
	}
	for _, c := range cases {
		res := syncImage(ctx, saver, c.ref, c.arch, c.remote, &out)
		if res.State != "skipped" || !strings.Contains(res.Note, c.contain) {
			t.Fatalf("%s：%+v", c.name, res)
		}
	}
}
