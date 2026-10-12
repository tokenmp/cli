package billing

import "testing"

// TestConfigDefaults 没写 currency 时补上默认币种。
func TestConfigDefaults(t *testing.T) {
	cfg := Config{Channels: map[string]ChannelCost{"openai": {}}}
	cfg.Defaults()
	if cfg.Currency != DefaultCurrency {
		t.Errorf("currency 默认值是 %q，期望 %q", cfg.Currency, DefaultCurrency)
	}
}

// TestConfigEnabled 没有渠道时不算启用：只发 records，不发 billing，也不记 WARN。
func TestConfigEnabled(t *testing.T) {
	var nilConfig *Config
	if nilConfig.Enabled() {
		t.Error("nil 配置不该算启用")
	}
	if (&Config{Currency: "CNY"}).Enabled() {
		t.Error("没有渠道时不该算启用")
	}
	if !(&Config{Channels: map[string]ChannelCost{"openai": {}}}).Enabled() {
		t.Error("配了渠道应当算启用")
	}
}

// TestConfigValidate 计费模式写错时报出来。
func TestConfigValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{name: "合法", cfg: Config{Currency: "CNY", Channels: map[string]ChannelCost{"openai": {CostMode: CostModeToken}}}},
		{name: "模式为空算继承", cfg: Config{Currency: "CNY", Channels: map[string]ChannelCost{"openai": {}}}},
		{name: "按次计费", cfg: Config{Currency: "CNY", Channels: map[string]ChannelCost{"openai": {CostMode: CostModeRequest, CostPerRequest: 0.01}}}},
		{
			name:    "渠道模式不合法",
			cfg:     Config{Currency: "CNY", Channels: map[string]ChannelCost{"openai": {CostMode: "monthly"}}},
			wantErr: true,
		},
		{
			name: "模型覆盖模式不合法",
			cfg: Config{Currency: "CNY", Channels: map[string]ChannelCost{"openai": {
				CostModels: map[string]ModelCost{"gpt-4o": {CostMode: "monthly"}},
			}}},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr && err == nil {
				t.Fatal("期望报错，实际通过")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("不该报错: %v", err)
			}
		})
	}
}

// TestChannelCostPriceFor 模型覆盖整组替换渠道级单价，没写 cost_mode 时继承。
func TestChannelCostPriceFor(t *testing.T) {
	channel := ChannelCost{
		CostMode:            CostModeToken,
		CostInputPerMillion: 1,
		CostModels: map[string]ModelCost{
			"gpt-4o": {InputPerMillion: 3},
			"o1":     {CostMode: CostModeRequest, PerRequest: 0.1},
		},
	}
	mode, input, _, _, _, perRequest := channel.priceFor("gpt-4o")
	if mode != CostModeToken || input != 3 || perRequest != 0 {
		t.Errorf("覆盖后是 %q/%v/%v，期望 token/3/0", mode, input, perRequest)
	}
	mode, input, _, _, _, perRequest = channel.priceFor("o1")
	if mode != CostModeRequest || input != 0 || perRequest != 0.1 {
		t.Errorf("o1 是 %q/%v/%v，期望 request/0/0.1", mode, input, perRequest)
	}
	mode, input, _, _, _, _ = channel.priceFor("other")
	if mode != CostModeToken || input != 1 {
		t.Errorf("未覆盖的模型是 %q/%v，期望 token/1", mode, input)
	}
}
