package configmgr

import (
	"strings"
	"testing"
)

// costConfig 是带 cost 段的沙箱配置。
const costConfig = `listen: 127.0.0.1:9999

channels:
  - name: example
    dialect: openai
    base_url: https://api.example.com/v1
    auth: {style: none}

models:
  - {match: "gpt-*", channel: example}

plugins:
  dir: plugins

cost:
  currency: CNY
  channels:
    example:
      cost_input_per_million: 2.5
      cost_output_per_million: 10
      cost_cache_read_per_million: 0.25
      cost_cache_write_per_million: 1.25
      cost_models:
        gpt-4o:
          input_per_million: 5
          output_per_million: 15
`

// TestCostSectionIsParsed cli 自己的 cost 段要能被接住：core 的解码开着 KnownFields，
// 这一段要是没接住，配置文件直接加载失败。
func TestCostSectionIsParsed(t *testing.T) {
	env, err := Load(writeConfig(t, costConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if env.Cost == nil {
		t.Fatal("cost 段没有被解析出来")
	}
	if !env.Cost.Enabled() {
		t.Error("配了渠道应当算启用")
	}
	channel, ok := env.Cost.Channels["example"]
	if !ok {
		t.Fatal("没有解析出 example 渠道的单价")
	}
	if channel.CostInputPerMillion != 2.5 || channel.CostOutputPerMillion != 10 {
		t.Errorf("渠道单价是 %v/%v", channel.CostInputPerMillion, channel.CostOutputPerMillion)
	}
	if override, ok := channel.CostModels["gpt-4o"]; !ok || override.InputPerMillion != 5 {
		t.Errorf("模型覆盖没有解析对: %+v", override)
	}
}

// TestCostAbsent 整段没写时 Cost 为 nil，config show 里也不出现 cost。
func TestCostAbsent(t *testing.T) {
	env, err := Load(writeConfig(t, costConfig[:strings.Index(costConfig, "\ncost:")]+"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if env.Cost != nil {
		t.Fatalf("没写 cost 段时不该有值: %+v", env.Cost)
	}
	pruned, err := env.Pruned()
	if err != nil {
		t.Fatalf("Pruned: %v", err)
	}
	if _, ok := pruned["cost"]; ok {
		t.Errorf("config show 里不该出现 cost: %+v", pruned)
	}
}

// TestCostInvalidMode 计费模式写错时加载报错。
func TestCostInvalidMode(t *testing.T) {
	content := strings.Replace(costConfig, "      cost_input_per_million: 2.5", "      cost_mode: monthly", 1)
	if _, err := Load(writeConfig(t, content)); err == nil {
		t.Fatal("cost_mode 不合法时应当报错")
	}
}
