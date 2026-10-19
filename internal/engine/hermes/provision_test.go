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
	if mcp := cfg["mcp_servers"].(map[string]any)[XBotMCPServer].(map[string]any); mcp["args"].([]any)[0] != XBotMCP {
		t.Fatalf("xbot mcp：%v", mcp)
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

// web_extract 不能落到只能搜尋的 ddgs（每次都失敗）；使用者自己選的後端保留。
// delegate_task 不開：Hermes 的子代理一律在背景跑，XBot 看不到、停不了。
func TestConfigWebExtractAndNoBackgroundDelegation(t *testing.T) {
	p := Profile{BotID: "b1", Name: "小幫手", ProviderID: "openai", Model: "gpt-x"}
	for _, c := range []struct {
		web  map[string]any
		want any
	}{
		{nil, ExtractBackend},
		{map[string]any{"backend": "ddgs"}, ExtractBackend},
		{map[string]any{"backend": "tavily"}, nil},        // 共用的後端會讀網頁
		{map[string]any{"extract_backend": "exa"}, "exa"}, // 使用者選的
		{map[string]any{"backend": "", "extract_backend": ""}, ExtractBackend},
	} {
		cfg := map[string]any{}
		if c.web != nil {
			cfg["web"] = c.web
		}
		cfg = Config(cfg, p)
		if got := cfg["web"].(map[string]any)["extract_backend"]; got != c.want {
			t.Errorf("web=%v：extract_backend=%v，要 %v", c.web, got, c.want)
		}
		tools := cfg["platform_toolsets"].(map[string]any)["cli"].([]any)
		for _, ts := range tools {
			if ts == "delegation" {
				t.Fatalf("不能開 delegation：%v", tools)
			}
		}
		if len(tools) == 0 || tools[0] != "terminal" {
			t.Fatalf("工具組：%v", tools)
		}
	}
}
