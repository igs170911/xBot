package hermes

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"slices"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"gopkg.in/yaml.v3"

	"xbot/internal/limits"
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
// delegate_task 開著：hermes_host.py 讓子代理在同一輪裡做完（不在背景跑）。
func TestConfigWebExtractAndInlineDelegation(t *testing.T) {
	p := Profile{BotID: "b1", Name: "小幫手", ProviderID: "openai", Model: "gpt-x"}
	// 搜尋沒有選後端時用 Exa 的免金鑰版（使用者要求，DuckDuckGo 只有標題摘要又常被限流）；使用者選的保留。
	for _, c := range []struct {
		web          map[string]any
		want, search any
	}{
		{nil, ExtractBackend, SearchBackend},
		{map[string]any{"backend": "ddgs"}, ExtractBackend, nil},                       // 使用者選的 ddgs 搜尋保留
		{map[string]any{"backend": "tavily"}, nil, nil},                                // 共用的後端會讀網頁、會搜尋
		{map[string]any{"extract_backend": "exa"}, "exa", SearchBackend},               // 使用者選的
		{map[string]any{"search_backend": "brave-free"}, ExtractBackend, "brave-free"}, // 使用者選的
		{map[string]any{"backend": "", "extract_backend": ""}, ExtractBackend, SearchBackend},
	} {
		cfg := map[string]any{}
		if c.web != nil {
			cfg["web"] = c.web
		}
		cfg = Config(cfg, p)
		if got := cfg["web"].(map[string]any)["extract_backend"]; got != c.want {
			t.Errorf("web=%v：extract_backend=%v，要 %v", c.web, got, c.want)
		}
		if got := cfg["web"].(map[string]any)["search_backend"]; got != c.search {
			t.Errorf("web=%v：search_backend=%v，要 %v", c.web, got, c.search)
		}
		if tf := cfg["mcp_servers"].(map[string]any)["computer"].(map[string]any)["tools"].(map[string]any); tf["resources"] != false || tf["prompts"] != false {
			t.Fatalf("桌面工具不列 resources／prompts 附屬工具：%v", tf)
		}
		if cfg[SearchSnippetKey] != limits.HermesSearchSnippetRunes {
			t.Fatalf("搜尋摘要上限：%v", cfg[SearchSnippetKey])
		}
		tools := cfg["platform_toolsets"].(map[string]any)["cli"].([]any)
		if !slices.Contains(tools, any("delegation")) {
			t.Fatalf("要開 delegation：%v", tools)
		}
		if len(tools) == 0 || tools[0] != "terminal" {
			t.Fatalf("工具組：%v", tools)
		}
	}
}
