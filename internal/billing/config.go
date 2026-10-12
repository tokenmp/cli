// Package billing 把一次调用的用量与两侧计价口径换成金额，并组装 web 的 billing 一行。
//
// 收入侧口径来自 web 的客户端密钥校验端点（用户当前套餐），成本侧单价来自本机配置的
// cost 段（渠道的上游成本）。两边都拿不到时不发 billing，只发调用记录，原因记 WARN。
package billing

import (
	"fmt"
	"strings"
)

// DefaultCurrency 是没写 currency 时的币种。web 的 billing_records.currency 默认也是它。
const DefaultCurrency = "CNY"

// 成本计费模式。与 web 的 CostBlock.cost_mode 同取值。
const (
	// CostModeToken 按 token 单价算，默认。
	CostModeToken = "token"
	// CostModeRequest 按次算，只看 cost_per_request。
	CostModeRequest = "request"
)

// Config 是配置文件里的 cost 段。整段不写、或没配任何渠道时只发 records、不发 billing。
type Config struct {
	// Currency 是金额的币种，不写用 DefaultCurrency。
	Currency string `yaml:"currency"`
	// Channels 按展开后的渠道名索引上游成本单价。展开后渠道名形如 pool/team-a，
	// 与 Outcome.Channel 同一口径。
	Channels map[string]ChannelCost `yaml:"channels"`
}

// ChannelCost 是一条渠道的上游成本单价，CostModels 是按模型的覆盖。
//
// 字段名与 web 的 CostBlock 一致，说明这组配置在线上挂在渠道分组上。
type ChannelCost struct {
	// CostMode 取 token 或 request，不写按 token。
	CostMode string `yaml:"cost_mode"`
	// 前四个是每百万 token 的单价。
	CostInputPerMillion      float64 `yaml:"cost_input_per_million"`
	CostOutputPerMillion     float64 `yaml:"cost_output_per_million"`
	CostCacheReadPerMillion  float64 `yaml:"cost_cache_read_per_million"`
	CostCacheWritePerMillion float64 `yaml:"cost_cache_write_per_million"`
	// CostPerRequest 是按次计费的单价，cost_mode 为 request 时用。
	CostPerRequest float64 `yaml:"cost_per_request"`
	// CostModels 是按模型的覆盖。写了某个模型就整组替换渠道级单价，没写的字段按 0；
	// 不写 cost_mode 时继承渠道级。
	CostModels map[string]ModelCost `yaml:"cost_models"`
}

// ModelCost 是一个模型的上游成本单价，字段名与 web 的 ModelCost 一致。
type ModelCost struct {
	CostMode             string  `yaml:"cost_mode"`
	InputPerMillion      float64 `yaml:"input_per_million"`
	OutputPerMillion     float64 `yaml:"output_per_million"`
	CacheReadPerMillion  float64 `yaml:"cache_read_per_million"`
	CacheWritePerMillion float64 `yaml:"cache_write_per_million"`
	PerRequest           float64 `yaml:"per_request"`
}

// Enabled 报告 cost 段是否配了渠道。为假时只发 records，不发 billing，也不记 WARN：
// 这与没有这一段时一样，是使用者的选择，不是算不出来。
func (c *Config) Enabled() bool {
	return c != nil && len(c.Channels) > 0
}

// Defaults 填入未写的默认值。校验前调用，与 core 的 Config.Defaults 同一时机。
func (c *Config) Defaults() {
	if strings.TrimSpace(c.Currency) == "" {
		c.Currency = DefaultCurrency
	}
}

// Validate 校验 cost 段。整形错误在这里一次报全。
func (c *Config) Validate() error {
	var problems []string
	if strings.TrimSpace(c.Currency) == "" {
		problems = append(problems, "currency 不能为空")
	}
	for name, channel := range c.Channels {
		if strings.TrimSpace(name) == "" {
			problems = append(problems, "channels 里有空渠道名")
		}
		problems = append(problems, channel.problems(name)...)
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("cost: %s", strings.Join(problems, "; "))
}

// problems 报一条渠道单价里的问题，供 Validate 汇总。
func (c ChannelCost) problems(channel string) []string {
	var problems []string
	if !validCostMode(c.CostMode) {
		problems = append(problems, fmt.Sprintf("channels.%s.cost_mode 只能是 %s 或 %s", channel, CostModeToken, CostModeRequest))
	}
	for model, override := range c.CostModels {
		if strings.TrimSpace(model) == "" {
			problems = append(problems, fmt.Sprintf("channels.%s.cost_models 里有空模型名", channel))
		}
		if !validCostMode(override.CostMode) {
			problems = append(problems, fmt.Sprintf("channels.%s.cost_models.%s.cost_mode 只能是 %s 或 %s",
				channel, model, CostModeToken, CostModeRequest))
		}
	}
	return problems
}

// validCostMode 报告计费模式是否合法。空串表示沿用上级，也合法。
func validCostMode(mode string) bool {
	switch mode {
	case "", CostModeToken, CostModeRequest:
		return true
	default:
		return false
	}
}

// priceFor 取某个模型在这条渠道上的有效单价。有模型覆盖就整组替换，没写 cost_mode
// 时继承渠道级；渠道级也没写时按 token。
func (c ChannelCost) priceFor(model string) (mode string, input, output, cacheRead, cacheWrite, perRequest float64) {
	mode = c.CostMode
	input, output = c.CostInputPerMillion, c.CostOutputPerMillion
	cacheRead, cacheWrite = c.CostCacheReadPerMillion, c.CostCacheWritePerMillion
	perRequest = c.CostPerRequest
	if override, ok := c.CostModels[model]; ok {
		if override.CostMode != "" {
			mode = override.CostMode
		}
		input, output = override.InputPerMillion, override.OutputPerMillion
		cacheRead, cacheWrite = override.CacheReadPerMillion, override.CacheWritePerMillion
		perRequest = override.PerRequest
	}
	if mode == "" {
		mode = CostModeToken
	}
	return mode, input, output, cacheRead, cacheWrite, perRequest
}
