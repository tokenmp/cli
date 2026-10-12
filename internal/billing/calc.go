package billing

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/tokenmp/core/config"
	"github.com/tokenmp/core/server"

	"github.com/tokenmp/cli/internal/clientauth"
)

// ErrNotBillable 表示这次调用不该发 billing：调用失败，或算出来收入不大于 0（免费
// 模型、命中套餐额度不额外计费之类）。它不是错误，调用方不该为它记 WARN。
var ErrNotBillable = errors.New("billing: 这次调用不计费")

// million 是单价的基数：配置与 web 的价目都按每百万 token 给。
const million = 1e6

// Calculator 按 cost 段的单价与 web 给的计价口径算一次调用的金额。
type Calculator struct {
	cost   Config
	models []config.Model
}

// New 构造计算器。models 是 cli 配置里的模型规则，table 模式按模型名查不到价目时
// 用它把上游名反查回用户看到的模型名，见 priceFor。
func New(cost Config, models []config.Model) *Calculator {
	if cost.Currency == "" {
		cost.Currency = DefaultCurrency
	}
	return &Calculator{cost: cost, models: models}
}

// Row 组装 billing 一行。
//
// 只在调用成功且算出的收入大于 0 时返回 nil 错误；其余情况返回 ErrNotBillable（不该
// 发，也不算问题）或具体错误（算不出来，调用方记 WARN 后只发 records）。
func (c *Calculator) Row(outcome server.Outcome, identity clientauth.Identity, now time.Time) (Row, error) {
	if outcome.Status >= 400 {
		return Row{}, ErrNotBillable
	}
	prompt, completion, cacheRead, cacheWrite, total := usageOf(outcome)
	// 计费输入要扣掉缓存读与缓存写：core 归一之后 PromptTokens 包含这两部分，
	// 不扣的话命中的 token 会先按输入价收一遍、再按缓存价收一遍，命中越多收得越多。
	billableInput := prompt - cacheRead - cacheWrite
	if billableInput < 0 {
		billableInput = 0
	}

	cost, costRule, err := c.costOf(outcome, billableInput, completion, cacheRead, cacheWrite)
	if err != nil {
		return Row{}, err
	}
	revenue, revenueMultiplier, revenueRule, err := revenueOf(
		identity.Pricing, outcome.Model, c.models, cost, billableInput, completion, cacheRead, cacheWrite)
	if err != nil {
		return Row{}, err
	}
	if revenue <= 0 {
		return Row{}, ErrNotBillable
	}

	userID, keyID := identity.UserID, identity.APIKeyID
	return Row{
		RequestID:              outcome.RequestID,
		UserID:                 &userID,
		APIKeyID:               &keyID,
		Model:                  outcome.Model,
		PromptTokens:           &prompt,
		CompletionTokens:       &completion,
		CacheReadTokens:        &cacheRead,
		CacheWriteTokens:       &cacheWrite,
		TotalTokens:            &total,
		CostAmount:             round8(cost),
		RevenueAmount:          round8(revenue),
		CostMultiplier:         round8(costRule.Multiplier),
		RevenueMultiplier:      round8(revenueMultiplier),
		CostMultiplierRules:    []Rule{costRule},
		RevenueMultiplierRules: []Rule{revenueRule},
		Currency:               c.cost.Currency,
		BillingMode:            identity.Pricing.Mode,
		Status:                 "settled",
		CreatedAt:              now.Add(-outcome.Duration),
	}, nil
}

// usageOf 取用量。上游没给用量时按 0 记：token 模式算出来是 0，收入为 0 会被挡掉；
// 按次计费的模型不看用量，仍然能算出金额。
func usageOf(outcome server.Outcome) (prompt, completion, cacheRead, cacheWrite, total int) {
	if usage := outcome.Usage; usage != nil {
		prompt, completion = usage.PromptTokens, usage.CompletionTokens
		cacheRead, cacheWrite = usage.CachedTokens, usage.CacheWriteTokens
		total = usage.TotalTokens
	}
	return prompt, completion, cacheRead, cacheWrite, total
}

// costOf 按 Outcome.Channel 与 Outcome.Model 取成本单价并算出成本。
//
// 渠道在 cost 段里没有对应项时返回错误：这条调用算不出付给上游多少钱，不发 billing。
func (c *Calculator) costOf(outcome server.Outcome, input, completion, cacheRead, cacheWrite int) (float64, Rule, error) {
	channel, ok := c.cost.Channels[outcome.Channel]
	if !ok {
		return 0, Rule{}, fmt.Errorf("cost 段里没有渠道 %s 的单价", outcome.Channel)
	}
	mode, inputPrice, outputPrice, cacheReadPrice, cacheWritePrice, perRequest := channel.priceFor(outcome.Model)
	rule := Rule{Scope: "channel", Channel: outcome.Channel, Model: outcome.Model, CostMode: mode, Multiplier: 1}
	if mode == CostModeRequest {
		return perRequest, rule, nil
	}
	return amount(input, completion, cacheRead, cacheWrite, inputPrice, outputPrice, cacheReadPrice, cacheWritePrice), rule, nil
}

// revenueOf 按 web 给的计价口径算收入，返回收入、收入倍率与命中的规则。
func revenueOf(
	pricing clientauth.Pricing,
	model string,
	rules []config.Model,
	cost float64,
	input, completion, cacheRead, cacheWrite int,
) (float64, float64, Rule, error) {
	switch pricing.Mode {
	case clientauth.PricingModeMarkup:
		// markup 模式：用户在成本上加价，收入直接由成本乘倍率得到。
		rule := Rule{Scope: "plan", PlanID: pricing.PlanID, Mode: pricing.Mode, Multiplier: pricing.MarkupMultiplier}
		return cost * pricing.MarkupMultiplier, pricing.MarkupMultiplier, rule, nil
	case clientauth.PricingModeTable:
		price, matched, ok := priceFor(pricing, model, rules)
		if !ok {
			return 0, 0, Rule{}, fmt.Errorf("table 模式的价目表里没有模型 %s", model)
		}
		rule := Rule{Scope: "plan", PlanID: pricing.PlanID, Mode: pricing.Mode, Model: matched, CostMode: price.CostMode, Multiplier: 1}
		if price.CostMode == CostModeRequest {
			weight := price.RequestWeight
			if weight == 0 {
				weight = 1
			}
			rule.Multiplier = weight
			return price.PerRequest * weight, weight, rule, nil
		}
		return amount(input, completion, cacheRead, cacheWrite, price.InputPerMillion, price.OutputPerMillion, price.CacheReadPerMillion, price.CacheWritePerMillion), 1, rule, nil
	default:
		return 0, 0, Rule{}, errors.New("没有可用的计价口径：密钥校验端点没给套餐或计价模式")
	}
}

// priceFor 在价目表里按模型名取价。
//
// table 模式的价目表按用户看到的模型名建，而 Outcome.Model 是插件改写之后参与匹配的
// 名字（core 在 Outcome 的注释里写明），两者改写脱节时会直接查不到。这时按 cli 的模型
// 规则反查：找上游名等于它的那条规则，用它匹配的模型名再查一次价目表。
// 反查只看 Upstream 的字面值；写了 ${model} 占位符的规则翻译不出固定名字，跳过。
func priceFor(pricing clientauth.Pricing, model string, rules []config.Model) (clientauth.Price, string, bool) {
	if price, ok := pricing.Price(model); ok {
		return price, model, true
	}
	for _, rule := range rules {
		if rule.Upstream == "" || rule.Upstream != model {
			continue
		}
		if price, ok := pricing.Price(rule.Match); ok {
			return price, rule.Match, true
		}
	}
	return clientauth.Price{}, "", false
}

// amount 按四个 token 分组与各自单价算一笔金额，单价是每百万 token。
func amount(input, completion, cacheRead, cacheWrite int, inputPrice, outputPrice, cacheReadPrice, cacheWritePrice float64) float64 {
	return float64(input)*inputPrice/million +
		float64(completion)*outputPrice/million +
		float64(cacheRead)*cacheReadPrice/million +
		float64(cacheWrite)*cacheWritePrice/million
}

// round8 把金额按 8 位小数四舍五入，与 web 的 DECIMAL(18,8) 对齐。
func round8(value float64) float64 {
	return math.Round(value*1e8) / 1e8
}
