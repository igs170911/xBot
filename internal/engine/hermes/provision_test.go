package hermes

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"gopkg.in/yaml.v3"
)

// fakeFiles 是記憶體裡的容器檔案系統。
type fakeFiles struct {
	files map[string][]byte
	uids  map[string]int
}

func (f *fakeFiles) CopyToContainer(_ context.Context, _ string, o client.CopyToContainerOptions) (client.CopyToContainerResult, error) {
	tr := tar.NewReader(o.Content)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return client.CopyToContainerResult{}, nil
		}
		if err != nil {
			return client.CopyToContainerResult{}, err
		}
		b, _ := io.ReadAll(tr)
		f.files[o.DestinationPath+"/"+h.Name] = b
		f.uids[o.DestinationPath+"/"+h.Name] = h.Uid
	}
}

func (f *fakeFiles) CopyFromContainer(_ context.Context, _ string, o client.CopyFromContainerOptions) (client.CopyFromContainerResult, error) {
	b, ok := f.files[o.SourcePath]
	if !ok {
		return client.CopyFromContainerResult{}, cerrdefs.ErrNotFound
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "x", Size: int64(len(b)), Mode: 0o600, Typeflag: tar.TypeReg})
	_, _ = tw.Write(b)
	_ = tw.Close()
	return client.CopyFromContainerResult{Content: io.NopCloser(&buf)}, nil
}

func TestProvisionWritesConfigWithoutRealKey(t *testing.T) {
	ctx := context.Background()
	f := &fakeFiles{files: map[string][]byte{
		"/hermes/config.yaml": []byte("memory:\n  enabled: true\nmodel:\n  default: old\nweb:\n  provider: x\n  backend: tavily\n"),
		"/hermes/.env":        []byte("TAVILY_API_KEY=user-saved\nXBOT_MODEL_TOKEN=old-key\n"),
	}, uids: map[string]int{}}
	p := Profile{BotID: "b1", Name: "小幫手", Soul: "細心", ProviderID: "openai", Model: "gpt-x", WorkerKey: "xbw_b1.sig", UID: 1000, GID: 1000}
	if err := Provision(ctx, f, "c1", p); err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(f.files["/hermes/config.yaml"], &cfg); err != nil {
		t.Fatal(err)
	}
	model := cfg["model"].(map[string]any)
	if model["default"] != "gpt-x" || model["provider"] != Provider || model["base_url"] != "http://127.0.0.1:8642/model/openai/v1" {
		t.Fatalf("model：%v", model)
	}
	prov := cfg["providers"].(map[string]any)["product"].(map[string]any)
	if prov["key_env"] != TokenEnv || prov["transport"] != "chat_completions" {
		t.Fatalf("providers：%v", prov)
	}
	// Hermes 自己的設定保留；web.provider 清掉。
	if cfg["memory"].(map[string]any)["enabled"] != true {
		t.Fatalf("應保留 memory：%v", cfg)
	}
	if web := cfg["web"].(map[string]any); web["backend"] != "tavily" || web["provider"] != nil {
		t.Fatalf("web：%v", web)
	}
	if mcp := cfg["mcp_servers"].(map[string]any)["computer"].(map[string]any); mcp["args"].([]any)[0] != DesktopMCP {
		t.Fatalf("mcp：%v", mcp)
	}
	env := string(f.files["/hermes/.env"])
	if env != "TAVILY_API_KEY=user-saved\nXBOT_MODEL_TOKEN=xbw_b1.sig\n" {
		t.Fatalf(".env：%q", env)
	}
	if string(f.files["/hermes/.worker-key"]) != "xbw_b1.sig\n" || f.uids["/hermes/.worker-key"] != 1000 {
		t.Fatalf(".worker-key：%q", f.files["/hermes/.worker-key"])
	}
	if soul := string(f.files["/hermes/SOUL.md"]); !strings.HasPrefix(soul, "# 小幫手\n\n細心") {
		t.Fatalf("SOUL：%q", soul)
	}
	if err := Provision(ctx, f, "c1", Profile{BotID: "b1"}); err == nil {
		t.Fatal("缺少模型時應該失敗")
	}
}
