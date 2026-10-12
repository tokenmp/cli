// Package clientauth 在转发路径上认出调用方是谁：按方言从请求里取出客户端密钥，
// 调 web 的校验端点换成身份，把结果按密钥的哈希缓存起来。
//
// 它做成 core 的宿主中间件：核验通过就把身份挂进 context，转发继续；核验不通过就
// 自己写出错误响应，不调 next。不调 next 有两个后果，都是有意为之：core 不写访问
// 日志、也不触发 OnRequestDone，因此被密钥拦下的请求不会进 request_records —— 这类
// 请求没有 user_id，也没有上游渠道，写进去只会污染全站视图的统计。
package clientauth

import (
	"context"
	"strings"

	"github.com/tokenmp/core/dialect"
)

// Identity 是一次调用认出的身份，取自 web 校验端点：user_id、api_key_id，以及该
// 用户当前套餐的计价口径 Pricing。
type Identity struct {
	UserID   int64
	APIKeyID int64
	// Pricing 是收入侧的计价口径，由 web 按用户的当前套餐给出。校验端点没给这一段
	// 时是零值，金额因此算不出来，宿主只推调用记录、不发 billing。
	Pricing Pricing
}

// 计价模式。web 的 plan.pricing_mode 用这两个取值。
const (
	// PricingModeMarkup 是成本乘倍率：revenue = cost × markup_multiplier。
	PricingModeMarkup = "markup"
	// PricingModeTable 是按套餐的模型价目表算。
	PricingModeTable = "table"
)

// Pricing 是 web 校验端点给出的计价口径。
type Pricing struct {
	// PlanID 是用户当前套餐的 id，没有套餐时为空。
	PlanID *int64
	// Mode 是 PricingModeMarkup 或 PricingModeTable，没有套餐时为空串。
	Mode string
	// MarkupMultiplier 只有 markup 模式用得上。
	MarkupMultiplier float64
	// Prices 是 table 模式的模型价目表，按模型名索引。markup 模式与没有套餐时为空。
	Prices map[string]Price
}

// Price 是套餐里一个模型的价。字段名与 web 的 plan_prices 一致，金额单位是每百万 token。
type Price struct {
	// CostMode 取 token 或 request。request 时按 PerRequest 与 RequestWeight 算，
	// 不看 token。
	CostMode             string
	InputPerMillion      float64
	OutputPerMillion     float64
	CacheReadPerMillion  float64
	CacheWritePerMillion float64
	PerRequest           float64
	RequestWeight        float64
}

// Price 按模型名取价目。没有该模型时返回假值。
func (p Pricing) Price(model string) (Price, bool) {
	price, ok := p.Prices[model]
	return price, ok
}

// identityKey 是身份在 context 里的键。用私有类型避免与别的包撞键。
type identityKey struct{}

// WithIdentity 把身份放进 context。
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identity)
}

// FromContext 取出本次请求的身份。没经过校验中间件时返回假值。
func FromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(Identity)
	return identity, ok
}

// 端点上与协议无关的固定路径。core 的 server 没有导出 Gemini 前缀，这里照 1 写一份，
// core 改路由时这里要跟着改（cmd/run.go 的启动信息也另写了一份）。
const geminiPrefix = "/v1beta/models/"

// dialectOfPath 按请求路径判断这是哪种方言的模型端点。不是模型端点时返回假值，
// 校验中间件由此放行 /healthz、/v1/models 与未知路径，交给 core 自己处理。
func dialectOfPath(path string) (dialect.Name, bool) {
	switch path {
	case dialect.OpenAIChat.LocalPath():
		return dialect.OpenAIChat, true
	case dialect.OpenAIResponses.LocalPath():
		return dialect.OpenAIResponses, true
	case dialect.ClaudeMessages.LocalPath():
		return dialect.ClaudeMessages, true
	}
	if strings.HasPrefix(path, geminiPrefix) {
		return dialect.GeminiGenerate, true
	}
	return "", false
}
