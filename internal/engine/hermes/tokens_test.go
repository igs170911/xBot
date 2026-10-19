package hermes

import (
	"context"
	"testing"

	"gopkg.in/yaml.v3"

	"xbot/internal/limits"
)

// 省 token 的設定（2026-10-19 實測）：視窗、摘要門檻、背景檢討、標題、子代理。經過 config.yaml 來回一次（YAML 讀回來是 int）。
func TestConfigSavesTokens(t *testing.T) {
	ctx := context.Background()
	f := &fakeFiles{files: map[string][]byte{
		"/hermes/config.yaml": []byte("compression:\n  threshold_tokens: 100000\n  proactive_prune_tokens: 48000\n" +
			"auxiliary:\n  background_review:\n    enabled: true\n    timeout: 120\n  title_generation:\n    timeout: 30\n" +
			"skills:\n  external_dirs: [/x]\n"),
	}, uids: map[string]int{}}
	p := Profile{BotID: "b1", Name: "水哥哥", ProviderID: "opencode-go", Model: "deepseek-v4-flash", WorkerKey: "k"}
	if err := Provision(ctx, f, "c1", p); err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(f.files["/hermes/config.yaml"], &cfg); err != nil {
		t.Fatal(err)
	}
	get := func(path ...string) any {
		var v any = cfg
		for _, k := range path {
			m, _ := v.(map[string]any)
			v = m[k]
		}
		return v
	}
	for _, c := range []struct {
		path []string
		want any
	}{
		{[]string{"model", "context_length"}, limits.HermesContextLength},
		{[]string{"compression", "threshold_tokens"}, limits.HermesCompressTokens}, // 舊版的 100K 換掉
		{[]string{"compression", "proactive_prune_tokens"}, limits.HermesPruneTokens},
		{[]string{"skills", "creation_nudge_interval"}, limits.HermesSkillNudgeInterval},
		{[]string{"skills", "external_dirs"}, []any{"/x"}},
		{[]string{"auxiliary", "background_review", "max_input_tokens"}, limits.HermesReviewInputTokens},
		{[]string{"auxiliary", "background_review", "enabled"}, true}, // 檢討照跑，只是有上限
		{[]string{"auxiliary", "background_review", "timeout"}, 120},
		{[]string{"auxiliary", "title_generation", "enabled"}, false},
		{[]string{"auxiliary", "title_generation", "timeout"}, 30},
		{[]string{"delegation", "max_iterations"}, limits.HermesDelegationMaxIterations},
		{[]string{"memory", "nudge_interval"}, nil}, // 記憶的檢討照 Hermes 預設
	} {
		got := get(c.path...)
		if want, ok := c.want.([]any); ok {
			if g, _ := got.([]any); len(g) != len(want) || g[0] != want[0] {
				t.Errorf("%v = %v，要 %v", c.path, got, c.want)
			}
			continue
		}
		if got != c.want {
			t.Errorf("%v = %v，要 %v", c.path, got, c.want)
		}
	}

	// 使用者自己改過的摘要門檻保留。
	cfg = Config(map[string]any{"compression": map[string]any{"threshold_tokens": 50000}}, p)
	if v := cfg["compression"].(map[string]any)["threshold_tokens"]; v != 50000 {
		t.Fatalf("使用者的門檻被換掉：%v", v)
	}
}
